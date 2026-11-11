package priceskeptic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramNotifierSend(t *testing.T) {
	type request struct {
		method  string
		path    string
		chatID  string
		text    string
		preview string
	}
	reqs := make(chan request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		reqs <- request{
			method:  r.Method,
			path:    r.URL.Path,
			chatID:  r.PostForm.Get("chat_id"),
			text:    r.PostForm.Get("text"),
			preview: r.PostForm.Get("disable_web_page_preview"),
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	n := newTelegramNotifier("TOKEN", "4242")
	if n == nil {
		t.Fatal("newTelegramNotifier returned nil for a full token and chat ID")
	}
	n.baseURL = srv.URL
	n.client = srv.Client()

	if err := n.Send(context.Background(), "hei <verden>"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := <-reqs
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/botTOKEN/sendMessage" {
		t.Errorf("path = %q, want /botTOKEN/sendMessage", got.path)
	}
	if got.chatID != "4242" {
		t.Errorf("chat_id = %q, want 4242", got.chatID)
	}
	if got.text != "hei <verden>" {
		t.Errorf("text = %q, want the original text", got.text)
	}
	if got.preview != "true" {
		t.Errorf("disable_web_page_preview = %q, want true", got.preview)
	}
}

func TestTelegramNotifierError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()

	n := newTelegramNotifier("TOKEN", "4242")
	n.baseURL = srv.URL
	n.client = srv.Client()

	err := n.Send(context.Background(), "hei")
	if err == nil {
		t.Fatal("Send returned nil for a 400 response")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("error = %q, want the API description", err)
	}
}

func TestNewTelegramNotifierDisabled(t *testing.T) {
	for _, tc := range []struct{ token, chat string }{
		{"", "4242"},
		{"TOKEN", ""},
		{"", ""},
	} {
		if n := newTelegramNotifier(tc.token, tc.chat); n != nil {
			t.Errorf("newTelegramNotifier(%q, %q) = %+v, want nil", tc.token, tc.chat, n)
		}
	}
}

func TestStoreMessage(t *testing.T) {
	now := time.Date(2026, 11, 11, 14, 5, 0, 0, time.Local)
	f := func(v float64) *float64 { return &v }

	st := newTestStore(t)
	if _, err := st.Replace("Kaffetrakter", []offer{{
		Product: "Moccamaster KBGT", Store: "Elkjøp", StoreRegion: "no",
		URL: "https://www.elkjop.no/moccamaster", Price: 1099, Shipping: f(99), Stock: "in_stock",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Replace("Støvsuger", []offer{{
		Product: "V15 Detect", Store: "Power", StoreRegion: "no",
		URL: "https://www.power.no/v15", Price: 799, Shipping: f(0), Stock: "in_stock",
	}}); err != nil {
		t.Fatal(err)
	}

	msg := st.Message(now)
	for _, want := range []string{
		"priceskeptic 2026-11-11 14:05",
		"Støvsuger", "Power", "799.00 kr", "V15 Detect", "https://www.power.no/v15",
		"Kaffetrakter", "Elkjøp", "1198.00 kr", "Moccamaster KBGT", "https://www.elkjop.no/moccamaster",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q:\n%s", want, msg)
		}
	}
	if strings.Index(msg, "Støvsuger") > strings.Index(msg, "Kaffetrakter") {
		t.Errorf("items are not cheapest first:\n%s", msg)
	}

	t.Run("no offers", func(t *testing.T) {
		msg := newTestStore(t).Message(now)
		if !strings.Contains(msg, "No offers yet.") {
			t.Errorf("message = %q, want No offers yet.", msg)
		}
	})

	t.Run("unknown shipping", func(t *testing.T) {
		st := newTestStore(t)
		if _, err := st.Replace("Kaffetrakter", []offer{{
			Product: "Moccamaster KBGT", Store: "Elkjøp", StoreRegion: "no",
			URL: "https://www.elkjop.no/moccamaster", Price: 299, Stock: "in_stock",
		}}); err != nil {
			t.Fatal(err)
		}
		msg := st.Message(now)
		if !strings.Contains(msg, "299.00 kr + shipping") {
			t.Errorf("message = %q, want the price plus a shipping caveat", msg)
		}
	})
}

func TestNotifyDebounce(t *testing.T) {
	fake := &countingNotifier{}
	s := &server{
		results: newTestStore(t),
		notify:  fake,
		root:    context.Background(),
	}

	s.notifyResults()
	s.notifyResults()
	if got := fake.count(); got != 1 {
		t.Fatalf("automatic sends = %d, want 1 inside the window", got)
	}

	rec := httptest.NewRecorder()
	s.handleNotify(rec, httptest.NewRequest(http.MethodPost, "/notify", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manual status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"sent":true`) {
		t.Errorf("manual body = %q, want sent true", body)
	}
	if got := fake.count(); got != 2 {
		t.Fatalf("sends after manual = %d, want 2", got)
	}

	s.notifyResults()
	if got := fake.count(); got != 2 {
		t.Errorf("automatic send after the manual one = %d, want the window still closed", got)
	}

	rec = httptest.NewRecorder()
	(&server{results: s.results}).handleNotify(rec, httptest.NewRequest(http.MethodPost, "/notify", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status without a notifier = %d, want 400", rec.Code)
	}
}

func TestNotifyRetriesAfterFailure(t *testing.T) {
	fake := &failOnceNotifier{}
	s := &server{
		results: newTestStore(t),
		notify:  fake,
		root:    context.Background(),
	}

	s.notifyResults()
	if got := fake.count(); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}
	snap := s.notifySnapshot()
	if snap.LastError != "telegram down" {
		t.Errorf("last_error = %q, want the send error", snap.LastError)
	}
	if snap.LastSentAt != "" {
		t.Errorf("last_sent_at = %q, want empty after a failure", snap.LastSentAt)
	}

	s.notifyResults()
	if got := fake.count(); got != 2 {
		t.Fatalf("sends after the retry = %d, want 2 (a failure must not start the cooldown)", got)
	}
	snap = s.notifySnapshot()
	if snap.LastError != "" {
		t.Errorf("last_error = %q, want empty after a successful send", snap.LastError)
	}
	if snap.LastSentAt == "" {
		t.Error("last_sent_at is empty after a successful send")
	}
}

func newTestStore(t *testing.T) *resultStore {
	t.Helper()
	dir := t.TempDir()
	st, err := openResultStore(filepath.Join(dir, "results.csv"), filepath.Join(dir, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

type countingNotifier struct {
	mu    sync.Mutex
	sends int
}

func (c *countingNotifier) Send(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends++
	return nil
}

func (c *countingNotifier) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sends
}

type failOnceNotifier struct {
	mu    sync.Mutex
	sends int
}

func (n *failOnceNotifier) Send(context.Context, string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sends++
	if n.sends == 1 {
		return errors.New("telegram down")
	}
	return nil
}

func (n *failOnceNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sends
}
