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

func TestTokensUsed_sumsEveryAssistantTurn(t *testing.T) {
	u, err := TokensUsed(fixturePath(t, "transcript_usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Turns != 3 {
		t.Errorf("Turns = %d, want 3", u.Turns)
	}
	if u.InputTokens != 17 || u.OutputTokens != 43 {
		t.Errorf("input/output = %d/%d, want 17/43", u.InputTokens, u.OutputTokens)
	}
	if u.CacheCreationTokens != 100 || u.CacheReadTokens != 5000 {
		t.Errorf("cache creation/read = %d/%d, want 100/5000", u.CacheCreationTokens, u.CacheReadTokens)
	}
	// Cache reads dominate: leaving them out would report 60 instead of 5160.
	if u.Total() != 5160 {
		t.Errorf("Total = %d, want 5160", u.Total())
	}
}

func TestTokensUsed_idleSlaveIsZeroNotError(t *testing.T) {
	u, err := TokensUsed(fixturePath(t, "transcript_tools_only.jsonl"))
	if err != nil {
		t.Fatalf("idle slave should not error: %v", err)
	}
	if u.Total() != 0 || u.Turns != 0 {
		t.Errorf("idle slave usage = %+v, want zero", u)
	}
}

func TestTokensUsed_missingFile(t *testing.T) {
	if _, err := TokensUsed(fixturePath(t, "does_not_exist.jsonl")); err == nil {
		t.Error("expected error for missing transcript")
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
