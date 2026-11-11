package prisjeger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebURLsReadsTools(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"type":"tool_use","part":{"type":"tool","tool":"webfetch","state":{"input":{"url":"https://example.com/a"},"output":"ok"}}}`,
		`{"type":"tool_use","part":{"type":"tool","tool":"websearch","state":{"input":{"query":"x"},"output":"Title: A\nURL: https://a.example/x\nText: ...\nTitle: B\nURL: https://b.example/y?q=1\n"}}}`,
		`{"type":"text","part":{"text":"done"}}`,
	}, "\n"))
	got := webURLs(raw)
	want := []string{"https://example.com/a", "https://a.example/x", "https://b.example/y?q=1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("url[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWebURLsDeduplicatesWithinSearch(t *testing.T) {
	raw := []byte(`{"type":"tool_use","part":{"type":"tool","tool":"websearch","state":{"output":"URL: https://a.example/x\nURL: https://a.example/x\n"}}}`)
	got := webURLs(raw)
	if len(got) != 1 || got[0] != "https://a.example/x" {
		t.Fatalf("got %v", got)
	}
}

func TestWebLogAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "websearch.log")
	w := newWebLog(path)
	w.Write([]string{"https://a.example/1", "https://b.example/2"})
	w.Write([]string{"https://c.example/3"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || lines[0] != "https://a.example/1" || lines[2] != "https://c.example/3" {
		t.Fatalf("log = %q", data)
	}
}

func TestCleanURL(t *testing.T) {
	cases := map[string]string{
		" https://example.com/x) ": "https://example.com/x",
		"ftp://example.com/x":      "",
		"example.com":              "",
	}
	for in, want := range cases {
		if got := cleanURL(in); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWebURLsReadsBashCommands(t *testing.T) {
	raw := []byte(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"input":{"command":"curl -s https://a.example/x -o /tmp/x && curl http://localhost:8080/status"}}}}`)
	got := webURLs(raw)
	if len(got) != 1 || got[0] != "https://a.example/x" {
		t.Fatalf("got %v, want only the public curl URL", got)
	}
}
