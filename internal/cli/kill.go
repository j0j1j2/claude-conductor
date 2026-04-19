package cli

import (
	"os"

	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

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
		if !state.SlaveExists(sess, id) {
			return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
		}

		_ = tmux.KillWindowCmd(sess, id).Run()

		if err := os.RemoveAll(state.SlaveDir(sess, id)); err != nil {
			return CLIError(exitcode.InternalError, "remove state dir: %v", err)
		}
		return nil
	},
}

func init() {
	Root.AddCommand(killCmd)
}
