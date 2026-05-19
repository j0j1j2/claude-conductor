package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"

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

	var backupContent []byte
	var backupPath string
	alreadyConductor := false
	if existing, err := os.ReadFile(target); err == nil {
		if looksLikeConductor(existing) {
			alreadyConductor = true
		} else {
			backupPath = nextBackupPath(target)
			backupContent = existing
			if werr := os.WriteFile(backupPath, existing, 0o644); werr != nil {
				return func() {}, werr
			}
		}
	}

	if err := atomicWriteFile(target, []byte(hooks.RenderProjectSettings(conductorBin)), 0o644); err != nil {
		if backupPath != "" {
			_ = os.Remove(backupPath)
		}
		return func() {}, err
	}

	// If the file was ALREADY conductor-owned when we got here, we did not
	// displace anything user-visible. Return a no-op restore so a later
	// rollback can't delete the conductor file we share with a peer process.
	if alreadyConductor {
		return func() {}, nil
	}

	// Capture backup content into the closure (path + content) so a stale
	// backup file does not affect correctness; the restore is self-contained.
	restore = func() {
		_ = os.Remove(target)
		if backupContent != nil {
			_ = os.WriteFile(target, backupContent, 0o644)
		}
		if backupPath != "" {
			_ = os.Remove(backupPath)
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
	return strconv.Itoa(n)
}

// atomicWriteFile writes content via a sibling tmp file + rename so concurrent
// readers (e.g., the slave's claude reading settings.local.json at startup)
// never observe a partial write.
func atomicWriteFile(path string, content []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
