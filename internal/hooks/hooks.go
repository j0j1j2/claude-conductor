// Package hooks renders slave-side Claude Code hook settings and a run.sh wrapper.
package hooks

import (
	"encoding/json"
	"fmt"
)

// RenderSettings returns the slave's settings.json content with Stop and
// SessionStart hooks. `conductorBin` must be an absolute path so the hook
// resolves regardless of the tmux pane's PATH.
func RenderSettings(slaveID, conductorBin string) string {
	cfg := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []map[string]any{
				{
					"matcher": "",
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": fmt.Sprintf("%s _internal_session_ready %s", conductorBin, slaveID),
						},
					},
				},
			},
			"Stop": []map[string]any{
				{
					"matcher": "",
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": fmt.Sprintf("%s _internal_stop_marker %s", conductorBin, slaveID),
						},
					},
				},
			},
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b)
}

// RenderRunScript returns the content of the slave's run.sh. It exports
// CLAUDE_CONFIG_DIR to the slave's state dir so Claude picks up the
// slave-specific settings.json (hooks), then execs claude in the project cwd.
func RenderRunScript(slaveDir, projectCwd string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
export CLAUDE_CONFIG_DIR="%s"
trap 'echo $? > "%s/.exit-code"' EXIT
cd "%s"
exec claude --dangerously-skip-permissions
`, slaveDir, slaveDir, projectCwd)
}
