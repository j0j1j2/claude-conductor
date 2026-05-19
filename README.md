# claude-conductor

A master Claude Code session delegates to slave Claude Code sessions via a Go CLI, inside a shared tmux session.

## What it does

You give a high-level goal to the **master** Claude. The master writes prompts for **slaves** running in sibling tmux windows, reads their outputs, and decides follow-ups — all autonomously — until the goal is done.

## Requirements

- tmux
- `claude` (Claude Code CLI, logged in with a Pro/Max subscription)
- Go 1.22+ (to build)

## Install

One-liner (requires Go 1.22+):

```bash
go install github.com/j0j1j2/claude-conductor/cmd/conductor@latest
```

This drops a `conductor` binary into `$(go env GOPATH)/bin` — make sure that directory is on your `PATH`. Verify:

```bash
conductor --help
```

From source:

```bash
git clone https://github.com/j0j1j2/claude-conductor
cd claude-conductor
go install ./cmd/conductor
```

## Usage

```bash
cd your-project
conductor
```

A tmux session opens with:
- Window 0: master Claude (you interact here)
- Window 1: slave `s1` ready to receive delegated prompts

Give the master your goal in plain language.

## Subcommands (used by the master, not you directly)

```
conductor spawn [--name sN]                 # new slave
conductor send [--timeout S] [--quiet] <id> <msg>
                                            # send prompt, block on completion
conductor interrupt <id>                    # cancel current turn (Escape)
conductor reset <id> [--force]              # fresh slave in same window
conductor kill <id> [--force]               # close window + clean up
conductor list                              # active slaves with status
conductor last <id>                         # print slave's last response (empty if none)
conductor doctor <id>                       # full diagnostic report
conductor unstick <id> [--force]            # clear a stale .pending lock
```

`reset`, `kill`, and `unstick` refuse to act on a slave with a live `.pending`
unless `--force` is passed.

## Design

See [`docs/superpowers/specs/2026-04-19-claude-conductor-design.md`](docs/superpowers/specs/2026-04-19-claude-conductor-design.md) for the full design.

## Development

```bash
go test ./...        # unit tests
go build ./...       # build
```

Manual smoke test: [`docs/manual-smoketest.md`](docs/manual-smoketest.md).

## License

MIT
