package cli

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/j0j1j2/claude-conductor/internal/hooks"
)

// installProjectHooks writes the conductor-compatible hook settings to
// `<projectCwd>/.claude/settings.local.json`. If a file already exists there
// and was not written by conductor, it is moved aside to a uniquely-named
// `.conductor-backup-<n>` so subsequent installs never clobber an earlier
// backup. Detection is keyed on a sentinel marker embedded in the rendered
// JSON — not a substring match — so a user who copies our hook command body
// into their own settings will not be mis-identified as a conductor file.
//
// Returns a Restore closure that, if invoked, deletes the conductor file we
// wrote and restores the latest backup (if any). Callers that fail later in
// the bootstrap sequence should invoke it to leave the user's project clean.
func installProjectHooks(projectCwd, conductorBin string) (restore func(), err error) {
	claudeDir := filepath.Join(projectCwd, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return func() {}, err
	}
	target := filepath.Join(claudeDir, "settings.local.json")

	var backupPath string
	if existing, err := os.ReadFile(target); err == nil {
		if !looksLikeConductor(existing) {
			backupPath = nextBackupPath(target)
			if werr := os.WriteFile(backupPath, existing, 0o644); werr != nil {
				return func() {}, werr
			}
		}
	}

	if err := os.WriteFile(target, []byte(hooks.RenderProjectSettings(conductorBin)), 0o644); err != nil {
		// Roll back the backup we just made (if any) so we leave no trace.
		if backupPath != "" {
			_ = os.Remove(backupPath)
		}
		return func() {}, err
	}

	restore = func() {
		_ = os.Remove(target)
		if backupPath != "" {
			if b, rerr := os.ReadFile(backupPath); rerr == nil {
				_ = os.WriteFile(target, b, 0o644)
				_ = os.Remove(backupPath)
			}
		}
	}
	return restore, nil
}

func looksLikeConductor(b []byte) bool {
	return bytes.Contains(b, []byte(hooks.ConductorMarker))
}

// nextBackupPath finds an unused `<target>.conductor-backup-N` slot so we
// never overwrite an earlier user file.
func nextBackupPath(target string) string {
	for i := 0; i < 10000; i++ {
		var p string
		if i == 0 {
			p = target + ".conductor-backup"
		} else {
			p = target + ".conductor-backup-" + itoa(i)
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
	// Fallback: append .latest; will overwrite, but only after 10k tries.
	return target + ".conductor-backup.latest"
}

func itoa(n int) string {
	// tiny stdlib-free int->string to keep this file dependency-light
	if n == 0 {
		return "0"
	}
	buf := [12]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
