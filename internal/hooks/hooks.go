// Package hooks renders Claude Code hook settings and a run.sh wrapper.
package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ConductorMarker is a sentinel comment embedded inside the rendered
// settings.local.json. installProjectHooks uses it (and ONLY it) to decide
// whether an existing file is conductor-owned, avoiding false positives from
// substring matches the user might legitimately have copied.
const ConductorMarker = "conductor-managed-do-not-edit-this-block"

// RenderProjectSettings returns the project-level `.claude/settings.local.json`
// content. Hook commands are env-var gated on CONDUCTOR_SLAVE_ID so only
// slave Claude processes (whose run.sh exports the var) trigger them; the
// master session, sharing the same cwd, treats the hooks as no-ops.
func RenderProjectSettings(conductorBin string) string {
	binQ := ShellQuote(conductorBin)
	stopCmd := fmt.Sprintf(
		`[ -n "$CONDUCTOR_SLAVE_ID" ] && %s _internal_stop_marker "$CONDUCTOR_SLAVE_ID"`,
		binQ)
	readyCmd := fmt.Sprintf(
		`[ -n "$CONDUCTOR_SLAVE_ID" ] && %s _internal_session_ready "$CONDUCTOR_SLAVE_ID"`,
		binQ)
	cfg := map[string]any{
		"_conductor": ConductorMarker, // sentinel for hooks_install.looksLikeConductor
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
// is firing, runs claude as a child (NOT via `exec`) so the shell stays
// alive long enough to capture claude's exit status, and writes that
// status to .exit-code.
//
// All embedded values are single-quoted with `'\''` escaping so a directory
// containing quotes or shell metacharacters cannot inject shell commands.
func RenderRunScript(slaveDir, projectCwd, slaveID string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
export CONDUCTOR_SLAVE_ID=%s
cd %s
claude --dangerously-skip-permissions
ec=$?
echo "$ec" > %s
exit "$ec"
`,
		ShellQuote(slaveID),
		ShellQuote(projectCwd),
		ShellQuote(slaveDir+"/.exit-code"),
	)
}

// ShellQuote single-quotes s for bash/sh and escapes any embedded single
// quotes via the standard `'\''` idiom. Exported so other packages can
// reuse the same escaping rule without re-implementing it.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
