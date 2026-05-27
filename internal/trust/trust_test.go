package trust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readClaudeJSON returns the parsed contents of $HOME/.claude.json.
func readClaudeJSON(t *testing.T) map[string]any {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read ~/.claude.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse ~/.claude.json: %v", err)
	}
	return doc
}

func projectEntry(t *testing.T, doc map[string]any, cwd string) map[string]any {
	t.Helper()
	projects, ok := doc["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects key missing or wrong type: %T", doc["projects"])
	}
	entry, ok := projects[cwd].(map[string]any)
	if !ok {
		t.Fatalf("projects[%q] missing or wrong type: %T", cwd, projects[cwd])
	}
	return entry
}

func TestEnsureCwdTrusted_createsFileWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/some/project"

	if err := EnsureCwdTrusted(cwd); err != nil {
		t.Fatalf("EnsureCwdTrusted: %v", err)
	}

	entry := projectEntry(t, readClaudeJSON(t), cwd)
	if v, _ := entry["hasTrustDialogAccepted"].(bool); !v {
		t.Errorf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
}

func TestEnsureCwdTrusted_addsProjectsKeyWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/another/project"

	// Existing file without projects key, but with other top-level fields.
	original := map[string]any{
		"theme":       "dark",
		"numStartups": float64(42),
	}
	b, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatalf("seed ~/.claude.json: %v", err)
	}

	if err := EnsureCwdTrusted(cwd); err != nil {
		t.Fatalf("EnsureCwdTrusted: %v", err)
	}

	doc := readClaudeJSON(t)
	if doc["theme"] != "dark" {
		t.Errorf("theme preserved: got %v", doc["theme"])
	}
	if doc["numStartups"] != float64(42) {
		t.Errorf("numStartups preserved: got %v", doc["numStartups"])
	}
	entry := projectEntry(t, doc, cwd)
	if v, _ := entry["hasTrustDialogAccepted"].(bool); !v {
		t.Errorf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
}

func TestEnsureCwdTrusted_addsCwdEntryAndPreservesSiblings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/new/project"
	sibling := "/old/project"

	original := map[string]any{
		"projects": map[string]any{
			sibling: map[string]any{
				"hasTrustDialogAccepted": true,
				"allowedTools":           []any{"Bash"},
			},
		},
	}
	b, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatalf("seed ~/.claude.json: %v", err)
	}

	if err := EnsureCwdTrusted(cwd); err != nil {
		t.Fatalf("EnsureCwdTrusted: %v", err)
	}

	doc := readClaudeJSON(t)

	// Sibling entry preserved verbatim.
	siblingEntry := projectEntry(t, doc, sibling)
	if v, _ := siblingEntry["hasTrustDialogAccepted"].(bool); !v {
		t.Errorf("sibling hasTrustDialogAccepted got clobbered: %v", siblingEntry["hasTrustDialogAccepted"])
	}
	tools, ok := siblingEntry["allowedTools"].([]any)
	if !ok || len(tools) != 1 || tools[0] != "Bash" {
		t.Errorf("sibling allowedTools got clobbered: %v", siblingEntry["allowedTools"])
	}

	// New entry has trust flag set.
	newEntry := projectEntry(t, doc, cwd)
	if v, _ := newEntry["hasTrustDialogAccepted"].(bool); !v {
		t.Errorf("hasTrustDialogAccepted = %v, want true", newEntry["hasTrustDialogAccepted"])
	}
}

func TestEnsureCwdTrusted_flipsFalseToTrueAndKeepsOtherFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/x/project"

	original := map[string]any{
		"projects": map[string]any{
			cwd: map[string]any{
				"hasTrustDialogAccepted": false,
				"lastCost":               float64(1.23),
				"allowedTools":           []any{"Edit", "Read"},
			},
		},
	}
	b, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatalf("seed ~/.claude.json: %v", err)
	}

	if err := EnsureCwdTrusted(cwd); err != nil {
		t.Fatalf("EnsureCwdTrusted: %v", err)
	}

	entry := projectEntry(t, readClaudeJSON(t), cwd)
	if v, _ := entry["hasTrustDialogAccepted"].(bool); !v {
		t.Errorf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
	if entry["lastCost"] != float64(1.23) {
		t.Errorf("lastCost preserved: got %v", entry["lastCost"])
	}
	tools, ok := entry["allowedTools"].([]any)
	if !ok || len(tools) != 2 || tools[0] != "Edit" || tools[1] != "Read" {
		t.Errorf("allowedTools preserved: got %v", entry["allowedTools"])
	}
}

func TestEnsureCwdTrusted_alreadyTrueIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/y/project"
	path := filepath.Join(home, ".claude.json")

	original := map[string]any{
		"projects": map[string]any{
			cwd: map[string]any{"hasTrustDialogAccepted": true},
		},
	}
	b, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("seed ~/.claude.json: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat seed: %v", err)
	}
	// Backdate the mtime so a rewrite would be detectable.
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := EnsureCwdTrusted(cwd); err != nil {
		t.Fatalf("EnsureCwdTrusted: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().Equal(old) {
		t.Errorf("file was rewritten despite already-trusted state (mtime: before=%v after=%v, seed=%v)",
			old, after.ModTime(), info.ModTime())
	}
}

func TestEnsureCwdTrusted_malformedJSONReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed ~/.claude.json: %v", err)
	}

	if err := EnsureCwdTrusted("/z/project"); err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}

	// File must be left untouched on parse failure.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after error: %v", err)
	}
	if string(b) != "{not json" {
		t.Errorf("file modified after parse error: %q", string(b))
	}
}
