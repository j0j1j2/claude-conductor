package cli

import (
	"fmt"
	"os"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/transcript"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List active slaves in this conductor session",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.InTmux() {
			return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
		}
		sess, err := tmux.CurrentSession()
		if err != nil {
			return CLIError(exitcode.InternalError, "%v", err)
		}
		entries, err := os.ReadDir(state.SessionDir(sess))
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return CLIError(exitcode.InternalError, "read session dir: %v", err)
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "master" {
				continue
			}
			id := e.Name()
			if state.ValidateSlaveID(id) != nil {
				continue
			}
			slaveDir := state.SlaveDir(sess, id)
			st := "idle"
			if state.IsBusy(slaveDir) {
				st = "busy"
			}
			// Per-agent token spend, read from the slave's own transcript. A
			// missing or usage-free transcript reports zero, never an error --
			// a slave that has not spoken yet has simply cost nothing.
			tokens := 0
			if path := state.ReadTranscriptPath(slaveDir); path != "" {
				if u, err := transcript.TokensUsed(path); err == nil {
					tokens = u.Total()
				}
			}
			fmt.Printf("%s\t%s\t%d\n", id, st, tokens)
		}
		return nil
	},
}

func init() {
	Root.AddCommand(listCmd)
}
