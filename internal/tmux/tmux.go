// Package tmux builds (and runs) tmux commands.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// InTmux reports whether the process is running inside a tmux session.
func InTmux() bool {
	return os.Getenv("TMUX") != ""
}

// SendKeysCmd builds `tmux send-keys -t <session>:<window> <keys>...`.
func SendKeysCmd(session, window string, keys ...string) *exec.Cmd {
	args := []string{"send-keys", "-t", session + ":" + window}
	args = append(args, keys...)
	return exec.Command("tmux", args...)
}

// SendLiteralCmd builds `tmux send-keys -l -t <session>:<window> <text>`.
// The -l flag forces the text to be treated as literal keystrokes, bypassing
// tmux's key-name interpretation. This is the safe way to type arbitrary text
// (including multi-line prompts) into a TUI running inside the pane.
func SendLiteralCmd(session, window, text string) *exec.Cmd {
	return exec.Command("tmux", "send-keys", "-l", "-t", session+":"+window, text)
}

// LoadBufferCmd builds `tmux load-buffer <file>`.
func LoadBufferCmd(path string) *exec.Cmd {
	return exec.Command("tmux", "load-buffer", path)
}

// PasteBufferCmd builds `tmux paste-buffer -t <session>:<window>`.
func PasteBufferCmd(session, window string) *exec.Cmd {
	return exec.Command("tmux", "paste-buffer", "-t", session+":"+window)
}

// NewWindowCmd builds `tmux new-window -d -t <session> -n <name> <cmd>`.
// The -d flag creates the window without switching focus to it, so spawning
// a slave does not yank the user away from the master window.
func NewWindowCmd(session, name, command string) *exec.Cmd {
	return exec.Command("tmux", "new-window", "-d", "-t", session, "-n", name, command)
}

// KillWindowCmd builds `tmux kill-window -t <session>:<window>`.
func KillWindowCmd(session, window string) *exec.Cmd {
	return exec.Command("tmux", "kill-window", "-t", session+":"+window)
}

// HasSessionCmd builds `tmux has-session -t <session>`.
func HasSessionCmd(session string) *exec.Cmd {
	return exec.Command("tmux", "has-session", "-t", session)
}

// NewSessionCmd builds `tmux new-session -d -s <session> <command>`.
func NewSessionCmd(session, command string) *exec.Cmd {
	return exec.Command("tmux", "new-session", "-d", "-s", session, command)
}

// NewSessionCmdShell builds `tmux new-session -d -s <session>` using the
// default shell for window 0 (no command override).
func NewSessionCmdShell(session string) *exec.Cmd {
	return exec.Command("tmux", "new-session", "-d", "-s", session)
}

// AttachSessionCmd builds `tmux attach-session -t <session>`.
func AttachSessionCmd(session string) *exec.Cmd {
	return exec.Command("tmux", "attach-session", "-t", session)
}

// CurrentSession returns the tmux session name the process is attached to.
// Caller must ensure InTmux() is true.
func CurrentSession() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#S").Output()
	if err != nil {
		return "", err
	}
	return parseSessionName(string(out)), nil
}

func parseSessionName(s string) string {
	return strings.TrimSpace(s)
}

// PaneDead reports whether the given window's first pane is marked dead.
func PaneDead(session, window string) (bool, error) {
	out, err := exec.Command("tmux", "list-panes",
		"-t", session+":"+window, "-F", "#{pane_dead}").Output()
	if err != nil {
		return false, err
	}
	return parsePaneDead(string(out)), nil
}

// CapturePane returns the last n lines of the given window's pane buffer.
func CapturePane(session, window string, n int) (string, error) {
	out, err := exec.Command("tmux", "capture-pane",
		"-p", "-t", session+":"+window,
		"-S", fmt.Sprintf("-%d", n)).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parsePaneDead(s string) bool {
	return strings.TrimSpace(s) == "1"
}
