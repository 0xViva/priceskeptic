package priceskeptic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type config struct {
	addr          string
	list          string
	csvPath       string
	obsidian      string
	skill         string
	opencode      string
	model         string
	schedule      string
	times         []int
	timeout       time.Duration
	parallel      int
	trust         *trustpilotCache
	minTrust      float64
	requireDeal   bool
	webLog        *webLog
	postalCode    string
	telegramToken string
	telegramChat  string
}

type searchResponse struct {
	Started bool   `json:"started"`
	RunID   int64  `json:"run_id"`
	State   string `json:"state"`
	Items   int    `json:"items"`
	CSVFile string `json:"csv_file"`
}

type statusResponse struct {
	State     string         `json:"state"`
	Run       runSnapshot    `json:"run"`
	NextRunAt string         `json:"next_run_at"`
	Notify    notifySnapshot `json:"notify"`
	Active    int            `json:"active"`
	Done      int            `json:"done"`
	Failed    int            `json:"failed"`
	LastError string         `json:"last_error"`
	CSVFile   string         `json:"csv_file"`
}

type server struct {
	cfg     config
	results *resultStore
	notify  notifier
	sem     chan struct{}
	root    context.Context
	runs    runTracker

	startMu sync.Mutex

	notifyMu   sync.Mutex
	lastNotify time.Time
	notifyErr  string
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, `priceskeptic: price hunter

POST /search    start a search run for every item in %s
POST /notify    send the current results to Telegram now
GET  /status    run state, counts, next run, notification state
GET  /results   download the current CSV file
GET  /healthz   ok

flags: -addr -list -csv -obsidian -skill -opencode -model -timeout -parallel -schedule
       -trust-cache -min-trust -require-deal -web-log -postal-code
       -telegram-token -telegram-chat -backfill-trust
env:   PRICESKEPTIC_ADDR PRICESKEPTIC_LIST PRICESKEPTIC_CSV PRICESKEPTIC_OBSIDIAN PRICESKEPTIC_SKILL
       PRICESKEPTIC_OPENCODE PRICESKEPTIC_MODEL PRICESKEPTIC_TIMEOUT PRICESKEPTIC_PARALLEL
       PRICESKEPTIC_SCHEDULE PRICESKEPTIC_TELEGRAM_TOKEN PRICESKEPTIC_TELEGRAM_CHAT
`, s.cfg.list)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.runs.snapshot()
	state := "idle"
	if snap.State == runRunning {
		state = "running"
	}
	nextRunAt := ""
	if at := nextRun(time.Now(), s.cfg.times); !at.IsZero() {
		nextRunAt = at.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, statusResponse{
		State:     state,
		Run:       snap,
		NextRunAt: nextRunAt,
		Notify:    s.notifySnapshot(),
		Active:    snap.Active,
		Done:      snap.Done,
		Failed:    snap.Failed,
		LastError: snap.LastError,
		CSVFile:   s.cfg.csvPath,
	})
}

func (s *server) handleResults(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, s.cfg.csvPath)
}

var errEmptyList = errors.New("shopping list is empty")

func (s *server) startRun(source string) (runSnapshot, bool, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	// A start request during a run is a pure no-op. Checking here keeps the
	// list read and the prune from touching an active run's rows.
	if snap := s.runs.snapshot(); snap.State == runRunning {
		log.Printf("%s run skipped: run %d (%s) is active", source, snap.ID, snap.Source)
		return snap, false, nil
	}

	items, err := readList(s.cfg.list)
	if err != nil {
		return runSnapshot{}, false, err
	}
	if len(items) == 0 {
		return runSnapshot{}, false, errEmptyList
	}
	if err := s.results.PruneItems(items); err != nil {
		return runSnapshot{}, false, err
	}

	snap, started := s.runs.begin(source, len(items))
	if !started {
		log.Printf("%s run skipped: run %d (%s) is active", source, snap.ID, snap.Source)
		return snap, false, nil
	}
	for _, item := range items {
		// The request context dies with the response, so runs use the server root.
		go s.search(snap.ID, item)
	}
	log.Printf("%s run %d started: %d items", source, snap.ID, len(items))
	return snap, true, nil
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	snap, started, err := s.startRun("search")
	switch {
	case errors.Is(err, errEmptyList):
		http.Error(w, "shopping list is empty: "+s.cfg.list, http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "read list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, searchResponse{
		Started: started,
		RunID:   snap.ID,
		State:   string(snap.State),
		Items:   snap.Total,
		CSVFile: s.cfg.csvPath,
	})
}

func (s *server) handleNotify(w http.ResponseWriter, r *http.Request) {
	if s.notify == nil {
		http.Error(w, "telegram notifications are not configured", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), notifyTimeout)
	defer cancel()
	if err := s.notify.Send(ctx, s.results.Message(time.Now())); err != nil {
		log.Printf("telegram: manual send failed: %v", err)
		s.setNotifyError(err)
		http.Error(w, "send notification: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordNotifySend()
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func parseSchedule(v string) ([]int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	var times []int
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		t, err := time.Parse("15:04", part)
		if err != nil || len(part) != 5 {
			return nil, fmt.Errorf("%q is not HH:MM", part)
		}
		times = append(times, t.Hour()*60+t.Minute())
	}
	sort.Ints(times)
	return times, nil
}

func nextRun(now time.Time, times []int) time.Time {
	if len(times) == 0 {
		return time.Time{}
	}
	y, mo, d := now.Date()
	for _, m := range times {
		if at := time.Date(y, mo, d, m/60, m%60, 0, 0, now.Location()); at.After(now) {
			return at
		}
	}
	m := times[0]
	return time.Date(y, mo, d+1, m/60, m%60, 0, 0, now.Location())
}

func (s *server) runScheduler() {
	for next := nextRun(time.Now(), s.cfg.times); !next.IsZero(); next = nextRun(time.Now(), s.cfg.times) {
		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.root.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if _, _, err := s.startRun("scheduled"); err != nil {
			log.Printf("scheduled run: %v", err)
		}
	}
}

func shorten(v string, n int) string {
	v = strings.TrimSpace(v)
	if len(v) <= n {
		return v
	}
	return v[:n-3] + "..."
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
