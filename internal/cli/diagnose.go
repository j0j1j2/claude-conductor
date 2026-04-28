package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
)

// SlaveDiagnosis is a snapshot of one slave's observable state. It exists
// to power both the `conductor doctor` subcommand and the one-line hint
// `conductor send` emits on timeout.
type SlaveDiagnosis struct {
	SlaveID     string
	SlaveDir    string
	WindowAlive bool

	HasReady    bool
	ReadyAt     time.Time
	HasPending  bool
	PendingAt   time.Time
	PendingPID  int
	PIDAlive    bool
	HasDone     bool
	DoneAt      time.Time
	HasExit     bool
	ExitCode    string

	HookCommand string // first non-empty Stop-hook command pulled from settings.local.json
	PaneTail    string // last few lines of the slave's tmux pane
}

// Diagnose collects everything we can observe about one slave without
// touching it. Intended for diagnostics; tolerates missing files quietly.
func Diagnose(session, id string) SlaveDiagnosis {
	d := SlaveDiagnosis{
		SlaveID:  id,
		SlaveDir: state.SlaveDir(session, id),
	}
	if alive, err := tmux.PaneDead(session, id); err == nil {
		d.WindowAlive = !alive
	}
	if fi, err := os.Stat(filepath.Join(d.SlaveDir, ".ready")); err == nil {
		d.HasReady = true
		d.ReadyAt = fi.ModTime()
	}
	if fi, err := os.Stat(filepath.Join(d.SlaveDir, ".pending")); err == nil {
		d.HasPending = true
		d.PendingAt = fi.ModTime()
		if b, err := os.ReadFile(filepath.Join(d.SlaveDir, ".pending")); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil {
				d.PendingPID = pid
				if proc, err := os.FindProcess(pid); err == nil {
					d.PIDAlive = proc.Signal(syscall.Signal(0)) == nil
				}
			}
		}
	}
	if fi, err := os.Stat(filepath.Join(d.SlaveDir, ".done")); err == nil {
		d.HasDone = true
		d.DoneAt = fi.ModTime()
	}
	if b, err := os.ReadFile(filepath.Join(d.SlaveDir, ".exit-code")); err == nil {
		d.HasExit = true
		d.ExitCode = strings.TrimSpace(string(b))
	}
	d.HookCommand = readStopHookCommand()
	if pane, err := tmux.CapturePane(session, id, 12); err == nil {
		d.PaneTail = strings.TrimRight(pane, "\n ")
	}
	return d
}

// readStopHookCommand pulls the first Stop-hook command from the
// project-level .claude/settings.local.json, for diagnostics.
func readStopHookCommand() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(cwd, ".claude", "settings.local.json"))
	if err != nil {
		return ""
	}
	// Cheap parse: find the first "_internal_stop_marker" line and emit
	// the surrounding "command" string. Avoids pulling in a JSON model
	// just for diagnostics.
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "_internal_stop_marker") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// Summary returns a single-line hint suitable for stderr after a timeout.
func (d SlaveDiagnosis) Summary() string {
	parts := []string{
		fmt.Sprintf("window=%s", boolWord(d.WindowAlive, "alive", "DEAD")),
		fmt.Sprintf(".ready=%s", boolWord(d.HasReady, "yes", "no")),
		fmt.Sprintf(".pending=%s", pendingWord(d)),
		fmt.Sprintf(".done=%s", boolWord(d.HasDone, "yes", "no")),
	}
	if d.HasExit {
		parts = append(parts, fmt.Sprintf("exit=%s", d.ExitCode))
	}
	return strings.Join(parts, " ")
}

func boolWord(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

func pendingWord(d SlaveDiagnosis) string {
	if !d.HasPending {
		return "no"
	}
	if d.PendingPID > 0 {
		if d.PIDAlive {
			return fmt.Sprintf("pid=%d alive", d.PendingPID)
		}
		return fmt.Sprintf("pid=%d STALE", d.PendingPID)
	}
	return "yes (no pid)"
}

// Full returns a multi-line report for `conductor doctor`.
func (d SlaveDiagnosis) Full() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Slave:        %s\n", d.SlaveID)
	fmt.Fprintf(&b, "State dir:    %s\n", d.SlaveDir)
	fmt.Fprintf(&b, "Window:       %s\n", boolWord(d.WindowAlive, "alive", "DEAD"))
	if d.HasExit {
		fmt.Fprintf(&b, "Exit code:    %s\n", d.ExitCode)
	}
	fmt.Fprintf(&b, "\nMarkers:\n")
	fmt.Fprintf(&b, "  .ready:     %s\n", fileLine(d.HasReady, d.ReadyAt))
	fmt.Fprintf(&b, "  .pending:   %s", fileLine(d.HasPending, d.PendingAt))
	if d.HasPending {
		if d.PendingPID > 0 {
			fmt.Fprintf(&b, " (pid %d %s)\n",
				d.PendingPID, boolWord(d.PIDAlive, "alive", "STALE"))
		} else {
			fmt.Fprintf(&b, " (no pid recorded)\n")
		}
	} else {
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "  .done:      %s\n", fileLine(d.HasDone, d.DoneAt))

	fmt.Fprintf(&b, "\nHook (from .claude/settings.local.json):\n")
	if d.HookCommand == "" {
		fmt.Fprintf(&b, "  (none found)\n")
	} else {
		fmt.Fprintf(&b, "  %s\n", d.HookCommand)
	}

	fmt.Fprintf(&b, "\nPane tail:\n")
	if d.PaneTail == "" {
		fmt.Fprintf(&b, "  (empty / capture failed)\n")
	} else {
		for _, line := range strings.Split(d.PaneTail, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	fmt.Fprintf(&b, "\nDiagnosis hints:\n")
	for _, h := range d.hints() {
		fmt.Fprintf(&b, "  - %s\n", h)
	}
	return b.String()
}

func fileLine(present bool, at time.Time) string {
	if !present {
		return "(none)"
	}
	return at.Format(time.RFC3339)
}

func (d SlaveDiagnosis) hints() []string {
	var hs []string
	if !d.WindowAlive {
		hs = append(hs, "Tmux window is dead. Try `conductor reset "+d.SlaveID+"` or `conductor kill "+d.SlaveID+"`.")
	}
	if !d.HasReady {
		hs = append(hs, "SessionStart hook never fired — slave probably failed to launch claude. Check pane tail above.")
	}
	if d.HasPending && !d.PIDAlive {
		hs = append(hs, "Stale .pending lock detected (PID dead). Next `conductor send` will auto-clear it; or run `conductor unstick "+d.SlaveID+"`.")
	}
	if d.HasPending && d.PIDAlive && d.HasDone {
		hs = append(hs, "Both .pending and .done are present — race; the next `conductor send` will treat the lock as stale.")
	}
	if d.HasPending && !d.HasDone && d.WindowAlive {
		if strings.Contains(strings.ToLower(d.PaneTail), "permission") ||
			strings.Contains(strings.ToLower(d.PaneTail), "y/n") ||
			strings.Contains(strings.ToLower(d.PaneTail), "yes/no") {
			hs = append(hs, "Pane appears to be waiting on an interactive prompt; `--dangerously-skip-permissions` did not cover it.")
		}
	}
	if d.HookCommand == "" {
		hs = append(hs, "No Stop-hook command found in .claude/settings.local.json — `conductor send` will always time out.")
	}
	if len(hs) == 0 {
		hs = append(hs, "No anomalies detected.")
	}
	return hs
}
