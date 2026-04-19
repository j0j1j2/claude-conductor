// Package state manages ~/.conductor directory layout and slave state markers.
package state

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrBusy means a .pending file already exists for the slave.
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

// CreatePending creates the `.pending` file exclusively. Returns ErrBusy if
// the file already exists.
func CreatePending(slaveDir string) error {
	f, err := os.OpenFile(filepath.Join(slaveDir, ".pending"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return ErrBusy
		}
		return err
	}
	return f.Close()
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
