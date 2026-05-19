package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderProjectSettings_validJSON(t *testing.T) {
	got := RenderProjectSettings("/usr/local/bin/conductor")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["_conductor"] != ConductorMarker {
		t.Error("missing conductor sentinel marker")
	}
	if !strings.Contains(got, "Stop") {
		t.Error("missing Stop hook")
	}
	if !strings.Contains(got, "SessionStart") {
		t.Error("missing SessionStart hook")
	}
	if !strings.Contains(got, "$CONDUCTOR_SLAVE_ID") {
		t.Error("hook must be gated on $CONDUCTOR_SLAVE_ID env var")
	}
	if !strings.Contains(got, "_internal_stop_marker") {
		t.Error("missing _internal_stop_marker reference")
	}
	if !strings.Contains(got, "_internal_session_ready") {
		t.Error("missing _internal_session_ready reference")
	}
	if !strings.Contains(got, "'/usr/local/bin/conductor'") {
		t.Error("conductor path must be single-quoted")
	}
}

func TestRenderProjectSettings_pathWithSpacesAndQuotes(t *testing.T) {
	got := RenderProjectSettings(`/Users/me/Go Stuff/conductor's bin`)
	// Parse the JSON and validate the embedded shell command, since JSON
	// encoding will escape the backslashes we emit for single-quote handling.
	var cfg struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(cfg.Hooks.Stop) == 0 || len(cfg.Hooks.Stop[0].Hooks) == 0 {
		t.Fatalf("no Stop hook found in %s", got)
	}
	cmd := cfg.Hooks.Stop[0].Hooks[0].Command
	want := `'/Users/me/Go Stuff/conductor'\''s bin'`
	if !strings.Contains(cmd, want) {
		t.Errorf("expected escaped quoted path %q in command, got:\n%s", want, cmd)
	}
}

func TestRenderRunScript_quotedValues(t *testing.T) {
	got := RenderRunScript("/slave/dir", "/home/me/proj", "s1")
	if !strings.HasPrefix(got, "#!/usr/bin/env bash\n") {
		t.Error("missing shebang")
	}
	if !strings.Contains(got, `cd '/home/me/proj'`) {
		t.Error("must cd into single-quoted project dir")
	}
	if !strings.Contains(got, `exec claude --dangerously-skip-permissions`) {
		t.Error("must exec claude with flag")
	}
	if !strings.Contains(got, `echo $? > '/slave/dir/.exit-code'`) {
		t.Error("must trap exit code with quoted path")
	}
	if !strings.Contains(got, `export CONDUCTOR_SLAVE_ID='s1'`) {
		t.Error("must export CONDUCTOR_SLAVE_ID quoted")
	}
	if strings.Contains(got, "CLAUDE_CONFIG_DIR") {
		t.Error("must not touch CLAUDE_CONFIG_DIR (breaks user auth)")
	}
}

func TestRenderRunScript_pathWithSpaces(t *testing.T) {
	got := RenderRunScript("/state/s 1", "/My Proj", "s1")
	if !strings.Contains(got, `cd '/My Proj'`) {
		t.Errorf("project cwd not quoted: %s", got)
	}
	if !strings.Contains(got, `'/state/s 1/.exit-code'`) {
		t.Errorf("state path not quoted: %s", got)
	}
}

func TestRenderRunScript_pathWithSingleQuote(t *testing.T) {
	// A directory like `/tmp/foo"; rm -rf ~; #` would inject if not escaped.
	got := RenderRunScript("/s", `/tmp/foo'; rm -rf ~; echo '`, "s1")
	// Single quotes inside single-quoted strings must be escaped via '\''
	if !strings.Contains(got, `'/tmp/foo'\''; rm -rf ~; echo '\'''`) {
		t.Errorf("single quote not escaped: %s", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"with'quote", `'with'\''quote'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
