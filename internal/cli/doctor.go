package cli

import (
	"fmt"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor <slave-id>",
	Short: "Print a diagnostic report for a slave (use after a timeout)",
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
		fmt.Print(Diagnose(sess, id).Full())
		return nil
	},
}

func init() {
	Root.AddCommand(doctorCmd)
}
