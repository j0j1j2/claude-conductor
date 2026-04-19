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
	if !strings.Contains(got, "/usr/local/bin/conductor") {
		t.Error("must use absolute conductor path")
	}
}

func TestRenderRunScript(t *testing.T) {
	got := RenderRunScript("/slave/dir", "/home/me/proj", "s1")
	if !strings.HasPrefix(got, "#!/usr/bin/env bash\n") {
		t.Error("missing shebang")
	}
	if !strings.Contains(got, `cd "/home/me/proj"`) {
		t.Error("must cd into project dir")
	}
	if !strings.Contains(got, `exec claude --dangerously-skip-permissions`) {
		t.Error("must exec claude with flag")
	}
	if !strings.Contains(got, `echo $? > "/slave/dir/.exit-code"`) {
		t.Error("must trap exit code")
	}
	if !strings.Contains(got, `export CONDUCTOR_SLAVE_ID="s1"`) {
		t.Error("must export CONDUCTOR_SLAVE_ID so shared hook identifies slave")
	}
	if strings.Contains(got, "CLAUDE_CONFIG_DIR") {
		t.Error("must not touch CLAUDE_CONFIG_DIR (breaks user auth)")
	}
}
