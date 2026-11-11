package prisjeger

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunTrackerStartsOnlyOneRun(t *testing.T) {
	var runs runTracker
	first, started := runs.begin("search", 2)
	if !started {
		t.Fatal("first begin did not start a run")
	}
	second, started := runs.begin("scheduled", 5)
	if started {
		t.Fatal("second begin started a run while the first was active")
	}
	if second.ID != first.ID {
		t.Fatalf("second run id = %d, want the active run's id %d", second.ID, first.ID)
	}
	if second.Source != "search" || second.Total != 2 || second.Active != 2 {
		t.Fatalf("second snapshot = %+v, want the active run's state", second)
	}
	if !runs.running() {
		t.Fatal("tracker is not running after begin")
	}
}

func TestRunTrackerFinishTransitionsRunningToFinished(t *testing.T) {
	var runs runTracker
	run, _ := runs.begin("search", 2)

	snap, finished := runs.finish(run.ID, "kaffe", errors.New("no offers"))
	if finished {
		t.Fatal("run finished after one of two items")
	}
	if snap.Active != 1 || snap.Done != 0 || snap.Failed != 1 {
		t.Fatalf("counts = active %d, done %d, failed %d, want 1/0/1", snap.Active, snap.Done, snap.Failed)
	}
	if snap.LastError != "kaffe: no offers" {
		t.Fatalf("last_error = %q, want the item and the error", snap.LastError)
	}
	if snap.State != runRunning {
		t.Fatalf("state = %q, want running", snap.State)
	}

	snap, finished = runs.finish(run.ID, "filter", nil)
	if !finished {
		t.Fatal("last item did not finish the run")
	}
	if snap.Active != 0 || snap.Done != 1 || snap.Failed != 1 {
		t.Fatalf("final counts = active %d, done %d, failed %d, want 0/1/1", snap.Active, snap.Done, snap.Failed)
	}
	if snap.State != runFinished {
		t.Fatalf("state = %q, want finished", snap.State)
	}
	if snap.FinishedAt == "" {
		t.Fatal("finished_at is empty on a finished run")
	}
	if runs.running() {
		t.Fatal("tracker still reports running after the last item")
	}
}

func TestRunTrackerIgnoresStaleFinishes(t *testing.T) {
	var runs runTracker
	run, _ := runs.begin("search", 1)

	if _, finished := runs.finish(run.ID+1, "kaffe", nil); finished {
		t.Fatal("a finish for another run id finished the active run")
	}
	if snap := runs.snapshot(); snap.Done != 0 || snap.Active != 1 {
		t.Fatalf("stale finish changed the snapshot: %+v", snap)
	}

	runs.finish(run.ID, "kaffe", nil)
	snap, finished := runs.finish(run.ID, "kaffe", nil)
	if finished {
		t.Fatal("a second finish of the same run finished it again")
	}
	if snap.Done != 1 || snap.Active != 0 {
		t.Fatalf("second finish changed the counts: %+v", snap)
	}
}

func TestRunTrackerRejectsEmptyRun(t *testing.T) {
	var runs runTracker
	if snap, started := runs.begin("empty", 0); started {
		t.Fatalf("an empty run started: %+v", snap)
	}
	if runs.running() {
		t.Fatal("an empty run left the tracker running")
	}

	run, started := runs.begin("search", 1)
	if !started {
		t.Fatal("a one-item run did not start")
	}
	if snap, finished := runs.finish(run.ID, "", nil); !finished || snap.Active != 0 {
		t.Fatalf("one-item run did not finish cleanly: %+v, finished=%v", snap, finished)
	}
}

func TestHandleStatusReportsRun(t *testing.T) {
	s := &server{cfg: config{csvPath: "results.csv", times: []int{0}}}
	run, _ := s.runs.begin("search", 3)
	s.runs.finish(run.ID, "kaffe", nil)

	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
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
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	if got.State != "running" {
		t.Errorf("state = %q, want running", got.State)
	}
	if got.Run.ID != run.ID || got.Run.State != runRunning || got.Run.Total != 3 || got.Run.Active != 2 {
		t.Errorf("run = %+v, want the active run with 2 active items", got.Run)
	}
	if got.NextRunAt == "" {
		t.Error("next_run_at is empty with a configured time")
	}
	at, err := time.Parse(time.RFC3339, got.NextRunAt)
	if err != nil {
		t.Errorf("next_run_at %q is not RFC3339: %v", got.NextRunAt, err)
	} else if at.IsZero() {
		t.Error("next_run_at parsed as the zero time")
	}
	if got.Notify.Enabled {
		t.Error("notify.enabled = true without a notifier")
	}
	if got.Active != 2 || got.Done != 1 || got.Failed != 0 || got.LastError != "" {
		t.Errorf("legacy counters = %d/%d/%d/%q, want 2/1/0/\"\"", got.Active, got.Done, got.Failed, got.LastError)
	}
	if got.CSVFile != "results.csv" {
		t.Errorf("csv_file = %q", got.CSVFile)
	}
}

func TestHandleSearchWhileRunActiveLaunchesNothing(t *testing.T) {
	dir := t.TempDir()
	listPath := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(listPath, []byte("kaffe\nfilter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cfg:     config{list: listPath, csvPath: filepath.Join(dir, "results.csv")},
		results: newTestStore(t),
		sem:     make(chan struct{}, 1),
		root:    context.Background(),
	}
	active, started := s.runs.begin("scheduled", 4)
	if !started {
		t.Fatal("could not start the active run")
	}

	rec := httptest.NewRecorder()
	s.handleSearch(rec, httptest.NewRequest(http.MethodPost, "/search", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	var got searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Started {
		t.Error("started = true while another run is active")
	}
	if got.RunID != active.ID {
		t.Errorf("run_id = %d, want the active run's id %d", got.RunID, active.ID)
	}
	if got.Items != 4 || got.State != "running" {
		t.Errorf("items/state = %d/%q, want the active run's 4/running", got.Items, got.State)
	}
	if len(s.sem) != 0 {
		t.Fatalf("launched %d searches while a run was active", len(s.sem))
	}
	if snap := s.runs.snapshot(); snap.Active != 4 || snap.Total != 4 {
		t.Fatalf("active run changed: %+v", snap)
	}
}
