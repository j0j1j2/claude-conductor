package cli

import (
	"os"
	"path/filepath"

	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var internalSessionReadyCmd = &cobra.Command{
	Use:    "_internal_session_ready <slave-id>",
	Short:  "Internal: SessionStart hook target; touches .ready",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := tmux.CurrentSession()
		if err != nil {
			return err
		}
		slaveDir := state.SlaveDir(sess, args[0])
		f, err := os.Create(filepath.Join(slaveDir, ".ready"))
		if err != nil {
			return err
		}
		return f.Close()
	},
}

func init() {
	Root.AddCommand(internalSessionReadyCmd)
}
