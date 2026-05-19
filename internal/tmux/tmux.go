// Package tmux builds (and runs) tmux commands.
package tmux

import (
	"errors"
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
// The -l flag forces the text to be treated as literal keystrokes.
func SendLiteralCmd(session, window, text string) *exec.Cmd {
	return exec.Command("tmux", "send-keys", "-l", "-t", session+":"+window, text)
}

// NewWindowCmd builds `tmux new-window -d -t <session> -n <name> <cmd>`.
// The -d flag creates the window without switching focus to it.
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
func CurrentSession() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#S").Output()
	if err != nil {
		return "", err
	}
	name := parseSessionName(string(out))
	if name == "" {
		return "", fmt.Errorf("tmux returned empty session name")
	}
	return name, nil
}

func parseSessionName(s string) string {
	return strings.TrimSpace(s)
}

// PaneDead reports whether the given window's first pane is marked dead.
// On error the bool is false and the caller should consult WindowGone to
// decide if the error itself indicates the window no longer exists.
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

// WindowGone classifies an error from PaneDead/CapturePane as a "the window
// does not exist" condition.
func WindowGone(err error) bool {
	if err == nil {
		return false
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		s := strings.ToLower(string(ee.Stderr))
		return strings.Contains(s, "can't find") ||
			strings.Contains(s, "no such") ||
			strings.Contains(s, "not found")
	}
	return strings.Contains(err.Error(), "can't find")
}
