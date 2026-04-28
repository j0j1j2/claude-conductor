// Package state manages ~/.conductor directory layout and slave state markers.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrBusy means a live .pending lock blocks a new send.
var ErrBusy = errors.New("state: slave is busy")

// RootDir returns ~/.conductor.
func RootDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".conductor")
}

// SessionDir returns ~/.conductor/sessions/<session>.
func SessionDir(session string) string {
	return filepath.Join(RootDir(), "sessions", session)
}

// SlaveDir returns ~/.conductor/sessions/<session>/<id>.
func SlaveDir(session, id string) string {
	return filepath.Join(SessionDir(session), id)
}

// SlaveExists reports whether the slave's state dir exists.
func SlaveExists(session, id string) bool {
	_, err := os.Stat(SlaveDir(session, id))
	return err == nil
}

// CreatePending creates the `.pending` file exclusively, recording the
// current process's PID inside it. If the file already exists, the lock is
// validated:
//   - A `.done` sitting next to it means the slave already finished — the
//     lock is stale (probably a previous send that was interrupted before
//     it could clean up). Replace and continue.
//   - The recorded PID is no longer alive — the lock is stale (the previous
//     conductor process crashed or was killed). Replace and continue.
//   - Otherwise the lock is live → ErrBusy.
func CreatePending(slaveDir string) error {
	pendingPath := filepath.Join(slaveDir, ".pending")
	if err := writePending(pendingPath); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return err
	}

	if !isPendingStale(slaveDir) {
		return ErrBusy
	}
	// Stale lock: replace it.
	_ = os.Remove(pendingPath)
	return writePending(pendingPath)
}

func writePending(pendingPath string) error {
	f, err := os.OpenFile(pendingPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return f.Close()
}

// isPendingStale reports whether an existing .pending lock is no longer
// meaningful (slave already finished, or the holder process is gone).
func isPendingStale(slaveDir string) bool {
	if _, err := os.Stat(filepath.Join(slaveDir, ".done")); err == nil {
		return true
	}
	b, err := os.ReadFile(filepath.Join(slaveDir, ".pending"))
	if err != nil {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	// Signal 0 probes for process existence without delivering anything.
	return proc.Signal(syscall.Signal(0)) != nil
}

// RemovePending removes the `.pending` file. No error if it does not exist.
func RemovePending(slaveDir string) error {
	err := os.Remove(filepath.Join(slaveDir, ".pending"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveDone removes the stale `.done` file.
func RemoveDone(slaveDir string) error {
	err := os.Remove(filepath.Join(slaveDir, ".done"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// WriteDone atomically writes the completion message to `.done`.
func WriteDone(slaveDir, content string) error {
	tmp := filepath.Join(slaveDir, ".done.tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(slaveDir, ".done"))
}

// ReadDone reads the `.done` file.
func ReadDone(slaveDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(slaveDir, ".done"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
