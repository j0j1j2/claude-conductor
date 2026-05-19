package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/hooks"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var resetForce bool

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
		if err := state.ValidateSlaveID(id); err != nil {
			return CLIError(exitcode.UnknownSlave, "invalid slave id %q: %v", id, err)
		}
		if !state.SlaveExists(sess, id) {
			return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
		}
		slaveDir := state.SlaveDir(sess, id)
		if state.IsBusy(slaveDir) && !resetForce {
			return CLIError(exitcode.Busy,
				"slave %s is busy; use `conductor interrupt %s` first, or pass --force", id, id)
		}

		for _, f := range []string{".pending", ".done", ".ready", ".exit-code", ".ready-error"} {
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
			return CLIError(exitcode.InternalError, "fsnotify: %v", err)
		}
		defer w.Close()
		if err := w.Add(slaveDir); err != nil {
			return CLIError(exitcode.InternalError, "fsnotify watch: %v", err)
		}

		// Type the quoted path as literal keystrokes so a HOME containing
		// spaces or special chars does not break the slave's shell, and so
		// tmux's key-name parser does not try to interpret it.
		quoted := hooks.ShellQuote(runShPath)
		if err := tmux.SendLiteralCmd(sess, id, quoted).Run(); err != nil {
			return CLIError(exitcode.InternalError, "type run.sh path: %v", err)
		}
		if err := tmux.SendKeysCmd(sess, id, "Enter").Run(); err != nil {
			return CLIError(exitcode.InternalError, "send Enter: %v", err)
		}

		deadline := time.After(30 * time.Second)
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify channel closed")
				}
				if ev.Op&fsnotify.Create == fsnotify.Create && filepath.Base(ev.Name) == ".ready" {
					return nil
				}
			case werr, ok := <-w.Errors:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify error channel closed")
				}
				return CLIError(exitcode.InternalError, "fsnotify: %v", werr)
			case <-deadline:
				return CLIError(exitcode.Crash, "slave did not become ready within 30s after reset")
			}
		}
	},
}

func init() {
	resetCmd.Flags().BoolVar(&resetForce, "force", false, "reset even if a `conductor send` is in flight")
	Root.AddCommand(resetCmd)
}
