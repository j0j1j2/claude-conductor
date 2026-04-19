package cli

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
	"github.com/spf13/cobra"
)

const masterSystemPrompt = `You are the **master** in a claude-conductor session.

## Your role
- The user gives you a high-level GOAL.
- You do NOT perform the work yourself. You delegate to slave Claude sessions via the ` + "`conductor`" + ` CLI (available through Bash).
- Treat each slave as a fresh Claude. Prompts must be self-contained with all needed context.

## Available slaves
- ` + "`s1`" + ` is pre-spawned and ready.
- Spawn more with ` + "`conductor spawn`" + ` if work can be split safely (slaves share the cwd — avoid overlap).

## Workflow
1. Break the goal into delegatable chunks.
2. ` + "`conductor send <slave-id> \"<full prompt>\"`" + ` — blocks; stdout is the slave's final message.
3. Read output, decide:
   - Need more work → ` + "`conductor send`" + ` again (slave's context persists).
   - Slave asked a question → answer via ` + "`conductor send`" + `.
   - Off-track → ` + "`conductor interrupt <id>`" + ` then re-prompt, or ` + "`conductor reset <id>`" + ` for clean slate.
4. Loop until the goal is complete. Report to the user.

## Hard rules
- Do not use Read/Edit/Write/Grep/Glob on project files yourself unless the user speaks directly to you. That is the slaves' job.
- Do not ask the user "what should I do next?" unless the goal is genuinely ambiguous.
- Surface slave errors transparently.

## conductor CLI
- ` + "`conductor spawn [--name sN]`" + ` — new slave
- ` + "`conductor send [--timeout S] <id> \"<prompt>\"`" + ` — blocking
- ` + "`conductor interrupt <id>`" + ` — cancel current turn
- ` + "`conductor reset <id>`" + ` — fresh claude in same window
- ` + "`conductor kill <id>`" + ` — close window
- ` + "`conductor list`" + ` — active slaves
- ` + "`conductor last <id>`" + ` — print prior response
`

func runRootBootstrap(cmd *cobra.Command, args []string) error {
	projectCwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if !tmux.InTmux() {
		return outsideTmux(projectCwd)
	}
	return insideTmux(projectCwd)
}

// outsideTmux: create (or attach to) the tmux session, then re-enter
// `conductor` from inside window 0 via send-keys.
func outsideTmux(projectCwd string) error {
	sessionName := sessionNameFromCwd(projectCwd)
	conductorBin, err := os.Executable()
	if err != nil {
		return err
	}

	// If session already exists, just attach.
	if tmux.HasSessionCmd(sessionName).Run() == nil {
		att := tmux.AttachSessionCmd(sessionName)
		att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
		return att.Run()
	}

	// The tmux session does not exist. Any state left in
	// ~/.conductor/sessions/<sessionName>/ is from a prior, now-dead run;
	// remove it so the fresh session starts with a clean slate of slaves.
	if err := os.RemoveAll(state.SessionDir(sessionName)); err != nil {
		return CLIError(exitcode.InternalError, "clean stale state dir: %v", err)
	}

	// Create session with the default shell in window 0 (NOT conductor —
	// if we ran the conductor binary directly as the pane process, window 0
	// would close as soon as this function returned).
	newCmd := tmux.NewSessionCmdShell(sessionName)
	newCmd.Dir = projectCwd
	if err := newCmd.Run(); err != nil {
		return CLIError(exitcode.InternalError, "tmux new-session: %v", err)
	}

	// Type `conductor` into window 0's shell so we re-enter with $TMUX set.
	if err := tmux.SendKeysCmd(sessionName, "0", conductorBin, "Enter").Run(); err != nil {
		return CLIError(exitcode.InternalError, "send-keys conductor: %v", err)
	}

	// Attach.
	att := tmux.AttachSessionCmd(sessionName)
	att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
	return att.Run()
}

// sessionNameFromCwd builds a tmux session name unique per project directory.
// Format: "conductor-<basename>-<short-hash>". The hash disambiguates two
// different directories that happen to share a basename.
func sessionNameFromCwd(cwd string) string {
	base := filepath.Base(cwd)
	// Sanitize: tmux dislikes '.' and ':' in session names.
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, base)
	sum := sha1.Sum([]byte(cwd))
	return fmt.Sprintf("conductor-%s-%s", base, hex.EncodeToString(sum[:3]))
}

// insideTmux: do per-session setup, pre-spawn s1, then *replace* this
// process with `claude` so window 0 stays alive as the master session.
func insideTmux(projectCwd string) error {
	sess, err := tmux.CurrentSession()
	if err != nil {
		return err
	}
	sessionDir := state.SessionDir(sess)
	masterDir := filepath.Join(sessionDir, "master")
	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return err
	}
	systemPath := filepath.Join(masterDir, "SYSTEM.md")
	if err := os.WriteFile(systemPath, []byte(masterSystemPrompt), 0o644); err != nil {
		return err
	}

	sessionJSON := fmt.Sprintf(`{"session":"%s","cwd":"%s","started":"%s"}`,
		sess, projectCwd, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sessionDir, "session.json"), []byte(sessionJSON), 0o644); err != nil {
		return err
	}

	conductorBin, _ := os.Executable()
	if err := installProjectHooks(projectCwd, conductorBin); err != nil {
		return CLIError(exitcode.InternalError, "install project hooks: %v", err)
	}

	// Pre-spawn s1 in window 1.
	spawnCmd := exec.Command(conductorBin, "spawn", "--name", "s1")
	spawnCmd.Stdout = os.Stdout
	spawnCmd.Stderr = os.Stderr
	if err := spawnCmd.Run(); err != nil {
		return CLIError(exitcode.InternalError, "pre-spawn s1: %v", err)
	}

	// Read system prompt into memory so we can pass it as a single argv
	// element (avoids shell-quoting pain).
	sysBytes, err := os.ReadFile(systemPath)
	if err != nil {
		return err
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return CLIError(exitcode.InternalError, "cannot find `claude` on PATH: %v", err)
	}

	argv := []string{
		"claude",
		"--append-system-prompt", string(sysBytes),
		"--dangerously-skip-permissions",
	}
	// Replace the conductor process with claude. Does not return on success.
	if err := syscall.Exec(claudePath, argv, os.Environ()); err != nil {
		return CLIError(exitcode.InternalError, "exec claude: %v", err)
	}
	return nil
}
