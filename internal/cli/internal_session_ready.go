package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var internalSessionReadyCmd = &cobra.Command{
	Use:    "_internal_session_ready <slave-id>",
	Short:  "Internal: SessionStart hook target; touches .ready",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		slaveID := args[0]
		if err := state.ValidateSlaveID(slaveID); err != nil {
			return fmt.Errorf("invalid slave id %q: %w", slaveID, err)
		}
		sess, err := tmux.CurrentSession()
		if err != nil {
			return err
		}
		slaveDir := state.SlaveDir(sess, slaveID)
		if slaveDir == "" {
			return fmt.Errorf("invalid slave dir for id %q", slaveID)
		}
		f, err := os.Create(filepath.Join(slaveDir, ".ready"))
		if err != nil {
			_ = os.WriteFile(filepath.Join(slaveDir, ".ready-error"), []byte(err.Error()), 0o644)
			return err
		}
		return f.Close()
	},
}

func init() {
	Root.AddCommand(internalSessionReadyCmd)
}
