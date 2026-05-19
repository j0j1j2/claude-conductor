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

// WindowGone classifies an error from PaneDead/CapturePane as a "the window
// does not exist" condition. tmux writes "can't find window" / "can't find
// pane" on stderr when the target is gone; exec.Cmd surfaces that as an
// *exec.ExitError whose Stderr we can inspect.
func WindowGone(err error) bool {
	if err == nil {
		return false
	}
	var ee *exec.ExitError
	if !errorsAs(err, &ee) {
		return strings.Contains(err.Error(), "can't find")
	}
	s := strings.ToLower(string(ee.Stderr))
	return strings.Contains(s, "can't find") ||
		strings.Contains(s, "no such") ||
		strings.Contains(s, "not found")
}

// errorsAs is a tiny shim to avoid pulling in `errors` just for As().
func errorsAs(err error, target any) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		if p, ok := target.(**exec.ExitError); ok {
			*p = ee
			return true
		}
	}
	return false
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
