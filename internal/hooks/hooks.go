// Package hooks renders Claude Code hook settings and a run.sh wrapper.
package hooks

import (
	"encoding/json"
	"fmt"
)

// RenderProjectSettings returns the project-level `.claude/settings.local.json`
// content. Hook commands are env-var gated on CONDUCTOR_SLAVE_ID so only
// slave Claude processes (whose run.sh exports the var) trigger them; the
// master session, sharing the same cwd, treats the hooks as no-ops.
func RenderProjectSettings(conductorBin string) string {
	stopCmd := fmt.Sprintf(
		`[ -n "$CONDUCTOR_SLAVE_ID" ] && %s _internal_stop_marker "$CONDUCTOR_SLAVE_ID"`,
		conductorBin)
	readyCmd := fmt.Sprintf(
		`[ -n "$CONDUCTOR_SLAVE_ID" ] && %s _internal_session_ready "$CONDUCTOR_SLAVE_ID"`,
		conductorBin)
	cfg := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []map[string]any{
				{
					"matcher": "",
					"hooks": []map[string]any{
						{"type": "command", "command": readyCmd},
					},
				},
			},
			"Stop": []map[string]any{
				{
					"matcher": "",
					"hooks": []map[string]any{
						{"type": "command", "command": stopCmd},
					},
				},
			},
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b)
}

// RenderRunScript returns the content of the slave's run.sh. It exports
// CONDUCTOR_SLAVE_ID so the shared project hook can identify which slave
// is firing, traps the exit code, and execs claude in the project cwd.
func RenderRunScript(slaveDir, projectCwd, slaveID string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
export CONDUCTOR_SLAVE_ID="%s"
trap 'echo $? > "%s/.exit-code"' EXIT
cd "%s"
exec claude --dangerously-skip-permissions
`, slaveID, slaveDir, projectCwd)
}
