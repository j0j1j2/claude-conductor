# claude-conductor — Manual smoke test

Run after any change to tmux interaction, hook rendering, or `send` loop logic.
Requires: `claude` (logged in), `tmux`, `go`, and this repo built via `go install ./...`.

## Pre-flight

- [ ] `which conductor` → resolves to the binary you just built
- [ ] `echo $TMUX` → empty (start outside tmux for a full end-to-end test)

## 1. Launch

- [ ] Run `cd ~/scratch && conductor`
- [ ] Expect: a new tmux session named `conductor` attaches. Window 0 shows master claude starting. Window 1 shows slave `s1` starting.
- [ ] In the master window, see that Claude's system prompt includes the conductor role (ask master "what's your role?" — it should explain the master/slave setup).

## 2. Basic send

- [ ] In master window, prompt: "append the line 'hello from conductor' to a file called TOUCH in cwd"
- [ ] Expect: master calls Bash: `conductor send s1 "..."`, blocks, prints a short completion message.
- [ ] Verify: `TOUCH` file created in cwd with the expected content.

## 3. Multi-turn slave context

- [ ] In master window: "ask s1 to recall the exact content it wrote"
- [ ] Expect: master calls `conductor send s1 "what did you write?"`, s1 recalls correctly (context persisted).

## 4. Interrupt

- [ ] Prompt master: "have s1 count to 1000 one number per line, slowly"
- [ ] Before s1 finishes, in a second pane: `conductor interrupt s1`
- [ ] Expect: s1's current turn cancels. `conductor send s1 "..."` works again afterwards.

## 5. Reset

- [ ] `conductor reset s1`
- [ ] Expect: after a few seconds, s1 is back with no prior context.

## 6. Spawn additional slave

- [ ] In master: "spawn s2 and ask it to list files in cwd"
- [ ] Expect: Window 2 appears with s2. Master gets s2's output.

## 7. Kill and cleanup

- [ ] `conductor kill s2` → window 2 closes, state dir removed
- [ ] `conductor list` → only shows s1 (and/or remaining slaves)

## 8. Crash recovery

- [ ] In s1's window, type `exit` manually to kill the process
- [ ] In master: `conductor send s1 "hi"`
- [ ] Expect: exits with code 3 (slave crashed). `conductor reset s1` recovers.

If any step fails, file an issue referencing the spec: `docs/superpowers/specs/2026-04-19-claude-conductor-design.md`.
