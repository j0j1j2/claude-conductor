package cli

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
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
		return CLIError(exitcode.InternalError, "os.Executable: %v", err)
	}

	// If session already exists, just attach.
	if tmux.HasSessionCmd(sessionName).Run() == nil {
		att := tmux.AttachSessionCmd(sessionName)
		att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
		return att.Run()
	}

	newCmd := tmux.NewSessionCmdShell(sessionName)
	newCmd.Dir = projectCwd
	if err := newCmd.Run(); err != nil {
		return CLIError(exitcode.InternalError, "tmux new-session: %v", err)
	}

	// Wipe any stale state directory from a prior dead session.
	if err := os.RemoveAll(state.SessionDir(sessionName)); err != nil {
		_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return CLIError(exitcode.InternalError, "clean stale state dir: %v", err)
	}

	if err := tmux.SendKeysCmd(sessionName, "0", conductorBin, "Enter").Run(); err != nil {
		_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()
		return CLIError(exitcode.InternalError, "send-keys conductor: %v", err)
	}

	att := tmux.AttachSessionCmd(sessionName)
	att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
	return att.Run()
}

// sessionNameFromCwd builds a tmux session name unique per project directory.
// Format: "conductor-<basename>-<short-hash>". The hash disambiguates two
// different directories that happen to share a basename.
func sessionNameFromCwd(cwd string) string {
	base := filepath.Base(cwd)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, base)
	if base == "" || base == "_" {
		base = "session"
	}
	sum := sha1.Sum([]byte(cwd))
	return fmt.Sprintf("conductor-%s-%s", base, hex.EncodeToString(sum[:3]))
}

// scrubbedEnv returns os.Environ() minus any CONDUCTOR_SLAVE_ID entry so the
// master claude does not inherit a slave gate from the user's shell. Without
// this scrub, the master's own Stop hook could fire and corrupt slave state.
func scrubbedEnv() []string {
	in := os.Environ()
	out := make([]string, 0, len(in))
	for _, kv := range in {
		if strings.HasPrefix(kv, "CONDUCTOR_SLAVE_ID=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// insideTmux: do per-session setup, pre-spawn s1, then *replace* this
// process with `claude` so window 0 stays alive as the master session.
func insideTmux(projectCwd string) error {
	sess, err := tmux.CurrentSession()
	if err != nil {
		return CLIError(exitcode.InternalError, "current tmux session: %v", err)
	}
	sessionDir := state.SessionDir(sess)
	masterDir := filepath.Join(sessionDir, "master")
	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return CLIError(exitcode.InternalError, "mkdir master dir: %v", err)
	}
	systemPath := filepath.Join(masterDir, "SYSTEM.md")
	if err := os.WriteFile(systemPath, []byte(masterSystemPrompt), 0o644); err != nil {
		return CLIError(exitcode.InternalError, "write SYSTEM.md: %v", err)
	}

	sessionMeta, err := json.Marshal(map[string]string{
		"session": sess,
		"cwd":     projectCwd,
		"started": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return CLIError(exitcode.InternalError, "marshal session.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.json"), sessionMeta, 0o644); err != nil {
		return CLIError(exitcode.InternalError, "write session.json: %v", err)
	}

	conductorBin, err := os.Executable()
	if err != nil {
		return CLIError(exitcode.InternalError, "os.Executable: %v", err)
	}

	restoreHooks, err := installProjectHooks(projectCwd, conductorBin)
	if err != nil {
		return CLIError(exitcode.InternalError, "install project hooks: %v", err)
	}
	bootstrapSucceeded := false
	defer func() {
		if !bootstrapSucceeded {
			restoreHooks()
			_ = os.RemoveAll(masterDir)
		}
	}()

	spawnCmd := exec.Command(conductorBin, "spawn", "--name", "s1")
	spawnCmd.Stdout = os.Stdout
	spawnCmd.Stderr = os.Stderr
	if err := spawnCmd.Run(); err != nil {
		return CLIError(exitcode.InternalError, "pre-spawn s1: %v", err)
	}

	sysBytes, err := os.ReadFile(systemPath)
	if err != nil {
		return CLIError(exitcode.InternalError, "read SYSTEM.md: %v", err)
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
	// We are about to hand the process off to claude permanently. After this
	// point our defers will not run, so we mark bootstrap successful first.
	bootstrapSucceeded = true
	if err := syscall.Exec(claudePath, argv, scrubbedEnv()); err != nil {
		return CLIError(exitcode.InternalError, "exec claude: %v", err)
	}
	return nil
}
