package tmux

import (
	"strings"
	"testing"
)

func TestSendKeysCmd(t *testing.T) {
	c := SendKeysCmd("mysession", "s1", "Escape")
	if !strings.Contains(strings.Join(c.Args, " "), "send-keys -t mysession:s1 Escape") {
		t.Errorf("unexpected args: %v", c.Args)
	}
}

func TestLoadBufferCmd(t *testing.T) {
	c := LoadBufferCmd("/tmp/prompt.txt")
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "load-buffer /tmp/prompt.txt") {
		t.Errorf("unexpected args: %v", c.Args)
	}
}

func TestPasteBufferCmd(t *testing.T) {
	c := PasteBufferCmd("sess", "s1")
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "paste-buffer -t sess:s1") {
		t.Errorf("unexpected args: %v", c.Args)
	}
}

func TestNewWindowCmd(t *testing.T) {
	c := NewWindowCmd("sess", "s2", "/path/to/run.sh")
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "new-window -d -t sess -n s2 /path/to/run.sh") {
		t.Errorf("unexpected args: %v", c.Args)
	}
}

func TestKillWindowCmd(t *testing.T) {
	c := KillWindowCmd("sess", "s1")
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "kill-window -t sess:s1") {
		t.Errorf("unexpected args: %v", c.Args)
	}
}

func TestInTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	if !InTmux() {
		t.Error("expected InTmux() = true")
	}

	t.Setenv("TMUX", "")
	if InTmux() {
		t.Error("expected InTmux() = false")
	}
}

func TestParsePaneDeadOutput(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0\n", false},
		{"1\n", true},
		{"", false},
		{" 1 ", true},
	}
	for _, c := range cases {
		if got := parsePaneDead(c.in); got != c.want {
			t.Errorf("parsePaneDead(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseSessionName(t *testing.T) {
	if got := parseSessionName("conductor\n"); got != "conductor" {
		t.Errorf("got %q, want %q", got, "conductor")
	}
	if got := parseSessionName("  my-sess  \n"); got != "my-sess" {
		t.Errorf("got %q, want %q", got, "my-sess")
	}
}
