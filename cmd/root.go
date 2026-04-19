package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/cloudchamb3r/claude-conductor/internal/audit"
	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

var cmdStart time.Time

var Root = &cobra.Command{
	Use:   "conductor",
	Short: "Master-slave orchestrator for Claude Code sessions in tmux",
	RunE: runRootBootstrap,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmdStart = time.Now()
	},
}

// writeAudit is called by main.go after Execute() so it can observe the
// final exit code regardless of whether the command returned an error.
func writeAudit(cmd *cobra.Command, args []string, exit int) {
	if !tmux.InTmux() {
		return
	}
	sess, err := tmux.CurrentSession()
	if err != nil {
		return
	}
	path := filepath.Join(state.SessionDir(sess), "audit.log")
	_ = audit.Append(path, audit.Entry{
		Timestamp:  time.Now().UTC(),
		Cmd:        cmd.Name(),
		Args:       args,
		DurationMS: time.Since(cmdStart).Milliseconds(),
		Exit:       exit,
	})
}

// ExitError carries a CLI exit code up to main.go.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// CLIError returns an ExitError with a formatted message.
func CLIError(code int, format string, a ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// ExecuteRoot runs the root command and returns the actual executed subcommand.
func ExecuteRoot() (*cobra.Command, error) {
	return Root.ExecuteC()
}

// WriteAudit is exposed for main.go to call after Execute().
func WriteAudit(c *cobra.Command, args []string, exit int) {
	writeAudit(c, args, exit)
}
