package cmd

import (
	"fmt"
	"os"

	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var lastCmd = &cobra.Command{
	Use:   "last <slave-id>",
	Short: "Print slave's last assistant response (non-blocking)",
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
		content, err := state.ReadDone(state.SlaveDir(sess, id))
		if err != nil {
			if os.IsNotExist(err) {
				return CLIError(exitcode.InternalError, "no prior response for %s", id)
			}
			return err
		}
		fmt.Print(content)
		return nil
	},
}

func init() {
	Root.AddCommand(lastCmd)
}
