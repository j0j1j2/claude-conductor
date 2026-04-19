// Package audit appends NDJSON audit entries to a log file.
package audit

import (
	"encoding/json"
	"os"
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
func Append(path string, e Entry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = f.Write(b)
	return err
}
