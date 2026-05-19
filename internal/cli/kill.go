package cli

import (
	"os"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var killForce bool

var killCmd = &cobra.Command{
	Use:   "kill <slave-id>",
	Short: "Close the slave's tmux window and remove its state dir",
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
		if state.IsBusy(slaveDir) && !killForce {
			return CLIError(exitcode.Busy,
				"slave %s is busy; use `conductor interrupt %s` first, or pass --force", id, id)
		}

		_ = tmux.KillWindowCmd(sess, id).Run()
		if err := os.RemoveAll(slaveDir); err != nil {
			return CLIError(exitcode.InternalError, "remove state dir: %v", err)
		}
		return nil
	},
}

func init() {
	killCmd.Flags().BoolVar(&killForce, "force", false, "kill even if a `conductor send` is in flight")
	Root.AddCommand(killCmd)
}
