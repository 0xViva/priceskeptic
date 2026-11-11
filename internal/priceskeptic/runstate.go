package priceskeptic

import (
	"sync"
	"time"
)

type runPhase string

const (
	runRunning  runPhase = "running"
	runFinished runPhase = "finished"
)

type runSnapshot struct {
	ID         int64    `json:"id"`
	Source     string   `json:"source"`
	State      runPhase `json:"state"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at,omitempty"`
	Total      int      `json:"total"`
	Active     int      `json:"active"`
	Done       int      `json:"done"`
	Failed     int      `json:"failed"`
	LastError  string   `json:"last_error"`
}

type runTracker struct {
	mu      sync.Mutex
	current runSnapshot
	nextID  int64
}

// begin is the only way into the running state, so two callers can never both
// decide they saw an idle tracker.
func (t *runTracker) begin(source string, total int) (runSnapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current.State == runRunning || total <= 0 {
		return t.current, false
	}
	t.nextID++
	t.current = runSnapshot{
		ID:        t.nextID,
		Source:    source,
		State:     runRunning,
		StartedAt: time.Now().Format(time.RFC3339),
		Total:     total,
		Active:    total,
	}
	return t.current, true
}

func (t *runTracker) finish(runID int64, item string, err error) (runSnapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current.ID != runID || t.current.State != runRunning {
		return t.current, false
	}
	if err != nil {
		t.current.Failed++
		t.current.LastError = item + ": " + err.Error()
	} else {
		t.current.Done++
	}
	if t.current.Active > 0 {
		t.current.Active--
	}
	if t.current.Active == 0 {
		t.current.State = runFinished
		t.current.FinishedAt = time.Now().Format(time.RFC3339)
		return t.current, true
	}
	return t.current, false
}

func (t *runTracker) snapshot() runSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current
}

func (t *runTracker) running() bool {
	return t.snapshot().State == runRunning
}
