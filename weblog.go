package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// webLog appends the URLs opencode looked at. Search queries have no URL, so
// a websearch contributes its result URLs; a webfetch contributes the page it
// opened. Keeping it to one URL per line makes the file easy to sort and diff.
type webLog struct {
	mu   sync.Mutex
	path string
}

func newWebLog(path string) *webLog {
	path = strings.TrimSpace(path)
	if dir := filepath.Dir(path); path != "" && dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("web log dir %s: %v", dir, err)
		}
	}
	return &webLog{path: path}
}

func (w *webLog) Write(urls []string) {
	if w == nil || w.path == "" || len(urls) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("web log %s: %v", w.path, err)
		return
	}
	defer f.Close()
	var b strings.Builder
	for _, u := range urls {
		b.WriteString(u)
		b.WriteByte('\n')
	}
	if _, err := f.WriteString(b.String()); err != nil {
		log.Printf("web log %s: %v", w.path, err)
	}
}

var (
	searchResultURL = regexp.MustCompile(`(?m)^URL:\s*(https?://\S+)`)
	commandURL      = regexp.MustCompile(`https?://[^\s"'<>\\]+`)
)

// webURLs reads opencode's JSON event stream. It returns every fetched URL,
// every result URL from a websearch, and the URLs inside bash commands (the
// agent often uses curl instead of the dedicated fetch tool), in order.
func webURLs(raw []byte) []string {
	var out []string
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev agentEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "tool_use" || ev.Part.Type != "tool" {
			continue
		}
		switch ev.Part.Tool {
		case "webfetch":
			if u := cleanURL(ev.Part.State.Input.URL); u != "" {
				out = append(out, u)
			}
		case "websearch":
			seen := make(map[string]bool)
			for _, m := range searchResultURL.FindAllStringSubmatch(ev.Part.State.Output, -1) {
				u := cleanURL(m[1])
				if u == "" || seen[u] {
					continue
				}
				seen[u] = true
				out = append(out, u)
			}
		case "bash":
			seen := make(map[string]bool)
			for _, m := range commandURL.FindAllString(ev.Part.State.Input.Command, -1) {
				u := cleanURL(m)
				if u == "" || seen[u] || isLocalURL(u) {
					continue
				}
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return true
	}
	return false
}

func cleanURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, ".,;:!?)]}\"'")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	if len(u) > 2000 {
		u = u[:2000]
	}
	return u
}
