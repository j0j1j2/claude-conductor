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

	if _, lerr := exec.LookPath("tmux"); lerr != nil {
		return CLIError(exitcode.InternalError, "tmux is required but not found on PATH: %v", lerr)
	}

	// If session already exists, verify it's actually conductor-managed
	// before attaching, so we don't drop the user into a foreign tmux
	// session with a hash-colliding name.
	if tmux.HasSessionCmd(sessionName).Run() == nil {
		sessionFile := filepath.Join(state.SessionDir(sessionName), "session.json")
		if _, sErr := os.Stat(sessionFile); sErr != nil {
			return CLIError(exitcode.InternalError,
				"tmux session %q exists but %s is missing; not attaching (use `tmux kill-session -t %s` if you want a fresh conductor)",
				sessionName, sessionFile, sessionName)
		}
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

// scrubbedEnv returns os.Environ() minus any CONDUCTOR_* entry so the master
// claude (and any subprocess it spawns) does not inherit a slave gate from
// the user's shell, and so future internal env vars are protected by default.
func scrubbedEnv() []string {
	in := os.Environ()
	out := make([]string, 0, len(in))
	for _, kv := range in {
		if strings.HasPrefix(kv, "CONDUCTOR_") {
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

	// Resolve everything we need BEFORE spawning s1 so a missing `claude`
	// binary or unreadable SYSTEM.md doesn't leak a spawned slave window.
	sysBytes, err := os.ReadFile(systemPath)
	if err != nil {
		return CLIError(exitcode.InternalError, "read SYSTEM.md: %v", err)
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return CLIError(exitcode.InternalError, "cannot find `claude` on PATH: %v", err)
	}

	restoreHooks, err := installProjectHooks(projectCwd, conductorBin)
	if err != nil {
		return CLIError(exitcode.InternalError, "install project hooks: %v", err)
	}
	// Rollback if anything below this point fails. syscall.Exec replaces our
	// process on success, so the defer NEVER runs on success — meaning we
	// must NOT pre-mark "succeeded". An Exec failure correctly triggers
	// rollback (hooks restored, master/s1 cleaned up).
	bootstrapFailed := true
	var s1Spawned bool
	defer func() {
		if !bootstrapFailed {
			return
		}
		restoreHooks()
		_ = os.RemoveAll(masterDir)
		if s1Spawned {
			_ = tmux.KillWindowCmd(sess, "s1").Run()
			_ = os.RemoveAll(state.SlaveDir(sess, "s1"))
		}
	}()

	spawnCmd := exec.Command(conductorBin, "spawn", "--name", "s1")
	spawnCmd.Stdout = os.Stdout
	spawnCmd.Stderr = os.Stderr
	if err := spawnCmd.Run(); err != nil {
		return CLIError(exitcode.InternalError, "pre-spawn s1: %v", err)
	}
	s1Spawned = true

	argv := []string{
		"claude",
		"--append-system-prompt", string(sysBytes),
		"--dangerously-skip-permissions",
	}
	// syscall.Exec returns ONLY on failure; on success the process is
	// replaced and no Go code runs after. So if we reach the next line, it
	// is unambiguously a failure and the defer rollback should fire.
	execErr := syscall.Exec(claudePath, argv, scrubbedEnv())
	return CLIError(exitcode.InternalError, "exec claude: %v", execErr)
}
