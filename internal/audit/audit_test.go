package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppend_writesNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	if err := Append(path, Entry{
		Timestamp:  time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC),
		Cmd:        "send",
		Args:       []string{"s1", "hello"},
		DurationMS: 123,
		Exit:       0,
	}); err != nil {
		t.Fatal(err)
	}

	if err := Append(path, Entry{
		Timestamp:  time.Date(2026, 4, 19, 10, 0, 1, 0, time.UTC),
		Cmd:        "list",
		Args:       []string{},
		DurationMS: 5,
		Exit:       0,
	}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	var got Entry
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Cmd != "send" {
		t.Errorf("got cmd %q", got.Cmd)
	}
}
