package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/audit"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
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
	if err := audit.Append(path, audit.Entry{
		Timestamp:  time.Now().UTC(),
		Cmd:        cmd.Name(),
		Args:       args,
		DurationMS: time.Since(cmdStart).Milliseconds(),
		Exit:       exit,
	}); err != nil && os.Getenv("CONDUCTOR_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "warn: audit append:", err)
	}
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

// versionCmd prints build metadata from runtime/debug.ReadBuildInfo so users
// can answer "what conductor am I running" without us cutting tags.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print conductor version and build info",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			fmt.Println("conductor (build info unavailable)")
			return nil
		}
		var rev, modified, when string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			case "vcs.time":
				when = s.Value
			}
		}
		fmt.Printf("conductor %s\n", info.Main.Version)
		if rev != "" {
			short := rev
			if len(short) > 12 {
				short = short[:12]
			}
			suffix := ""
			if modified == "true" {
				suffix = " (dirty)"
			}
			fmt.Printf("commit %s%s  %s\n", short, suffix, when)
		}
		fmt.Printf("go     %s\n", info.GoVersion)
		return nil
	},
}

func init() { Root.AddCommand(versionCmd) }

// ExecuteRoot runs the root command and returns the actual executed subcommand.
func ExecuteRoot() (*cobra.Command, error) {
	return Root.ExecuteC()
}

// WriteAudit is exposed for main.go to call after Execute().
func WriteAudit(c *cobra.Command, args []string, exit int) {
	writeAudit(c, args, exit)
}
