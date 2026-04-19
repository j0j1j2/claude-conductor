package cmd

import (
	"os"
	"path/filepath"
	"time"

	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
	Use:   "reset <slave-id>",
	Short: "Restart the slave's claude process in its existing window",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.InTmux() {
			return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
		}
		sess, err := tmux.CurrentSession()
		if err != nil {
			return CLIError(exitcode.InternalError, "%v", err)
		}
		id := args[0]
		if !state.SlaveExists(sess, id) {
			return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
		}
		slaveDir := state.SlaveDir(sess, id)

		for _, f := range []string{".pending", ".done", ".ready", ".exit-code"} {
			_ = os.Remove(filepath.Join(slaveDir, f))
		}

		if err := tmux.SendKeysCmd(sess, id, "C-c").Run(); err != nil {
			return CLIError(exitcode.InternalError, "send C-c: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
		_ = tmux.SendKeysCmd(sess, id, "C-c").Run()

		runShPath := filepath.Join(slaveDir, "run.sh")

		w, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer w.Close()
		if err := w.Add(slaveDir); err != nil {
			return err
		}

		if err := tmux.SendKeysCmd(sess, id, runShPath, "Enter").Run(); err != nil {
			return CLIError(exitcode.InternalError, "relaunch run.sh: %v", err)
		}

		deadline := time.After(30 * time.Second)
		for {
			select {
			case ev := <-w.Events:
				if ev.Op&fsnotify.Create == fsnotify.Create && filepath.Base(ev.Name) == ".ready" {
					return nil
				}
			case err := <-w.Errors:
				return err
			case <-deadline:
				return CLIError(exitcode.Crash, "slave did not become ready within 30s after reset")
			}
		}
	},
}

func init() {
	Root.AddCommand(resetCmd)
}
