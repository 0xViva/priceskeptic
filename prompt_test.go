package main

import (
	"strings"
	"testing"
)

func TestSearchPromptUsesPostalCode(t *testing.T) {
	prompt := searchPrompt("widget", "2026-01-01", "6773")
	if !strings.Contains(prompt, "6773") {
		t.Fatalf("search prompt does not mention the postal code:\n%s", prompt)
	}
	if !strings.Contains(prompt, "home delivery") {
		t.Fatal("search prompt does not ask for home delivery")
	}
}

func TestFollowUpPromptUsesPostalCode(t *testing.T) {
	prompt := followUpPrompt("widget", "", "6773")
	if !strings.Contains(prompt, "6773") {
		t.Fatalf("follow-up prompt does not mention the postal code:\n%s", prompt)
	}
}
