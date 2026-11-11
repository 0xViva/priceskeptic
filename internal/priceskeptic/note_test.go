package prisjeger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteNoteReplacesUnclosedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	original := "# Prisjeger\n\nkeep this preamble\n\n" + noteStartMarker + "\nold table without an end marker\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeNote(path, goldenRows()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if got := strings.Count(content, noteStartMarker); got != 1 {
		t.Fatalf("start markers = %d, want 1:\n%s", got, content)
	}
	if got := strings.Count(content, noteEndMarker); got != 1 {
		t.Fatalf("end markers = %d, want 1:\n%s", got, content)
	}
	if !strings.HasPrefix(content, "# Prisjeger\n\nkeep this preamble\n\n") {
		t.Fatalf("content before the block was not preserved:\n%s", content)
	}
	if strings.Contains(content, "old table without an end marker") {
		t.Fatalf("the unclosed block's old body survived:\n%s", content)
	}
	if !strings.Contains(content, "PURFLUX AHC736-2") {
		t.Fatalf("the new block is missing its rows:\n%s", content)
	}
	if start, end, ok := findNoteBlock(content); !ok || start != len("# Prisjeger\n\nkeep this preamble\n\n") || end != len(content) {
		t.Fatalf("findNoteBlock = %d/%d/%v, want the block to run to EOF", start, end, ok)
	}
}

func TestFindNoteBlock(t *testing.T) {
	closed := "before\n" + noteStartMarker + "\nbody\n" + noteEndMarker + "\nafter\n"
	start, end, ok := findNoteBlock(closed)
	if !ok || closed[start:end] != noteStartMarker+"\nbody\n"+noteEndMarker {
		t.Fatalf("closed span = %d/%d/%v", start, end, ok)
	}

	if _, _, ok := findNoteBlock("no markers here\n"); ok {
		t.Fatal("findNoteBlock found a block without a start marker")
	}

	open := "before\n" + noteStartMarker + "\nbody\n"
	start, end, ok = findNoteBlock(open)
	if !ok || start != len("before\n") || end != len(open) {
		t.Fatalf("open span = %d/%d/%v, want %d/%d", start, end, ok, len("before\n"), len(open))
	}
}
