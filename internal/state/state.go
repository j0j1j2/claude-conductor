// Package state manages ~/.conductor directory layout and slave state markers.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// ErrBusy means a live .pending lock blocks a new send.
var ErrBusy = errors.New("state: slave is busy")

// ErrInvalidSlaveID means the slave ID did not pass the safety allow-list.
var ErrInvalidSlaveID = errors.New("state: invalid slave id (must match [a-zA-Z0-9_-]{1,32}, not reserved)")


// slaveIDRe is the allow-list applied to every slave identifier flowing into
// filesystem paths or hook arguments.
var slaveIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

// reservedSlaveIDs are names that have a special role in the layout and may
// not be used as slave IDs (would collide with master state, etc.).
var reservedSlaveIDs = map[string]bool{
	"master":    true,
	"sessions":  true,
	"audit.log": true,
}

// ValidateSlaveID returns nil iff id is safe to use in paths, tmux window
// names, and hook arguments.
func ValidateSlaveID(id string) error {
	if !slaveIDRe.MatchString(id) {
		return ErrInvalidSlaveID
	}
	if reservedSlaveIDs[id] {
		return ErrInvalidSlaveID
	}
	if strings.HasPrefix(id, "-") || strings.HasPrefix(id, "_internal_") {
		return ErrInvalidSlaveID
	}
	return nil
}

// RootDir returns ~/.conductor.
func RootDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".conductor")
}

// SessionDir returns ~/.conductor/sessions/<session>.
func SessionDir(session string) string {
	return filepath.Join(RootDir(), "sessions", session)
}

// SlaveDir returns ~/.conductor/sessions/<session>/<id>. The id is validated
// to prevent path traversal; on invalid input it returns an empty string so
// any subsequent filesystem op fails clearly rather than silently escaping.
func SlaveDir(session, id string) string {
	if ValidateSlaveID(id) != nil {
		return ""
	}
	return filepath.Join(SessionDir(session), id)
}

// SlaveExists reports whether the slave's state dir exists.
func SlaveExists(session, id string) bool {
	dir := SlaveDir(session, id)
	if dir == "" {
		return false
	}
	_, err := os.Stat(dir)
	return err == nil
}

// PendingLock is the structured content of a .pending file. The TurnID lets
// `_internal_stop_marker` tag .done with the same turn the caller asked for,
// so a delayed prior-turn Stop hook cannot impersonate the current turn.
type PendingLock struct {
	PID    int    `json:"pid"`
	TurnID string `json:"turn_id"`
}

// NewTurnID returns a fresh per-send identifier.
func NewTurnID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// CreatePending creates the `.pending` file exclusively, recording PID and a
// fresh TurnID. On contention the existing lock is validated:
//   - The recorded PID is no longer alive → stale, take over.
//   - Otherwise the lock is live → ErrBusy.
//
// Returns the newly written turn ID on success.
func CreatePending(slaveDir string) (string, error) {
	turnID := NewTurnID()
	if err := writePending(slaveDir, PendingLock{PID: os.Getpid(), TurnID: turnID}); err == nil {
		return turnID, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", err
	}

	if !isPendingStale(slaveDir) {
		return "", ErrBusy
	}

	if err := writePendingForce(slaveDir, PendingLock{PID: os.Getpid(), TurnID: turnID}); err != nil {
		return "", err
	}
	return turnID, nil
}

// writePending is the exclusive-create path used on a fresh lock. The file
// is written to a tmp path first and renamed in so concurrent ReadPending
// callers never observe a partially-written file.
func writePending(slaveDir string, lock PendingLock) error {
	pendingPath := filepath.Join(slaveDir, ".pending")
	// Probe for existence first; if a file is already there, surface
	// os.ErrExist so CreatePending's caller can route into staleness.
	if _, err := os.Stat(pendingPath); err == nil {
		return os.ErrExist
	}
	tmp := filepath.Join(slaveDir, ".pending.tmp")
	b, err := json.Marshal(lock)
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	// Use Link+Remove (so a concurrent writer racing us gets EEXIST on Link)
	// to keep the create exclusive.
	if err := os.Link(tmp, pendingPath); err != nil {
		_ = os.Remove(tmp)
		if os.IsExist(err) {
			return os.ErrExist
		}
		return err
	}
	_ = os.Remove(tmp)
	return nil
}

// writePendingForce is the atomic-rename path used during stale takeover.
func writePendingForce(slaveDir string, lock PendingLock) error {
	tmp := filepath.Join(slaveDir, ".pending.tmp")
	final := filepath.Join(slaveDir, ".pending")
	b, err := json.Marshal(lock)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadPending returns the active lock or os.ErrNotExist if no lock is held.
func ReadPending(slaveDir string) (PendingLock, error) {
	b, err := os.ReadFile(filepath.Join(slaveDir, ".pending"))
	if err != nil {
		return PendingLock{}, err
	}
	// Tolerate the legacy plain-PID format for graceful upgrade.
	trimmed := strings.TrimSpace(string(b))
	if pid, atoiErr := strconv.Atoi(trimmed); atoiErr == nil {
		return PendingLock{PID: pid}, nil
	}
	var lock PendingLock
	if err := json.Unmarshal(b, &lock); err != nil {
		return PendingLock{}, err
	}
	return lock, nil
}

// IsBusy reports whether the slave currently holds a live .pending lock.
func IsBusy(slaveDir string) bool {
	if _, err := os.Stat(filepath.Join(slaveDir, ".pending")); err != nil {
		return false
	}
	return !isPendingStale(slaveDir)
}

// isPendingStale reports whether an existing .pending lock is no longer
// meaningful (PID gone). A sibling .done is NOT sufficient evidence on its
// own anymore — the .done could be from the SAME live turn that simply
// completed. The owning send will clean up its own lock; if it didn't (it
// crashed) we'll see PID-dead and clear the lock then.
func isPendingStale(slaveDir string) bool {
	lock, err := ReadPending(slaveDir)
	if err != nil {
		return true // unreadable / unparseable lock → safe to replace
	}
	if lock.PID <= 0 {
		return true
	}
	proc, err := os.FindProcess(lock.PID)
	if err != nil {
		return true
	}
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

// Done is the on-disk shape of a .done file: the response text the slave
// produced for a specific turn.
type Done struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
}

// WriteDone atomically writes a turn's completion to `.done`. The tmp file
// is removed on rename failure so it does not linger.
func WriteDone(slaveDir, turnID, content string) error {
	tmp := filepath.Join(slaveDir, ".done.tmp")
	final := filepath.Join(slaveDir, ".done")
	b, err := json.Marshal(Done{TurnID: turnID, Text: content})
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename .done.tmp -> .done: %w", err)
	}
	return nil
}

// ReadDone reads a `.done` file. It REQUIRES the JSON format produced by
// WriteDone — a missing or empty TurnID is treated as a turn-mismatch and
// callers should discard it. (Legacy plain-text fallback has been removed
// because it was being exploited to deliver prior-turn content to new sends.)
func ReadDone(slaveDir string) (Done, error) {
	b, err := os.ReadFile(filepath.Join(slaveDir, ".done"))
	if err != nil {
		return Done{}, err
	}
	var d Done
	if jerr := json.Unmarshal(b, &d); jerr != nil {
		// Malformed .done; treat as empty TurnID so caller discards it.
		return Done{Text: string(b)}, nil
	}
	return d, nil
}

// WriteDoneError writes a sentinel .done indicating a hook-side failure, so
// `conductor send` returns promptly with a useful message instead of timing
// out. The turn ID is included if known.
func WriteDoneError(slaveDir, turnID, errMsg string) error {
	return WriteDone(slaveDir, turnID, "[conductor hook error] "+errMsg)
}
