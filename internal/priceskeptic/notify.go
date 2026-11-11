package prisjeger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	telegramAPI     = "https://api.telegram.org"
	notifyInterval  = 6 * time.Hour
	notifyTimeout   = 30 * time.Second
	maxMessageRunes = 4000
)

type notifier interface {
	Send(ctx context.Context, text string) error
}

type telegramNotifier struct {
	token   string
	chatID  string
	client  *http.Client
	baseURL string
}

func newTelegramNotifier(token, chatID string) *telegramNotifier {
	token = strings.TrimSpace(token)
	chatID = strings.TrimSpace(chatID)
	if token == "" || chatID == "" {
		return nil
	}
	return &telegramNotifier{
		token:   token,
		chatID:  chatID,
		client:  http.DefaultClient,
		baseURL: telegramAPI,
	}
}

func (t *telegramNotifier) Send(ctx context.Context, text string) error {
	form := url.Values{
		"chat_id":                  {t.chatID},
		"text":                     {text},
		"disable_web_page_preview": {"true"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/bot"+t.token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	var reply struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(body, &reply)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reason := reply.Description
		if reason == "" {
			reason = strings.TrimSpace(string(body))
		}
		return fmt.Errorf("telegram: %s: %s", resp.Status, reason)
	}
	if !reply.OK {
		reason := reply.Description
		if reason == "" {
			reason = strings.TrimSpace(string(body))
		}
		return fmt.Errorf("telegram: not ok: %s", reason)
	}
	return nil
}

type notifySnapshot struct {
	Enabled    bool   `json:"enabled"`
	LastSentAt string `json:"last_sent_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func (s *server) notifySnapshot() notifySnapshot {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	snap := notifySnapshot{
		Enabled:   s.notify != nil,
		LastError: s.notifyErr,
	}
	if !s.lastNotify.IsZero() {
		snap.LastSentAt = s.lastNotify.Format(time.RFC3339)
	}
	return snap
}

func (s *server) setNotifyError(err error) {
	s.notifyMu.Lock()
	s.notifyErr = err.Error()
	s.notifyMu.Unlock()
}

func (s *server) recordNotifySend() {
	s.notifyMu.Lock()
	s.lastNotify = time.Now()
	s.notifyErr = ""
	s.notifyMu.Unlock()
}

// notifyResults sends the results after a run ends. It stays quiet when no
// notifier is configured and when the last successful send is under six hours
// old. A failed send does not start that window, so the next run retries.
func (s *server) notifyResults() {
	if s.notify == nil {
		return
	}
	s.notifyMu.Lock()
	if time.Since(s.lastNotify) < notifyInterval {
		s.notifyMu.Unlock()
		return
	}
	s.notifyMu.Unlock()

	ctx, cancel := context.WithTimeout(s.root, notifyTimeout)
	defer cancel()
	if err := s.notify.Send(ctx, s.results.Message(time.Now())); err != nil {
		log.Printf("telegram: send failed: %v", err)
		s.setNotifyError(err)
		return
	}
	s.recordNotifySend()
}

// Message renders the current rows as the plain-text notification body.
func (st *resultStore) Message(now time.Time) string {
	st.mu.Lock()
	rows := make(map[string][]storedRow, len(st.rows))
	for item, list := range st.rows {
		rows[item] = append([]storedRow(nil), list...)
	}
	st.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "prisjeger %s\n", now.Format("2006-01-02 15:04"))
	items := sortedItems(rows)
	if len(items) == 0 {
		b.WriteString("\nNo offers yet.\n")
		return b.String()
	}
	for _, item := range items {
		b.WriteString("\n")
		b.WriteString(item)
		b.WriteString("\n")
		for _, row := range sortedByTotal(rows[item]) {
			b.WriteString("  ")
			b.WriteString(offerLine(row))
			b.WriteString("\n")
			if row.url != "" {
				b.WriteString("  ")
				b.WriteString(row.url)
				b.WriteString("\n")
			}
		}
	}
	return truncateRunes(b.String(), maxMessageRunes)
}

func offerLine(row storedRow) string {
	parts := []string{row.store, totalText(row), row.stock, row.product}
	line := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		line = append(line, part)
	}
	return strings.Join(line, ", ")
}

// totalText shows the landed total, or the item price with a shipping caveat
// when the total is unknown.
func totalText(row storedRow) string {
	if total := numberText(row.total); total != "" {
		return total + " kr"
	}
	if price := numberText(row.price); price != "" {
		return price + " kr + shipping"
	}
	return ""
}

func numberText(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "NaN") {
		return ""
	}
	return v
}
