package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Root = &cobra.Command{
	Use:   "conductor",
	Short: "Master-slave orchestrator for Claude Code sessions in tmux",
	// RunE is wired in Task 16 (bootstrap).
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
