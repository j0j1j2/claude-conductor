package cli

import (
	"os"
	"path/filepath"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var unstickCmd = &cobra.Command{
	Use:   "unstick <slave-id>",
	Short: "Force-clear a stale .pending lock without touching the running slave",
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
		for _, f := range []string{".pending", ".done"} {
			_ = os.Remove(filepath.Join(slaveDir, f))
		}
		return nil
	},
}

func init() {
	Root.AddCommand(unstickCmd)
}
