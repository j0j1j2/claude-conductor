package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var lastCmd = &cobra.Command{
	Use:   "last <slave-id>",
	Short: "Print slave's last assistant response (non-blocking; empty if none yet)",
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
		d, err := state.ReadDone(state.SlaveDir(sess, id))
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "(no response yet)")
				return nil
			}
			return CLIError(exitcode.InternalError, "read .done: %v", err)
		}
		text := d.Text
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		fmt.Print(text)
		return nil
	},
}

func init() {
	Root.AddCommand(lastCmd)
}
