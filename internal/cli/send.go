package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
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

		promptFile, err := os.CreateTemp("", "conductor-prompt-*.txt")
		if err != nil {
			return err
		}
		defer os.Remove(promptFile.Name())
		if _, err := promptFile.WriteString(prompt); err != nil {
			return err
		}
		promptFile.Close()

		if err := tmux.LoadBufferCmd(promptFile.Name()).Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux load-buffer: %v", err)
		}
		if err := tmux.PasteBufferCmd(sess, id).Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux paste-buffer: %v", err)
		}
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
				dead, _ := tmux.PaneDead(sess, id)
				if dead {
					_ = state.RemovePending(slaveDir)
					return CLIError(exitcode.Crash, "slave %s window is dead", id)
				}
			case <-deadline:
				return CLIError(exitcode.Timeout, "slave %s did not complete within %ds (may still be working; use `conductor interrupt %s`)", id, sendTimeout, id)
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
