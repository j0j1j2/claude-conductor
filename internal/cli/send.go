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
		prompt := args[1]

		if !state.SlaveExists(sess, id) {
			return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
		}
		slaveDir := state.SlaveDir(sess, id)

		if err := state.CreatePending(slaveDir); err != nil {
			if errors.Is(err, state.ErrBusy) {
				return CLIError(exitcode.Busy, "slave %s is busy", id)
			}
			return err
		}
		if err := state.RemoveDone(slaveDir); err != nil {
			return err
		}

		w, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer w.Close()
		if err := w.Add(slaveDir); err != nil {
			return err
		}

		// Type the prompt as literal keystrokes into the slave's TUI.
		// paste-buffer does not reliably land in Claude Code's Ink-based
		// input widget; `send-keys -l` simulates actual typing instead.
		if err := tmux.SendLiteralCmd(sess, id, prompt).Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux send-keys -l: %v", err)
		}
		// Brief pause so the TUI processes the input buffer before submit.
		time.Sleep(100 * time.Millisecond)
		if err := tmux.SendKeysCmd(sess, id, "Enter").Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux send-keys Enter: %v", err)
		}

		donePath := filepath.Join(slaveDir, ".done")
		if _, err := os.Stat(donePath); err == nil {
			return finishSend(slaveDir)
		}

		deadline := time.After(time.Duration(sendTimeout) * time.Second)
		liveness := time.NewTicker(1 * time.Second)
		defer liveness.Stop()

		for {
			select {
			case ev := <-w.Events:
				if (ev.Op&fsnotify.Create == fsnotify.Create || ev.Op&fsnotify.Write == fsnotify.Write) &&
					filepath.Base(ev.Name) == ".done" {
					return finishSend(slaveDir)
				}
			case err := <-w.Errors:
				return err
			case <-liveness.C:
				// Belt-and-suspenders poll: fsnotify can drop or coalesce
				// events under load, leaving us blocked even though the
				// slave already wrote .done.
				if _, err := os.Stat(donePath); err == nil {
					return finishSend(slaveDir)
				}
				dead, _ := tmux.PaneDead(sess, id)
				if dead {
					_ = state.RemovePending(slaveDir)
					return CLIError(exitcode.Crash, "slave %s window is dead", id)
				}
			case <-deadline:
				_ = state.RemovePending(slaveDir)
				diag := Diagnose(sess, id).Summary()
				return CLIError(exitcode.Timeout,
					"slave %s did not complete within %ds\n  diagnostics: %s\n  hint: `conductor doctor %s` for full report; `conductor interrupt %s` to cancel",
					id, sendTimeout, diag, id, id)
			}
		}
	},
}

func finishSend(slaveDir string) error {
	content, err := state.ReadDone(slaveDir)
	if err != nil {
		return err
	}
	_ = state.RemovePending(slaveDir)
	fmt.Print(content)
	return nil
}

func init() {
	sendCmd.Flags().IntVar(&sendTimeout, "timeout", 600, "seconds to wait for slave turn completion")
	sendCmd.Flags().BoolVar(&sendQuiet, "quiet", false, "suppress stderr progress")
	Root.AddCommand(sendCmd)
}
