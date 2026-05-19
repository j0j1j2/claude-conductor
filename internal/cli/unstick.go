package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var unstickForce bool

var unstickCmd = &cobra.Command{
	Use:   "unstick <slave-id>",
	Short: "Clear a stale .pending lock; refuses live locks unless --force",
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
		if state.IsBusy(slaveDir) && !unstickForce {
			lock, _ := state.ReadPending(slaveDir)
			return CLIError(exitcode.Busy,
				"slave %s has a LIVE lock (pid %d); refusing without --force", id, lock.PID)
		}
		for _, f := range []string{".pending", ".done"} {
			_ = os.Remove(filepath.Join(slaveDir, f))
		}
		fmt.Fprintln(os.Stderr, "cleared .pending and .done for", id)
		return nil
	},
}

func init() {
	unstickCmd.Flags().BoolVar(&unstickForce, "force", false, "clear even a live (PID-alive) lock")
	Root.AddCommand(unstickCmd)
}
