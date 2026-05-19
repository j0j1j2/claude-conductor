package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var (
	sendTimeout int
	sendQuiet   bool
)

// hookErrSentinel marks .done content that was written by the Stop-hook's
// error path rather than by a real assistant turn. `send` surfaces this as
// a non-zero exit so scripts and the master Claude can tell hook failure
// from a successful (possibly empty) response.
const hookErrSentinel = "[conductor hook error]"

var sendCmd = &cobra.Command{
	Use:   "send <slave-id> <prompt>",
	Short: "Send a prompt to a slave and block until its turn ends",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.InTmux() {
			return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
		}
		sess, err := tmux.CurrentSession()
		if err != nil {
			return CLIError(exitcode.InternalError, "detect tmux session: %v", err)
		}

		id := args[0]
		if err := state.ValidateSlaveID(id); err != nil {
			return CLIError(exitcode.UnknownSlave, "invalid slave id %q: %v", id, err)
		}
		prompt := args[1]
		if sendTimeout < 1 {
			sendTimeout = 1
		}
		// Refuse prompts that would let an attacker (or a misbehaving master)
		// hijack the slave's TUI via embedded ANSI / tmux escape sequences.
		if bad := findUnsafeControlByte(prompt); bad != "" {
			return CLIError(exitcode.UnknownSlave,
				"prompt contains disallowed control byte %s; strip ANSI/escape sequences", bad)
		}
		if strings.TrimSpace(prompt) == "" {
			return CLIError(exitcode.UnknownSlave, "prompt is empty")
		}

		if !state.SlaveExists(sess, id) {
			return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
		}
		slaveDir := state.SlaveDir(sess, id)

		// Acquire a turn-id'd lock in DRAINING state. Until we finalize the
		// watermark below, the Stop hook refuses to write .done. This guards
		// the race where the Escape we are about to send causes claude to
		// commit the cancelled prior turn's text to the transcript — that
		// content would otherwise look like a legitimate post-watermark
		// response and be tagged with our turn id.
		turnID, err := state.CreatePending(slaveDir)
		if err != nil {
			if errors.Is(err, state.ErrBusy) {
				return CLIError(exitcode.Busy, "slave %s is busy", id)
			}
			return CLIError(exitcode.InternalError, "create pending lock: %v", err)
		}
		sendDone := false
		defer func() {
			if !sendDone {
				_ = state.RemovePending(slaveDir)
			}
		}()

		// Drain phase (under the lock + Draining flag): interrupt any
		// in-flight prior turn. Any Stop hook firing now sees Draining=true
		// and refuses to deliver. Only the winner of contention pays this
		// cancellation cost.
		_ = tmux.SendKeysCmd(sess, id, "Escape").Run()
		time.Sleep(300 * time.Millisecond)
		if err := state.RemoveDone(slaveDir); err != nil {
			return CLIError(exitcode.InternalError, "clear stale .done: %v", err)
		}

		// NOW stat the transcript: its size at this moment is the watermark.
		// Lift Draining atomically by rewriting .pending with the watermark.
		transcriptPath, transcriptOffset := readTranscriptWatermark(slaveDir)
		if err := state.FinalizePendingWatermark(slaveDir, turnID, transcriptPath, transcriptOffset); err != nil {
			return CLIError(exitcode.InternalError, "finalize watermark: %v", err)
		}

		w, err := fsnotify.NewWatcher()
		if err != nil {
			return CLIError(exitcode.InternalError, "fsnotify: %v", err)
		}
		defer w.Close()
		if err := w.Add(slaveDir); err != nil {
			return CLIError(exitcode.InternalError, "fsnotify watch: %v", err)
		}

		if err := tmux.SendLiteralCmd(sess, id, prompt).Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux send-keys -l: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		if err := tmux.SendKeysCmd(sess, id, "Enter").Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux send-keys Enter: %v", err)
		}

		donePath := filepath.Join(slaveDir, ".done")
		if exit, ferr := tryFinish(slaveDir, turnID); ferr != nil {
			return ferr
		} else if exit >= 0 {
			sendDone = true
			if exit != 0 {
				return CLIError(exit, "slave %s reported a hook error", id)
			}
			return nil
		}

		deadline := time.After(time.Duration(sendTimeout) * time.Second)
		liveness := time.NewTicker(1 * time.Second)
		defer liveness.Stop()

		mismatches := 0
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify channel closed")
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 &&
					filepath.Base(ev.Name) == ".done" {
					exit, ferr, matched := tryFinishV(slaveDir, turnID)
					if ferr != nil {
						return ferr
					}
					if exit >= 0 {
						sendDone = true
						if exit != 0 {
							return CLIError(exit, "slave %s reported a hook error", id)
						}
						return nil
					}
					if !matched {
						mismatches++
						if mismatches > 50 {
							return CLIError(exitcode.Crash,
								"slave %s keeps emitting prior-turn responses (%d mismatches); try `conductor reset %s`",
								id, mismatches, id)
						}
					}
				}
			case werr, ok := <-w.Errors:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify error channel closed")
				}
				if _, sErr := os.Stat(slaveDir); os.IsNotExist(sErr) {
					return CLIError(exitcode.Crash, "slave %s vanished mid-send (killed?)", id)
				}
				return CLIError(exitcode.InternalError, "fsnotify: %v", werr)
			case <-liveness.C:
				if _, err := os.Stat(donePath); err == nil {
					if exit, ferr := tryFinish(slaveDir, turnID); ferr != nil {
						return ferr
					} else if exit >= 0 {
						sendDone = true
						if exit != 0 {
							return CLIError(exit, "slave %s reported a hook error", id)
						}
						return nil
					}
				}
				dead, perr := tmux.PaneDead(sess, id)
				if dead || (perr != nil && tmux.WindowGone(perr)) {
					return CLIError(exitcode.Crash, "slave %s window is dead", id)
				}
			case <-deadline:
				if _, err := os.Stat(donePath); err == nil {
					if exit, ferr := tryFinish(slaveDir, turnID); ferr != nil {
						return ferr
					} else if exit >= 0 {
						sendDone = true
						if exit != 0 {
							return CLIError(exit, "slave %s reported a hook error", id)
						}
						return nil
					}
				}
				diag := Diagnose(sess, id).Summary()
				if !sendQuiet {
					fmt.Fprintf(os.Stderr, "diagnostics: %s\n", diag)
				}
				return CLIError(exitcode.Timeout,
					"slave %s did not complete within %ds (hint: `conductor doctor %s`, `conductor interrupt %s`)",
					id, sendTimeout, id, id)
			}
		}
	},
}

// tryFinish wraps tryFinishV for callers that don't care whether a .done was
// mismatched-and-discarded vs. simply absent. Return value: -1 = no .done to
// consume yet; 0 = success; >0 = exit code from a hook-error sentinel.
func tryFinish(slaveDir, turnID string) (int, error) {
	exit, err, _ := tryFinishV(slaveDir, turnID)
	return exit, err
}

// tryFinishV consumes a .done if its TurnID strictly matches turnID. Empty
// or different TurnIDs are discarded (mismatched=true). A consumed .done
// whose Text begins with the hook-error sentinel is reported as a non-zero
// exit code (so scripts can detect hook failure).
func tryFinishV(slaveDir, turnID string) (exit int, err error, mismatched bool) {
	d, rerr := state.ReadDone(slaveDir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return -1, nil, false
		}
		return -1, CLIError(exitcode.InternalError, "read .done: %v", rerr), false
	}
	if d.TurnID != turnID {
		// Strict match: empty TurnID (legacy/malformed) and prior-turn IDs
		// are both treated as mismatch. Drop and keep waiting.
		_ = state.RemoveDone(slaveDir)
		return -1, nil, true
	}
	_ = state.RemovePending(slaveDir)
	text := d.Text
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	fmt.Print(text)
	if strings.HasPrefix(d.Text, hookErrSentinel) {
		return exitcode.Crash, nil, false
	}
	return 0, nil, false
}

// readTranscriptWatermark resolves the slave's current transcript path and
// stats it for a size, to use as a "drop everything before this offset"
// barrier for the Stop hook. Both values may be empty/zero when no transcript
// path has been recorded yet (e.g. very first send, or right after reset),
// OR when the path is recorded but unreadable (we fail closed — return
// empty path so the hook gate is explicitly disabled rather than silently
// neutered by a path-set + offset=0).
func readTranscriptWatermark(slaveDir string) (string, int64) {
	path := state.ReadTranscriptPath(slaveDir)
	if path == "" {
		return "", 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		// Disable watermarking explicitly rather than recording a 0 offset
		// that every subsequent hook would trivially pass.
		return "", 0
	}
	return path, fi.Size()
}

// findUnsafeControlByte returns a human description of the first disallowed
// control character in s, or "" if the prompt is safe. Newline/tab/CR are
// allowed; everything else < 0x20 plus DEL (0x7f) and ESC (already covered
// by <0x20) is rejected.
func findUnsafeControlByte(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\t' || c == '\r' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return fmt.Sprintf("0x%02x at offset %d", c, i)
		}
	}
	return ""
}

func init() {
	sendCmd.Flags().IntVar(&sendTimeout, "timeout", 600, "seconds to wait for slave turn completion")
	sendCmd.Flags().BoolVar(&sendQuiet, "quiet", false, "suppress stderr diagnostics on timeout")
	Root.AddCommand(sendCmd)
}
