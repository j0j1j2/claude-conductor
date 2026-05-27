// Package trust pre-marks the conductor cwd as trusted in ~/.claude.json so
// the master and slave Claude sessions don't block on the "Do you trust the
// files in this folder?" dialog. That dialog is a separate gate from
// --dangerously-skip-permissions, and conductor starts claude via tmux
// send-keys — so a blocking trust prompt strands every slave and master.
package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureCwdTrusted sets projects[projectCwd].hasTrustDialogAccepted = true in
// ~/.claude.json. If the value is already true, the file is not rewritten.
// Existing fields (top-level and within the per-project entry) are preserved.
//
// The write is read-modify-write with an atomic rename in the same directory.
func EnsureCwdTrusted(projectCwd string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	path := filepath.Join(home, ".claude.json")

	var doc map[string]any
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) > 0 {
			if err := json.Unmarshal(b, &doc); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// fall through with nil doc
	default:
		return fmt.Errorf("read %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	projects, ok := doc["projects"].(map[string]any)
	if !ok {
		projects = map[string]any{}
		doc["projects"] = projects
	}
	entry, ok := projects[projectCwd].(map[string]any)
	if !ok {
		entry = map[string]any{}
		projects[projectCwd] = entry
	}
	if v, _ := entry["hasTrustDialogAccepted"].(bool); v {
		return nil
	}
	entry["hasTrustDialogAccepted"] = true

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".claude.json.conductor-*")
	if err != nil {
		return fmt.Errorf("tempfile in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename tempfile to %s: %w", path, err)
	}
	return nil
}
