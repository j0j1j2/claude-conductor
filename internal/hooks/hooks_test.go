package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderSettings_validJSON(t *testing.T) {
	got := RenderSettings("s1", "/usr/local/bin/conductor")
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
	if !strings.Contains(got, "_internal_stop_marker s1") {
		t.Error("Stop hook must call conductor _internal_stop_marker s1")
	}
	if !strings.Contains(got, "/usr/local/bin/conductor") {
		t.Error("Stop hook must use absolute conductor path")
	}
}

func TestRenderRunScript(t *testing.T) {
	got := RenderRunScript("/slave/dir", "/home/me/proj")
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
	if !strings.Contains(got, `export CLAUDE_CONFIG_DIR="/slave/dir"`) {
		t.Error("must export CLAUDE_CONFIG_DIR so slave picks up its own settings.json")
	}
}
