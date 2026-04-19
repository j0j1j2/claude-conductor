package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cloudchamb3r/claude-conductor/internal/exitcode"
	"github.com/cloudchamb3r/claude-conductor/internal/state"
	"github.com/cloudchamb3r/claude-conductor/internal/tmux"
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
		sessionName := "conductor"
		conductorBin, err := os.Executable()
		if err != nil {
			return err
		}
		if tmux.HasSessionCmd(sessionName).Run() == nil {
			att := tmux.AttachSessionCmd(sessionName)
			att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
			return att.Run()
		}
		newCmd := tmux.NewSessionCmd(sessionName, conductorBin)
		newCmd.Dir = projectCwd
		if err := newCmd.Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux new-session: %v", err)
		}
		att := tmux.AttachSessionCmd(sessionName)
		att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
		return att.Run()
	}

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

	masterLaunch := fmt.Sprintf(
		`cd %q && claude --append-system-prompt "$(cat %q)" --dangerously-skip-permissions`,
		projectCwd, systemPath)
	if err := tmux.SendKeysCmd(sess, "0", masterLaunch, "Enter").Run(); err != nil {
		return CLIError(exitcode.InternalError, "launch master: %v", err)
	}

	conductorBin, _ := os.Executable()
	spawnCmd := exec.Command(conductorBin, "spawn", "--name", "s1")
	spawnCmd.Stdout = os.Stdout
	spawnCmd.Stderr = os.Stderr
	if err := spawnCmd.Run(); err != nil {
		return CLIError(exitcode.InternalError, "pre-spawn s1: %v", err)
	}
	return nil
}
