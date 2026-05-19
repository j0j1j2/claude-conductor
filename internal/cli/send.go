package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

		if !state.SlaveExists(sess, id) {
			return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
		}
		slaveDir := state.SlaveDir(sess, id)

		// Drain phase: cancel any in-flight prior turn and wait briefly for
		// the previous Stop hook (if any) to land. This narrows the window
		// in which the *current* turn's Stop hook could attribute a prior
		// turn's response to our new turn id.
		_ = tmux.SendKeysCmd(sess, id, "Escape").Run()
		time.Sleep(300 * time.Millisecond)
		_ = state.RemoveDone(slaveDir)

		// Acquire a turn-id'd lock so this send can later distinguish its own
		// .done from any straggling Stop-hook completion of a prior turn.
		turnID, err := state.CreatePending(slaveDir)
		if err != nil {
			if errors.Is(err, state.ErrBusy) {
				return CLIError(exitcode.Busy, "slave %s is busy", id)
			}
			return CLIError(exitcode.InternalError, "create pending lock: %v", err)
		}
		// Belt-and-suspenders: if we exit before finishSend (timeout, crash,
		// pane death, validation), release the lock so the slave isn't stuck.
		sendDone := false
		defer func() {
			if !sendDone {
				_ = state.RemovePending(slaveDir)
			}
		}()

		if err := state.RemoveDone(slaveDir); err != nil {
			return CLIError(exitcode.InternalError, "clear stale .done: %v", err)
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
		if ok, ferr := tryFinish(slaveDir, turnID); ferr != nil {
			return ferr
		} else if ok {
			sendDone = true
			return nil
		}

		deadline := time.After(time.Duration(sendTimeout) * time.Second)
		liveness := time.NewTicker(1 * time.Second)
		defer liveness.Stop()

		// Track recent mismatched-turn events so we don't burn CPU if a
		// misbehaving hook keeps rewriting the same wrong .done.
		mismatches := 0
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify channel closed")
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 &&
					filepath.Base(ev.Name) == ".done" {
					okFin, ferr, matched := tryFinishV(slaveDir, turnID)
					if ferr != nil {
						return ferr
					}
					if okFin {
						sendDone = true
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
				// Slave's state dir vanished beneath us (kill / reset midway).
				if ev.Op&fsnotify.Remove != 0 && ev.Name == slaveDir {
					return CLIError(exitcode.Crash, "slave %s state dir vanished (killed?)", id)
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
					if okFin, ferr := tryFinish(slaveDir, turnID); ferr != nil {
						return ferr
					} else if okFin {
						sendDone = true
						return nil
					}
				}
				dead, perr := tmux.PaneDead(sess, id)
				if dead || (perr != nil && tmux.WindowGone(perr)) {
					return CLIError(exitcode.Crash, "slave %s window is dead", id)
				}
			case <-deadline:
				// Final-chance Stat: a .done arriving in the same scheduling
				// quantum as the deadline must not be lost to select's random
				// fairness.
				if _, err := os.Stat(donePath); err == nil {
					if okFin, ferr := tryFinish(slaveDir, turnID); ferr != nil {
						return ferr
					} else if okFin {
						sendDone = true
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

// tryFinish reads .done; if its turn id matches turnID (or is empty == legacy),
// it consumes the .done and returns ok=true. If .done belongs to a DIFFERENT
// turn (a prior-turn Stop hook racing us), it discards the file and keeps
// waiting. Returns (false, nil) when .done does not yet exist.
func tryFinish(slaveDir, turnID string) (bool, error) {
	ok, err, _ := tryFinishV(slaveDir, turnID)
	return ok, err
}

// tryFinishV is tryFinish with an extra return indicating whether a .done
// existed at all (matched and consumed, mismatched and discarded, or absent).
func tryFinishV(slaveDir, turnID string) (ok bool, err error, mismatched bool) {
	d, rerr := state.ReadDone(slaveDir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return false, nil, false
		}
		return false, CLIError(exitcode.InternalError, "read .done: %v", rerr), false
	}
	if d.TurnID != "" && d.TurnID != turnID {
		_ = state.RemoveDone(slaveDir)
		return false, nil, true
	}
	_ = state.RemovePending(slaveDir)
	fmt.Print(d.Text)
	return true, nil, false
}

func init() {
	sendCmd.Flags().IntVar(&sendTimeout, "timeout", 600, "seconds to wait for slave turn completion")
	sendCmd.Flags().BoolVar(&sendQuiet, "quiet", false, "suppress stderr diagnostics on timeout")
	Root.AddCommand(sendCmd)
}
