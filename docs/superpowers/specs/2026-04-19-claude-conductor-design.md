# claude-conductor — Design

**Date:** 2026-04-19
**Status:** Approved design. Implementation pending.

## 1. Overview

`claude-conductor` lets a user give a single high-level goal to a *master* Claude Code session, which then delegates work to one or more *slave* Claude Code sessions running in sibling tmux windows. Master and slaves are all real `claude` TUI processes; the master drives slaves via a small Go CLI (`conductor`) invoked through its `Bash` tool. The loop continues autonomously until the master decides the goal is complete.

### Primary use case
> "I tell the master Claude a goal. The master writes prompts for slave Claudes, reads their results (especially any final questions), decides follow-up actions or yes/no answers, and repeats until the goal is done."

### Goals
- User interacts with a single interactive Claude session (the master).
- Delegation to multiple isolated Claude sessions (slaves) without leaving the terminal.
- Subscription-only auth (no Anthropic API key required).
- Minimal context overhead on the master (CLI tool, not MCP).
- Synchronous mental model: `conductor send` blocks until the slave finishes one turn.

### Non-goals (MVP)
- Parallel slave execution. Master's Bash tool calls are sequential, so only one slave works at a time. Parallelism may be added later via `dispatch`/`wait`.
- Controlling pre-existing Claude sessions the user started outside of conductor. Only conductor-spawned slaves are managed.
- Per-slave workspace isolation. All slaves share the master's cwd. Isolation (`git worktree` / temp dir) is a future opt-in.
- Permission delegation from slave to master. Slaves run with `--dangerously-skip-permissions`.
- CI automation.

## 2. Architecture

```
┌───────────────── tmux session (one per conductor invocation) ─────────────┐
│                                                                           │
│  Window 0 "master"              Window 1 "s1"  (default, pre-spawned)     │
│  ┌─────────────────────┐        ┌────────────────────────────────────┐    │
│  │  claude             │        │  run.sh → exec claude              │    │
│  │    --append-system  │        │    --dangerously-skip-permissions  │    │
│  │    --dangerously-…  │        │  (Stop / SessionStart hooks on)    │    │
│  │                     │        │                                    │    │
│  │  user ↔ master      │        │  slave executes tasks              │    │
│  │                     │        │                                    │    │
│  │  Bash tool:         │◀──┐    │                                    │    │
│  │    conductor send…  │   │    │                                    │    │
│  └─────────┬───────────┘   │    └─────────────────┬──────────────────┘    │
│            │               │                      │                       │
│            │ invokes       │ tmux send-keys       │ fires Stop hook       │
│            ▼               │ paste-buffer         ▼                       │
│  ┌────────────────────────────────────────────────────────────────┐       │
│  │  conductor (Go binary, stateless per invocation)               │       │
│  │    spawn | send | interrupt | reset | kill | list | last       │       │
│  │    _internal_stop_marker  (Stop hook handler)                  │       │
│  └────────────────────────────────────────────────────────────────┘       │
│                                                                           │
│  State: ~/.conductor/sessions/<tmux-session>/                             │
│         ├── session.json                                                  │
│         ├── master/SYSTEM.md                                              │
│         └── s<N>/                                                         │
└───────────────────────────────────────────────────────────────────────────┘
```

### Core flow
1. User runs `conductor` in a project directory.
2. Conductor creates a tmux session if not inside one, sets up state dir, launches master in Window 0 and a default slave `s1` in Window 1.
3. User types a goal into master Claude.
4. Master calls `conductor send s1 "<prompt>"` via `Bash`.
5. Conductor injects the prompt into Window 1 via tmux paste-buffer, blocks on the slave's `Stop` hook firing and writing `.done`, returns slave's final message on stdout.
6. Master reads output, decides next action (more `send`, `spawn` another slave, `interrupt`, or report to user).
7. Loop until master reports goal completion.

## 3. CLI Command Surface

All commands are run by master via the Bash tool. `$TMUX` is auto-detected; all commands fail with exit 10 outside a conductor-owned tmux session.

```bash
conductor                                    # launch: create tmux session + spawn master + s1
conductor spawn [--name s2]                  # new slave in a new tmux window
conductor send [--timeout 600] [--quiet] <id> "<prompt>"
conductor interrupt <id>                     # send Escape (cancel current turn)
conductor reset <id>                         # kill claude in window, restart fresh
conductor kill <id>                          # close window + clean state dir
conductor list                               # active slaves with status
conductor last <id>                          # print last assistant response (non-blocking)
conductor _internal_stop_marker <id>         # internal: Stop hook target
```

### Exit codes

| Code | Meaning |
|-----:|---------|
| 0    | Slave completed a turn normally |
| 2    | `send` timed out |
| 3    | Slave process crashed or window died |
| 4    | Unknown slave ID |
| 5    | Slave busy (`.pending` already exists) |
| 10   | Not inside a conductor tmux session |
| 99   | Conductor internal error |

### Flags
- `--timeout <seconds>` on `send`, default **600**.
- `--quiet` on `send`: suppress stderr progress.
- `--name <id>` on `spawn`: choose slave ID. Default is auto-increment (`s1`, `s2`, ...).

### Documented limitation
MVP is sequential-only (one slave's `send` completes before the next starts). Future: `dispatch` (non-blocking spawn/send) + `wait` to allow true parallelism.

## 4. Sync Blocking Internals

The single highest-risk subsystem.

### Slave launch
Each slave's tmux window runs a wrapper `run.sh`:

```bash
#!/usr/bin/env bash
trap 'echo $? > "$CONDUCTOR_SLAVE_DIR/.exit-code"' EXIT
cd "$CONDUCTOR_CWD"
exec claude --dangerously-skip-permissions
```

Conductor injects two Claude Code hooks for the slave (mechanism TBD — see Open Questions):
- **`SessionStart`** → `touch $CONDUCTOR_SLAVE_DIR/.ready` (signals Claude is ready to receive input)
- **`Stop`** → `conductor _internal_stop_marker $SLAVE_ID`

### `conductor spawn` flow
1. Allocate next slave ID (or use `--name`).
2. `mkdir -p $CONDUCTOR_SLAVE_DIR`, write `run.sh` and hook settings.
3. `tmux new-window -t <session> -n <id> <run.sh>`.
4. Block on `.ready` file via `fsnotify` until SessionStart hook fires. Timeout 30 seconds → exit 3.
5. Print slave ID to stdout.

### `conductor send` flow
```
1. Verify slave state dir exists (else exit 4).
2. Create .pending exclusively (else exit 5 — busy).
3. Remove stale .done if any.
4. Set up fsnotify watcher on state dir (BEFORE step 5 — avoid race).
5. Write prompt to a temp file; tmux load-buffer <file>; tmux paste-buffer -t <session>:<id>; tmux send-keys Enter.
6. Loop:
   - Wait for fsnotify event on .done (up to --timeout).
   - Every 1s: check `tmux list-panes ... pane_dead` for the slave's window. If dead → cleanup, exit 3.
7. On .done: read contents (last assistant text), delete .pending, print to stdout, exit 0.
8. On timeout: leave .pending (slave may still be working), stderr warning, exit 2.
```

### Stop hook handler (`_internal_stop_marker`)
Invoked by Claude Code with a JSON stdin containing `session_id` and `transcript_path`.
1. Read stdin JSON.
2. Read the transcript at `transcript_path`.
3. Walk entries in reverse; find first entry with `role: "assistant"` that contains at least one `type: "text"` content block.
4. Concatenate those text blocks' content into the `.done` file.
5. Append one-line summary to `transcript.log` (for debugging).
6. Exit 0.

### Rationale — why Stop hook + file watch vs alternatives

| Alternative | Rejected because |
|-------------|------------------|
| `tmux capture-pane` scraping | Terminal chrome, ANSI codes, unreliable idle detection |
| Tailing `~/.claude/projects/.../*.jsonl` directly | Undocumented path layout; flush timing unclear |
| Claude Agent SDK | Does not support subscription auth (Feb 2026 policy) |
| `Notification` hook | For permission prompts, not turn end |

Stop hook + file marker is officially supported, deterministic, and language-agnostic.

### Edge cases
- **User types into slave window manually** — Stop hook still fires; conductor treats the resulting `.done` as a normal result. Caller may see unexpected content.
- **Stop hook script fails** (PATH issue etc.) — `.done` never written → timeout → exit 2. CLI prints a hint on timeout about verifying the `conductor` binary is on PATH in the tmux env.
- **Two simultaneous `send` calls for same slave** — second hits `.pending` lock, exits 5 immediately.

## 5. Master Configuration & Startup

### `conductor` (no args) sequence

1. If `$TMUX` is empty → create a new tmux session named `conductor` and re-invoke `conductor` inside it. (Exact invocation — e.g., `tmux new-session -s conductor "$0"` — is an implementation detail.)
2. If already inside tmux → use current session.
3. Ensure `~/.conductor/sessions/<session>/` exists; write `session.json`.
4. Render `master/SYSTEM.md` from template.
5. Launch master in Window 0:
   ```bash
   tmux send-keys -t <session>:0 \
     "cd $CONDUCTOR_CWD && claude \
        --append-system-prompt \"\$(cat $MASTER_DIR/SYSTEM.md)\" \
        --dangerously-skip-permissions" Enter
   ```
6. Pre-spawn `s1` via internal `spawn` call.
7. Attach the user to Window 0.

### Master system prompt (`SYSTEM.md`, appended)

```markdown
You are the **master** in a claude-conductor session.

## Your role
- The user gives you a high-level GOAL.
- You do NOT perform the work yourself. You delegate to slave Claude sessions via the `conductor` CLI (available through Bash).
- Treat each slave as a fresh Claude. Prompts must be self-contained with all needed context.

## Available slaves
- `s1` is pre-spawned and ready.
- Spawn more with `conductor spawn` if work can be split safely (slaves share the cwd — avoid overlap).

## Workflow
1. Break the goal into delegatable chunks.
2. `conductor send <slave-id> "<full prompt>"` — blocks; stdout is the slave's final message.
3. Read output, decide:
   - Need more work → `conductor send` again (slave's context persists).
   - Slave asked a question → answer via `conductor send`.
   - Off-track → `conductor interrupt <id>` then re-prompt, or `conductor reset <id>` for clean slate.
4. Loop until the goal is complete. Report to the user.

## Hard rules
- Do not use Read/Edit/Write/Grep/Glob on project files yourself unless the user speaks directly to you. That is the slaves' job.
- Do not ask the user "what should I do next?" unless the goal is genuinely ambiguous.
- Surface slave errors transparently.

## `conductor` CLI
(refer to section 3 of the design doc)
```

### Why `--append-system-prompt` and not a project `CLAUDE.md`?
- Does not touch the user's real project files.
- Layered over any existing `CLAUDE.md` the project has.
- Scoped to this invocation.

## 6. State Storage

```
~/.conductor/
└── sessions/
    └── <tmux-session-name>/
        ├── session.json          # cwd, start time, active slave list
        ├── audit.log             # NDJSON of every conductor invocation
        ├── master/
        │   └── SYSTEM.md
        └── s<N>/
            ├── run.sh
            ├── settings.json     # Stop / SessionStart hooks
            ├── .ready            # created by SessionStart hook
            ├── .pending          # created by `send`, deleted on completion
            ├── .done             # created by Stop hook (content = last assistant text)
            ├── .exit-code        # written by run.sh trap on slave exit
            └── transcript.log    # human-readable history (appended by Stop hook)
```

- State is **not** deleted on tmux session end. Preserved for debugging.
- `conductor clean` (TODO for later) purges dead sessions.

## 7. Error Handling Matrix

| Situation | Detection | Action | Exit |
|-----------|-----------|--------|-----:|
| `send` timeout | fsnotify + timer | Keep `.pending`, stderr hint | 2 |
| Slave crash mid-send | `tmux list-panes` pane_dead | Remove `.pending`, stderr | 3 |
| Unknown slave ID | State dir absent | stderr | 4 |
| Slave busy | `.pending` exists | Fail fast | 5 |
| Not inside conductor tmux | `$TMUX` check | Hint: run `conductor` first | 10 |
| Stop hook never fires | Manifests as timeout | Hint about PATH in tmux env | 2 |
| Internal panic | Go `recover()` | stderr + stack | 99 |

All invocations append one line to `audit.log`:
```json
{"ts":"2026-04-19T10:00:00Z","cmd":"send","args":["s1","..."],"duration_ms":4120,"exit":0}
```

## 8. Testing Strategy

### Unit tests only (Go `testing`)
- `transcript` package: fixture JSONL → expected extracted text.
- `state` package: tmpdir round-trip of `.pending`/`.done`/`.exit-code` transitions.
- `tmux` package: command string builders (no actual tmux invocation).
- `cli` package: argument parsing, exit code mapping.

### Not tested
- Live integration with Claude or tmux. Non-determinism of Claude responses makes assertions brittle. Covered by manual smoke tests.

### Manual smoke test checklist (`docs/manual-smoketest.md`)
- [ ] `conductor` launches tmux session with two windows.
- [ ] Master receives a simple goal and dispatches to `s1`.
- [ ] Slave's final response is returned from `conductor send`.
- [ ] `conductor interrupt` stops a running slave.
- [ ] `conductor reset` restores a slave to a clean state.
- [ ] Multi-turn: master sends follow-up; slave context persists.
- [ ] `conductor kill s1` cleans the window and state dir.

### CI
- Local only. `go test ./...` by developer before commit/push. No GitHub Actions.

## 9. Open Questions

Research these during implementation; may alter specific mechanisms without changing overall design.

1. **Hook injection mechanism for slaves.** `CLAUDE_CONFIG_DIR` was speculated but not verified. Candidates:
   - `CLAUDE_CONFIG_DIR` env var (if it redirects everything).
   - `.claude/settings.local.json` inside the slave's cwd (but master shares cwd — would collide).
   - `--settings` / `--mcp-config` flags if they exist.
   - A per-slave wrapper cwd containing only `.claude/settings.json`.
2. **Claude TUI input commit semantics.** Does `paste-buffer` followed by `send-keys Enter` reliably submit a multi-line prompt? If not, may need per-line `send-keys` or a bracketed-paste wrapper.
3. **Escape single-press vs double-press.** Single Escape cancels the current turn. Double-Escape or `/clear` behaviors should be documented.
4. **`--dangerously-skip-permissions` coverage.** Does it auto-allow all MCP tools too? MVP slaves will not load MCP servers, so likely moot, but confirm.

## 10. Future Extensions (post-MVP)

- `conductor dispatch <id> "<prompt>"` + `conductor wait <id...>` for true parallel slave work.
- `conductor spawn --isolate` for per-slave `git worktree` or temp-dir cwd.
- `conductor budget` subcommand to summarize token usage from transcripts.
- `conductor clean` to purge dead session state dirs.
- Slave → master permission escalation for cases where `--dangerously-skip-permissions` is unacceptable (hook-based prompt forwarding).
