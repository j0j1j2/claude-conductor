package cli

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/j0j1j2/claude-conductor/internal/hooks"
)

// installProjectHooks writes the conductor-compatible hook settings to
// `<projectCwd>/.claude/settings.local.json`. If a file already exists there
// and was not written by conductor, it is moved aside to
// `.conductor-backup` on first install so the user can restore it later.
// On subsequent calls, the file is overwritten idempotently.
func installProjectHooks(projectCwd, conductorBin string) error {
	claudeDir := filepath.Join(projectCwd, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(claudeDir, "settings.local.json")
	backup := target + ".conductor-backup"

	if existing, err := os.ReadFile(target); err == nil {
		if !looksLikeConductor(existing) {
			if _, err := os.Stat(backup); os.IsNotExist(err) {
				if err := os.WriteFile(backup, existing, 0o644); err != nil {
					return err
				}
			}
		}
	}
	return os.WriteFile(target, []byte(hooks.RenderProjectSettings(conductorBin)), 0o644)
}

func looksLikeConductor(b []byte) bool {
	return bytes.Contains(b, []byte(`$CONDUCTOR_SLAVE_ID`)) &&
		bytes.Contains(b, []byte(`_internal_stop_marker`))
}
