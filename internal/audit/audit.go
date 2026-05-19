//go:build unix

// Package audit appends NDJSON audit entries to a log file.
package audit

import (
	"encoding/json"
	"os"
	"syscall"
	"time"
)

// Entry is one audited conductor invocation.
type Entry struct {
	Timestamp  time.Time `json:"ts"`
	Cmd        string    `json:"cmd"`
	Args       []string  `json:"args"`
	DurationMS int64     `json:"duration_ms"`
	Exit       int       `json:"exit"`
}

// Append writes one NDJSON line to path, creating the file if needed.
// Uses an exclusive advisory lock around the write so two concurrent
// conductor processes never interleave bytes within a line.
func Append(path string, e Entry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = f.Write(b)
	return err
}
