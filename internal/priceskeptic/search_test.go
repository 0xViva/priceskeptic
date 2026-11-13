package priceskeptic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeOpencode(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecChildKeepsCompletedAnswerOnNonZeroExit(t *testing.T) {
	script := fakeOpencode(t, `cat <<'EOF'
{"type":"text","sessionID":"ses_test","part":{"type":"text","text":"{\"product\":\"P\",\"store\":\"S\",\"url\":\"https://example.no/p\",\"price_nok\":100}"}}
{"type":"step_finish","sessionID":"ses_test","part":{"type":"step-finish","reason":"stop"}}
EOF
exit 1
`)
	s := &server{cfg: config{opencode: script}}

	text, sessionID, err := s.execChild(context.Background())
	if err == nil {
		t.Fatal("execChild returned nil, want the child's exit error")
	}
	if sessionID != "ses_test" {
		t.Errorf("sessionID = %q, want ses_test", sessionID)
	}
	if !strings.Contains(text, `"product":"P"`) {
		t.Errorf("text = %q, want the completed answer", text)
	}
}

func TestExecChildKeepsOfferJSONLOnNonZeroExit(t *testing.T) {
	script := fakeOpencode(t, `printf '%s\n' '{"product":"P","store":"S","url":"https://example.no/p","price_nok":100}'
exit 1
`)
	s := &server{cfg: config{opencode: script}}

	text, _, err := s.execChild(context.Background())
	if err == nil {
		t.Fatal("execChild returned nil, want the child's exit error")
	}
	if !strings.Contains(text, `"product":"P"`) {
		t.Errorf("text = %q, want the offer payload", text)
	}
}

func TestExecChildDropsOutputWithoutCompletedAnswer(t *testing.T) {
	script := fakeOpencode(t, `printf '%s\n' '{"type":"error","sessionID":"ses_test","error":{"type":"unknown","message":"boom"}}'
exit 1
`)
	s := &server{cfg: config{opencode: script}}

	text, _, err := s.execChild(context.Background())
	if err == nil {
		t.Fatal("execChild returned nil, want the child's exit error")
	}
	if text != "" {
		t.Errorf("text = %q, want no answer", text)
	}
}
