package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "testdata", name)
}

func TestLastAssistantText_basic(t *testing.T) {
	got, err := LastAssistantText(fixturePath(t, "transcript_basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "The answer is 4." {
		t.Errorf("got %q, want %q", got, "The answer is 4.")
	}
}

func TestLastAssistantText_toolsOnly(t *testing.T) {
	_, err := LastAssistantText(fixturePath(t, "transcript_tools_only.jsonl"))
	if err != ErrNoAssistantText {
		t.Errorf("err = %v, want ErrNoAssistantText", err)
	}
}

func TestLastAssistantText_empty(t *testing.T) {
	_, err := LastAssistantText(fixturePath(t, "transcript_empty.jsonl"))
	if err != ErrNoAssistantText {
		t.Errorf("err = %v, want ErrNoAssistantText", err)
	}
}

func TestLastAssistantText_missingFile(t *testing.T) {
	_, err := LastAssistantText("/nonexistent/path.jsonl")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
