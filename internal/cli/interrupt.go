package cli

import (
	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var interruptCmd = &cobra.Command{
	Use:   "interrupt <slave-id>",
	Short: "Cancel the slave's current turn (sends Escape)",
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
		if err := tmux.SendKeysCmd(sess, id, "Escape").Run(); err != nil {
			return CLIError(exitcode.InternalError, "send Escape: %v", err)
		}
		return nil
	},
}

func init() {
	Root.AddCommand(interruptCmd)
}
