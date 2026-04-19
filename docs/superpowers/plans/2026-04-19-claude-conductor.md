# claude-conductor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI `conductor` that lets a master Claude Code session delegate work to slave Claude Code sessions running in sibling tmux windows, with sync-blocking `send` via Stop-hook file markers.

**Architecture:** Stateless Go binary (`conductor`) invoked per command. Spawns a tmux session with window 0 (master) and window 1 (slave `s1`). Slaves run with `--dangerously-skip-permissions`; master drives them via `Bash: conductor <subcmd>`. Per-slave state dir under `~/.conductor/sessions/<session>/s<N>/` with `.pending` / `.done` / `.exit-code` markers. `send` uses `fsnotify` on `.done`.

**Tech Stack:** Go 1.22+, `github.com/spf13/cobra` (CLI framework), `github.com/fsnotify/fsnotify` (file watching), standard library for tmux (`os/exec`), json, io.

**Reference spec:** `docs/superpowers/specs/2026-04-19-claude-conductor-design.md`

---

## File Structure

```
claude-conductor/
├── go.mod
├── go.sum
├── main.go                         # cobra root, dispatches to cmd/
├── cmd/
│   ├── root.go                     # `conductor` (no args) — bootstrap tmux + master + s1
│   ├── spawn.go
│   ├── send.go
│   ├── interrupt.go
│   ├── reset.go
│   ├── kill.go
│   ├── list.go
│   ├── last.go
│   └── internal_stop_marker.go     # `_internal_stop_marker`
├── internal/
│   ├── exitcode/
│   │   └── exitcode.go             # constants: OK=0, Timeout=2, …
│   ├── transcript/
│   │   ├── transcript.go           # LastAssistantText(path) (string, error)
│   │   └── transcript_test.go
│   ├── state/
│   │   ├── state.go                # SessionDir, SlaveDir, pending/done ops
│   │   └── state_test.go
│   ├── tmux/
│   │   ├── tmux.go                 # command builders + runner
│   │   └── tmux_test.go
│   ├── hooks/
│   │   ├── hooks.go                # settings.json + run.sh generators
│   │   └── hooks_test.go
│   └── audit/
│       └── audit.go                # NDJSON appender
├── testdata/
│   ├── transcript_basic.jsonl
│   └── transcript_tools_only.jsonl
├── docs/
│   ├── manual-smoketest.md
│   └── superpowers/
│       ├── specs/2026-04-19-claude-conductor-design.md
│       └── plans/2026-04-19-claude-conductor.md
└── README.md
```

**Rationale:**
- `cmd/` holds thin subcommand wiring; all logic lives in `internal/`.
- Each `internal/` package has a single responsibility (transcript parsing, state I/O, tmux building, hook rendering, audit).
- No cross-imports between `internal/` packages except through narrow APIs — keep each file small and holdable in context.

---

## Task 1: Initialize Go project with Cobra skeleton

**Files:**
- Create: `go.mod`, `main.go`, `cmd/root.go`, `internal/exitcode/exitcode.go`
- Create: `internal/exitcode/exitcode_test.go`

- [ ] **Step 1: Init module and add deps**

```bash
cd /Users/cloudchamb3r/projects/claude-conductor
go mod init github.com/cloudchamb3r/claude-conductor
go get github.com/spf13/cobra@latest
go get github.com/fsnotify/fsnotify@latest
```

Expected: `go.mod` and `go.sum` appear.

- [ ] **Step 2: Write the failing exit code test**

Create `internal/exitcode/exitcode_test.go`:

```go
package exitcode

import "testing"

func TestConstants(t *testing.T) {
    cases := map[string]struct {
        got  int
        want int
    }{
        "OK":              {OK, 0},
        "Timeout":         {Timeout, 2},
        "Crash":           {Crash, 3},
        "UnknownSlave":    {UnknownSlave, 4},
        "Busy":            {Busy, 5},
        "NotInTmux":       {NotInTmux, 10},
        "InternalError":   {InternalError, 99},
    }
    for name, c := range cases {
        if c.got != c.want {
            t.Errorf("%s = %d, want %d", name, c.got, c.want)
        }
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/exitcode/...`
Expected: FAIL with `undefined: OK` etc.

- [ ] **Step 4: Implement exit codes**

Create `internal/exitcode/exitcode.go`:

```go
// Package exitcode defines conductor CLI exit codes.
package exitcode

const (
    OK            = 0
    Timeout       = 2
    Crash         = 3
    UnknownSlave  = 4
    Busy          = 5
    NotInTmux     = 10
    InternalError = 99
)
```

- [ ] **Step 5: Create cobra root with ExitError + main**

Create `cmd/root.go`:

```go
package cmd

import (
    "fmt"

    "github.com/spf13/cobra"
)

var Root = &cobra.Command{
    Use:   "conductor",
    Short: "Master-slave orchestrator for Claude Code sessions in tmux",
    // RunE is populated in root bootstrap task (Task 16).
}

// ExitError carries a CLI exit code up to main.go.
type ExitError struct {
    Code int
    Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// CLIError returns an ExitError with a formatted message.
func CLIError(code int, format string, a ...any) error {
    return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}
```

Create `main.go`:

```go
package main

import (
    "errors"
    "fmt"
    "os"

    "github.com/cloudchamb3r/claude-conductor/cmd"
    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
)

func main() {
    err := cmd.Root.Execute()
    if err == nil {
        return
    }
    fmt.Fprintln(os.Stderr, err)
    var ee *cmd.ExitError
    if errors.As(err, &ee) {
        os.Exit(ee.Code)
    }
    os.Exit(exitcode.InternalError)
}
```

- [ ] **Step 6: Verify builds and tests pass**

Run: `go build ./... && go test ./...`
Expected: build succeeds, `exitcode` test PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum main.go cmd/root.go internal/exitcode/
git commit -m "chore: scaffold Go project with cobra skeleton and exit codes"
```

---

## Task 2: Transcript parser — LastAssistantText

**Files:**
- Create: `internal/transcript/transcript.go`
- Create: `internal/transcript/transcript_test.go`
- Create: `testdata/transcript_basic.jsonl`
- Create: `testdata/transcript_tools_only.jsonl`
- Create: `testdata/transcript_empty.jsonl`

- [ ] **Step 1: Create test fixtures**

Create `testdata/transcript_basic.jsonl` (content written as literal lines):

```jsonl
{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello there."}]}}
{"type":"user","message":{"role":"user","content":"2+2?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Calc","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"4"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"The answer is 4."}]}}
```

Create `testdata/transcript_tools_only.jsonl`:

```jsonl
{"type":"user","message":{"role":"user","content":"run something"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"cmd":"ls"}}]}}
```

Create `testdata/transcript_empty.jsonl` as an empty file.

- [ ] **Step 2: Write failing tests**

Create `internal/transcript/transcript_test.go`:

```go
package transcript

import (
    "os"
    "path/filepath"
    "testing"
)

func fixturePath(t *testing.T, name string) string {
    t.Helper()
    wd, err := os.Getwd()
    if err != nil {
        t.Fatal(err)
    }
    return filepath.Join(wd, "..", "..", "testdata", name)
}

func TestLastAssistantText_basic(t *testing.T) {
    got, err := LastAssistantText(fixturePath(t, "transcript_basic.jsonl"))
    if err != nil {
        t.Fatal(err)
    }
    if got != "The answer is 4." {
        t.Errorf("got %q, want %q", got, "The answer is 4.")
    }
}

func TestLastAssistantText_toolsOnly(t *testing.T) {
    _, err := LastAssistantText(fixturePath(t, "transcript_tools_only.jsonl"))
    if err != ErrNoAssistantText {
        t.Errorf("err = %v, want ErrNoAssistantText", err)
    }
}

func TestLastAssistantText_empty(t *testing.T) {
    _, err := LastAssistantText(fixturePath(t, "transcript_empty.jsonl"))
    if err != ErrNoAssistantText {
        t.Errorf("err = %v, want ErrNoAssistantText", err)
    }
}

func TestLastAssistantText_missingFile(t *testing.T) {
    _, err := LastAssistantText("/nonexistent/path.jsonl")
    if err == nil {
        t.Error("expected error for missing file")
    }
}
```

- [ ] **Step 3: Run tests — verify they fail**

Run: `go test ./internal/transcript/...`
Expected: FAIL with `undefined: LastAssistantText`.

- [ ] **Step 4: Implement the parser**

Create `internal/transcript/transcript.go`:

```go
// Package transcript parses Claude Code transcript JSONL files.
package transcript

import (
    "bufio"
    "encoding/json"
    "errors"
    "os"
    "strings"
)

// ErrNoAssistantText is returned when the transcript contains no assistant
// message with a text block.
var ErrNoAssistantText = errors.New("transcript: no assistant text block found")

type entry struct {
    Type    string `json:"type"`
    Message struct {
        Role    string          `json:"role"`
        Content json.RawMessage `json:"content"`
    } `json:"message"`
}

type contentBlock struct {
    Type string `json:"type"`
    Text string `json:"text"`
}

// LastAssistantText returns the concatenated text content of the last assistant
// message in the transcript that contains at least one "text" block. Tool-only
// assistant turns are skipped.
func LastAssistantText(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer f.Close()

    var last string
    scanner := bufio.NewScanner(f)
    scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max line

    for scanner.Scan() {
        line := scanner.Bytes()
        if len(line) == 0 {
            continue
        }
        var e entry
        if err := json.Unmarshal(line, &e); err != nil {
            continue // tolerate malformed lines
        }
        if e.Type != "assistant" && e.Message.Role != "assistant" {
            continue
        }
        text := extractText(e.Message.Content)
        if text != "" {
            last = text
        }
    }
    if err := scanner.Err(); err != nil {
        return "", err
    }
    if last == "" {
        return "", ErrNoAssistantText
    }
    return last, nil
}

func extractText(raw json.RawMessage) string {
    // content may be a string or an array of blocks
    var asString string
    if err := json.Unmarshal(raw, &asString); err == nil {
        return asString
    }
    var blocks []contentBlock
    if err := json.Unmarshal(raw, &blocks); err != nil {
        return ""
    }
    var parts []string
    for _, b := range blocks {
        if b.Type == "text" && b.Text != "" {
            parts = append(parts, b.Text)
        }
    }
    return strings.Join(parts, "")
}
```

- [ ] **Step 5: Run tests — verify they pass**

Run: `go test ./internal/transcript/...`
Expected: all four tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/transcript/ testdata/
git commit -m "feat(transcript): parse Claude JSONL to extract last assistant text"
```

---

## Task 3: State package — directories + pending/done markers

**Files:**
- Create: `internal/state/state.go`
- Create: `internal/state/state_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/state/state_test.go`:

```go
package state

import (
    "os"
    "path/filepath"
    "testing"
)

func tempHome(t *testing.T) string {
    t.Helper()
    dir := t.TempDir()
    t.Setenv("HOME", dir)
    return dir
}

func TestSessionDir(t *testing.T) {
    home := tempHome(t)
    got := SessionDir("foo")
    want := filepath.Join(home, ".conductor", "sessions", "foo")
    if got != want {
        t.Errorf("got %q, want %q", got, want)
    }
}

func TestSlaveDir(t *testing.T) {
    home := tempHome(t)
    got := SlaveDir("foo", "s1")
    want := filepath.Join(home, ".conductor", "sessions", "foo", "s1")
    if got != want {
        t.Errorf("got %q, want %q", got, want)
    }
}

func TestCreatePending_exclusive(t *testing.T) {
    tempHome(t)
    slaveDir := SlaveDir("s", "s1")
    if err := os.MkdirAll(slaveDir, 0o755); err != nil {
        t.Fatal(err)
    }

    if err := CreatePending(slaveDir); err != nil {
        t.Fatalf("first CreatePending failed: %v", err)
    }
    if err := CreatePending(slaveDir); err == nil {
        t.Fatal("second CreatePending should fail (busy)")
    }

    if err := RemovePending(slaveDir); err != nil {
        t.Fatalf("RemovePending failed: %v", err)
    }
    if err := CreatePending(slaveDir); err != nil {
        t.Fatalf("CreatePending after remove failed: %v", err)
    }
}

func TestWriteReadDone(t *testing.T) {
    tempHome(t)
    slaveDir := SlaveDir("s", "s1")
    if err := os.MkdirAll(slaveDir, 0o755); err != nil {
        t.Fatal(err)
    }
    if err := WriteDone(slaveDir, "hello"); err != nil {
        t.Fatal(err)
    }
    got, err := ReadDone(slaveDir)
    if err != nil {
        t.Fatal(err)
    }
    if got != "hello" {
        t.Errorf("got %q, want %q", got, "hello")
    }
}

func TestSlaveExists(t *testing.T) {
    tempHome(t)
    if SlaveExists("s", "s1") {
        t.Error("should not exist yet")
    }
    if err := os.MkdirAll(SlaveDir("s", "s1"), 0o755); err != nil {
        t.Fatal(err)
    }
    if !SlaveExists("s", "s1") {
        t.Error("should exist now")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/state/...`
Expected: FAIL with undefined symbols.

- [ ] **Step 3: Implement state package**

Create `internal/state/state.go`:

```go
// Package state manages ~/.conductor directory layout and slave state markers.
package state

import (
    "errors"
    "os"
    "path/filepath"
)

// ErrBusy means a .pending file already exists for the slave.
var ErrBusy = errors.New("state: slave is busy")

// RootDir returns ~/.conductor.
func RootDir() string {
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".conductor")
}

// SessionDir returns ~/.conductor/sessions/<session>.
func SessionDir(session string) string {
    return filepath.Join(RootDir(), "sessions", session)
}

// SlaveDir returns ~/.conductor/sessions/<session>/<id>.
func SlaveDir(session, id string) string {
    return filepath.Join(SessionDir(session), id)
}

// SlaveExists reports whether the slave's state dir exists.
func SlaveExists(session, id string) bool {
    _, err := os.Stat(SlaveDir(session, id))
    return err == nil
}

// CreatePending creates the `.pending` file exclusively. Returns ErrBusy if
// the file already exists.
func CreatePending(slaveDir string) error {
    f, err := os.OpenFile(filepath.Join(slaveDir, ".pending"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
    if err != nil {
        if os.IsExist(err) {
            return ErrBusy
        }
        return err
    }
    return f.Close()
}

// RemovePending removes the `.pending` file. No error if it does not exist.
func RemovePending(slaveDir string) error {
    err := os.Remove(filepath.Join(slaveDir, ".pending"))
    if err != nil && !os.IsNotExist(err) {
        return err
    }
    return nil
}

// RemoveDone removes the stale `.done` file.
func RemoveDone(slaveDir string) error {
    err := os.Remove(filepath.Join(slaveDir, ".done"))
    if err != nil && !os.IsNotExist(err) {
        return err
    }
    return nil
}

// WriteDone atomically writes the completion message to `.done`.
func WriteDone(slaveDir, content string) error {
    tmp := filepath.Join(slaveDir, ".done.tmp")
    if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
        return err
    }
    return os.Rename(tmp, filepath.Join(slaveDir, ".done"))
}

// ReadDone reads the `.done` file.
func ReadDone(slaveDir string) (string, error) {
    b, err := os.ReadFile(filepath.Join(slaveDir, ".done"))
    if err != nil {
        return "", err
    }
    return string(b), nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/state/...`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/state/
git commit -m "feat(state): add session/slave dir layout and pending/done markers"
```

---

## Task 4: Tmux command builders

**Files:**
- Create: `internal/tmux/tmux.go`
- Create: `internal/tmux/tmux_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/tmux/tmux_test.go`:

```go
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
    if !strings.Contains(args, "new-window -t sess -n s2 /path/to/run.sh") {
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

func TestCurrentSession_fromEnv(t *testing.T) {
    // $TMUX format: /tmp/tmux-501/default,12345,0
    // We only use the socket path piece; session name comes from #S.
    t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
    if !InTmux() {
        t.Error("expected InTmux() = true")
    }

    t.Setenv("TMUX", "")
    if InTmux() {
        t.Error("expected InTmux() = false")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tmux/...`
Expected: FAIL.

- [ ] **Step 3: Implement tmux package**

Create `internal/tmux/tmux.go`:

```go
// Package tmux builds (but does not execute) tmux commands.
package tmux

import (
    "os"
    "os/exec"
)

// InTmux reports whether the process is running inside a tmux session.
func InTmux() bool {
    return os.Getenv("TMUX") != ""
}

// SendKeysCmd builds `tmux send-keys -t <session>:<window> <keys>...`.
func SendKeysCmd(session, window string, keys ...string) *exec.Cmd {
    args := []string{"send-keys", "-t", session + ":" + window}
    args = append(args, keys...)
    return exec.Command("tmux", args...)
}

// LoadBufferCmd builds `tmux load-buffer <file>`.
func LoadBufferCmd(path string) *exec.Cmd {
    return exec.Command("tmux", "load-buffer", path)
}

// PasteBufferCmd builds `tmux paste-buffer -t <session>:<window>`.
func PasteBufferCmd(session, window string) *exec.Cmd {
    return exec.Command("tmux", "paste-buffer", "-t", session+":"+window)
}

// NewWindowCmd builds `tmux new-window -t <session> -n <name> <cmd>`.
func NewWindowCmd(session, name, command string) *exec.Cmd {
    return exec.Command("tmux", "new-window", "-t", session, "-n", name, command)
}

// KillWindowCmd builds `tmux kill-window -t <session>:<window>`.
func KillWindowCmd(session, window string) *exec.Cmd {
    return exec.Command("tmux", "kill-window", "-t", session+":"+window)
}

// HasSessionCmd builds `tmux has-session -t <session>`.
func HasSessionCmd(session string) *exec.Cmd {
    return exec.Command("tmux", "has-session", "-t", session)
}

// NewSessionCmd builds `tmux new-session -d -s <session> <command>`.
func NewSessionCmd(session, command string) *exec.Cmd {
    return exec.Command("tmux", "new-session", "-d", "-s", session, command)
}

// AttachSessionCmd builds `tmux attach-session -t <session>`.
func AttachSessionCmd(session string) *exec.Cmd {
    return exec.Command("tmux", "attach-session", "-t", session)
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/tmux/...`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/
git commit -m "feat(tmux): add command builders for session, window, keys, buffer"
```

---

## Task 5: Tmux runtime helpers — PaneDead, CurrentSession

**Files:**
- Modify: `internal/tmux/tmux.go`
- Modify: `internal/tmux/tmux_test.go`

- [ ] **Step 1: Add failing test for pane parsing**

Append to `internal/tmux/tmux_test.go`:

```go
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

func TestCurrentSessionName(t *testing.T) {
    // CurrentSession shells out to `tmux display-message -p '#S'`.
    // We test the parser only.
    got := parseSessionName("conductor\n")
    if got != "conductor" {
        t.Errorf("got %q, want %q", got, "conductor")
    }
    got = parseSessionName("  my-sess  \n")
    if got != "my-sess" {
        t.Errorf("got %q, want %q", got, "my-sess")
    }
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/tmux/...`
Expected: FAIL (undefined `parsePaneDead`, `parseSessionName`).

- [ ] **Step 3: Implement helpers**

Append to `internal/tmux/tmux.go`:

```go
import (
    "os/exec"
    "strings"
)

// CurrentSession returns the tmux session name the process is attached to.
// Caller must ensure InTmux() is true.
func CurrentSession() (string, error) {
    out, err := exec.Command("tmux", "display-message", "-p", "#S").Output()
    if err != nil {
        return "", err
    }
    return parseSessionName(string(out)), nil
}

func parseSessionName(s string) string {
    return strings.TrimSpace(s)
}

// PaneDead reports whether the given window's first pane is marked dead.
func PaneDead(session, window string) (bool, error) {
    out, err := exec.Command("tmux", "list-panes",
        "-t", session+":"+window, "-F", "#{pane_dead}").Output()
    if err != nil {
        return false, err
    }
    return parsePaneDead(string(out)), nil
}

func parsePaneDead(s string) bool {
    return strings.TrimSpace(s) == "1"
}
```

Note: the existing `tmux.go` already has `import ( "os"; "os/exec" )`. Merge the `strings` import into that block.

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/tmux/...`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/
git commit -m "feat(tmux): add CurrentSession and PaneDead with parse helpers"
```

---

## Task 6: Hook settings and run.sh generators

**Files:**
- Create: `internal/hooks/hooks.go`
- Create: `internal/hooks/hooks_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/hooks/hooks_test.go`:

```go
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
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./internal/hooks/...`
Expected: FAIL.

- [ ] **Step 3: Implement hooks package**

Create `internal/hooks/hooks.go`:

```go
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
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./internal/hooks/...`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/
git commit -m "feat(hooks): render slave settings.json and run.sh wrapper"
```

---

## Task 7: Audit log writer

**Files:**
- Create: `internal/audit/audit.go`
- Create: `internal/audit/audit_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/audit/audit_test.go`:

```go
package audit

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestAppend_writesNDJSON(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "audit.log")

    if err := Append(path, Entry{
        Timestamp: time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC),
        Cmd:       "send",
        Args:      []string{"s1", "hello"},
        DurationMS: 123,
        Exit:      0,
    }); err != nil {
        t.Fatal(err)
    }

    // Second append — verify newline-delimited.
    if err := Append(path, Entry{
        Timestamp:  time.Date(2026, 4, 19, 10, 0, 1, 0, time.UTC),
        Cmd:        "list",
        Args:       []string{},
        DurationMS: 5,
        Exit:       0,
    }); err != nil {
        t.Fatal(err)
    }

    b, err := os.ReadFile(path)
    if err != nil {
        t.Fatal(err)
    }
    lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
    if len(lines) != 2 {
        t.Fatalf("got %d lines, want 2", len(lines))
    }
    var got Entry
    if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }
    if got.Cmd != "send" {
        t.Errorf("got cmd %q", got.Cmd)
    }
}
```

- [ ] **Step 2: Run test — verify it fails**

Run: `go test ./internal/audit/...`
Expected: FAIL.

- [ ] **Step 3: Implement audit package**

Create `internal/audit/audit.go`:

```go
// Package audit appends NDJSON audit entries to a log file.
package audit

import (
    "encoding/json"
    "os"
    "time"
)

// Entry is one audited conductor invocation.
type Entry struct {
    Timestamp  time.Time `json:"ts"`
    Cmd        string    `json:"cmd"`
    Args       []string  `json:"args"`
    DurationMS int64     `json:"duration_ms"`
    Exit       int       `json:"exit"`
}

// Append writes one NDJSON line to path, creating the file if needed.
// Multiple concurrent writers are safe at the OS level for small writes
// (atomic under typical filesystems up to PIPE_BUF).
func Append(path string, e Entry) error {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
    if err != nil {
        return err
    }
    defer f.Close()
    b, err := json.Marshal(e)
    if err != nil {
        return err
    }
    b = append(b, '\n')
    _, err = f.Write(b)
    return err
}
```

- [ ] **Step 4: Run test — verify it passes**

Run: `go test ./internal/audit/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/audit/
git commit -m "feat(audit): NDJSON audit log appender"
```

---

## Task 7.5: Wire audit logging around every subcommand

**Files:**
- Modify: `cmd/root.go`

Every CLI invocation should append an entry to `~/.conductor/sessions/<session>/audit.log`. Hook this centrally via cobra's `PersistentPreRunE` / `PersistentPostRunE` so individual subcommands stay simple.

- [ ] **Step 1: Wire audit into root**

Replace `cmd/root.go` with:

```go
package cmd

import (
    "fmt"
    "path/filepath"
    "time"

    "github.com/cloudchamb3r/claude-conductor/internal/audit"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

var cmdStart time.Time

var Root = &cobra.Command{
    Use:   "conductor",
    Short: "Master-slave orchestrator for Claude Code sessions in tmux",
    // RunE is wired in Task 16 (bootstrap).
    PersistentPreRun: func(cmd *cobra.Command, args []string) {
        cmdStart = time.Now()
    },
}

// writeAudit is called by main.go after Execute() so it can observe the
// final exit code regardless of whether the command returned an error.
func writeAudit(cmd *cobra.Command, args []string, exit int) {
    if !tmux.InTmux() {
        return // no session to attribute the entry to
    }
    sess, err := tmux.CurrentSession()
    if err != nil {
        return
    }
    path := filepath.Join(state.SessionDir(sess), "audit.log")
    _ = audit.Append(path, audit.Entry{
        Timestamp:  time.Now().UTC(),
        Cmd:        cmd.Name(),
        Args:       args,
        DurationMS: time.Since(cmdStart).Milliseconds(),
        Exit:       exit,
    })
}

// ExitError carries a CLI exit code up to main.go.
type ExitError struct {
    Code int
    Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// CLIError returns an ExitError with a formatted message.
func CLIError(code int, format string, a ...any) error {
    return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}
```

- [ ] **Step 2: Add exported helpers in cmd/root.go**

Append to `cmd/root.go`:

```go
// ExecuteRoot runs the root command and returns the actual executed subcommand
// (so main can audit it).
func ExecuteRoot() (*cobra.Command, error) {
    return Root.ExecuteC()
}

// WriteAudit is exposed for main.go to call after Execute().
func WriteAudit(c *cobra.Command, args []string, exit int) {
    writeAudit(c, args, exit)
}
```

- [ ] **Step 3: Replace main.go to call WriteAudit**

Replace `main.go` with:

```go
package main

import (
    "errors"
    "fmt"
    "os"

    "github.com/cloudchamb3r/claude-conductor/cmd"
    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
)

func main() {
    executed, execErr := cmd.ExecuteRoot()
    exit := 0
    if execErr != nil {
        fmt.Fprintln(os.Stderr, execErr)
        var ee *cmd.ExitError
        if errors.As(execErr, &ee) {
            exit = ee.Code
        } else {
            exit = exitcode.InternalError
        }
    }
    if executed != nil {
        cmd.WriteAudit(executed, os.Args[1:], exit)
    }
    os.Exit(exit)
}
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 5: Commit**

```bash
git add cmd/root.go main.go
git commit -m "feat(audit): wire NDJSON audit logging around every conductor invocation"
```

---

## Task 8: `_internal_stop_marker` subcommand

**Files:**
- Create: `cmd/internal_stop_marker.go`
- Create: `cmd/internal_session_ready.go`

This subcommand is invoked by the slave's Stop hook. It reads the hook's stdin JSON (`{"session_id":..., "transcript_path":...}`), extracts the last assistant text from the transcript, and writes it to the slave's `.done` file.

- [ ] **Step 1: Write the subcommand**

Create `cmd/internal_stop_marker.go`:

```go
package cmd

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "time"

    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/transcript"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

type stopHookStdin struct {
    SessionID      string `json:"session_id"`
    TranscriptPath string `json:"transcript_path"`
}

var internalStopMarkerCmd = &cobra.Command{
    Use:    "_internal_stop_marker <slave-id>",
    Short:  "Internal: Stop-hook target; writes .done from the transcript",
    Args:   cobra.ExactArgs(1),
    Hidden: true,
    RunE: func(cmd *cobra.Command, args []string) error {
        slaveID := args[0]

        raw, err := io.ReadAll(os.Stdin)
        if err != nil {
            return fmt.Errorf("read stdin: %w", err)
        }
        var hookIn stopHookStdin
        if err := json.Unmarshal(raw, &hookIn); err != nil {
            return fmt.Errorf("parse hook stdin: %w", err)
        }

        sess, err := tmux.CurrentSession()
        if err != nil {
            return fmt.Errorf("current tmux session: %w", err)
        }

        text, err := transcript.LastAssistantText(hookIn.TranscriptPath)
        if err != nil {
            // Still produce a .done so `send` unblocks with an obvious marker.
            text = "(no assistant text in transcript)"
        }

        slaveDir := state.SlaveDir(sess, slaveID)
        if err := state.WriteDone(slaveDir, text); err != nil {
            return err
        }
        // Append one-line summary to transcript.log for debugging.
        summary := fmt.Sprintf("%s | turn ended | %d bytes | session=%s\n",
            time.Now().UTC().Format(time.RFC3339), len(text), hookIn.SessionID)
        f, err := os.OpenFile(filepath.Join(slaveDir, "transcript.log"),
            os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
        if err == nil {
            _, _ = f.WriteString(summary)
            _ = f.Close()
        }
        return nil
    },
}

func init() {
    Root.AddCommand(internalStopMarkerCmd)
}
```

Create `cmd/internal_session_ready.go`:

```go
package cmd

import (
    "os"
    "path/filepath"

    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

var internalSessionReadyCmd = &cobra.Command{
    Use:    "_internal_session_ready <slave-id>",
    Short:  "Internal: SessionStart hook target; touches .ready",
    Args:   cobra.ExactArgs(1),
    Hidden: true,
    RunE: func(cmd *cobra.Command, args []string) error {
        sess, err := tmux.CurrentSession()
        if err != nil {
            return err
        }
        slaveDir := state.SlaveDir(sess, args[0])
        f, err := os.Create(filepath.Join(slaveDir, ".ready"))
        if err != nil {
            return err
        }
        return f.Close()
    },
}

func init() {
    Root.AddCommand(internalSessionReadyCmd)
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Manual sanity check (optional, not a real test)**

```bash
mkdir -p ~/.conductor/sessions/testsess/s1
TMUX=fake echo '{"session_id":"x","transcript_path":"testdata/transcript_basic.jsonl"}' | \
  go run . _internal_stop_marker s1
```
Expected: without a real tmux session, this will error on `CurrentSession`. That's fine — integration happens in real use.

- [ ] **Step 4: Commit**

```bash
git add cmd/internal_stop_marker.go cmd/internal_session_ready.go
git commit -m "feat(cmd): add _internal_stop_marker and _internal_session_ready hooks"
```

---

## Task 9: `spawn` subcommand

**Files:**
- Create: `cmd/spawn.go`

- [ ] **Step 1: Implement spawn**

Create `cmd/spawn.go`:

```go
package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "time"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/hooks"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/fsnotify/fsnotify"
    "github.com/spf13/cobra"
)

var spawnName string

var spawnCmd = &cobra.Command{
    Use:   "spawn",
    Short: "Launch a new slave Claude in a new tmux window",
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "conductor must run inside a tmux session (run `conductor` first)")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "detect tmux session: %v", err)
        }

        id := spawnName
        if id == "" {
            id = nextSlaveID(sess)
        }
        if state.SlaveExists(sess, id) {
            return CLIError(exitcode.InternalError, "slave %q already exists", id)
        }

        slaveDir := state.SlaveDir(sess, id)
        if err := os.MkdirAll(slaveDir, 0o755); err != nil {
            return err
        }

        conductorBin, err := os.Executable()
        if err != nil {
            return err
        }
        projectCwd, err := os.Getwd()
        if err != nil {
            return err
        }

        settingsPath := filepath.Join(slaveDir, "settings.json")
        if err := os.WriteFile(settingsPath, []byte(hooks.RenderSettings(id, conductorBin)), 0o644); err != nil {
            return err
        }
        runShPath := filepath.Join(slaveDir, "run.sh")
        runShContent := hooks.RenderRunScript(slaveDir, projectCwd)
        if err := os.WriteFile(runShPath, []byte(runShContent), 0o755); err != nil {
            return err
        }

        // Set up fsnotify BEFORE starting window.
        w, err := fsnotify.NewWatcher()
        if err != nil {
            return err
        }
        defer w.Close()
        if err := w.Add(slaveDir); err != nil {
            return err
        }

        if err := tmux.NewWindowCmd(sess, id, runShPath).Run(); err != nil {
            return CLIError(exitcode.InternalError, "tmux new-window: %v", err)
        }

        // Wait up to 30s for .ready.
        readyPath := filepath.Join(slaveDir, ".ready")
        if _, err := os.Stat(readyPath); err == nil {
            fmt.Println(id)
            return nil
        }
        timeout := time.After(30 * time.Second)
        for {
            select {
            case ev := <-w.Events:
                if ev.Op&fsnotify.Create == fsnotify.Create && filepath.Base(ev.Name) == ".ready" {
                    fmt.Println(id)
                    return nil
                }
            case err := <-w.Errors:
                return err
            case <-timeout:
                return CLIError(exitcode.Crash, "slave did not become ready within 30s")
            }
        }
    },
}

func init() {
    spawnCmd.Flags().StringVar(&spawnName, "name", "", "slave ID to use (default: auto-increment s<N>)")
    Root.AddCommand(spawnCmd)
}

// nextSlaveID returns s1 if no slaves exist, otherwise s<max+1>.
func nextSlaveID(session string) string {
    entries, err := os.ReadDir(state.SessionDir(session))
    if err != nil {
        return "s1"
    }
    var nums []int
    for _, e := range entries {
        if !e.IsDir() || !strings.HasPrefix(e.Name(), "s") {
            continue
        }
        n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "s"))
        if err != nil {
            continue
        }
        nums = append(nums, n)
    }
    if len(nums) == 0 {
        return "s1"
    }
    sort.Ints(nums)
    return "s" + strconv.Itoa(nums[len(nums)-1]+1)
}
```

Remove the `"os/exec"` line from the imports block at the top of `cmd/spawn.go` — it is not used in this file.

- [ ] **Step 2: Build**

Run: `go build ./... && go vet ./...`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/spawn.go
git commit -m "feat(cmd): add spawn subcommand"
```

---

## Task 10: `send` subcommand (core blocking loop)

**Files:**
- Create: `cmd/send.go`

- [ ] **Step 1: Implement send**

Create `cmd/send.go`:

```go
package cmd

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/fsnotify/fsnotify"
    "github.com/spf13/cobra"
)

var (
    sendTimeout int
    sendQuiet   bool
)

var sendCmd = &cobra.Command{
    Use:   "send <slave-id> <prompt>",
    Short: "Send a prompt to a slave and block until its turn ends",
    Args:  cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "detect tmux session: %v", err)
        }

        id := args[0]
        prompt := args[1]

        if !state.SlaveExists(sess, id) {
            return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
        }
        slaveDir := state.SlaveDir(sess, id)

        if err := state.CreatePending(slaveDir); err != nil {
            if errors.Is(err, state.ErrBusy) {
                return CLIError(exitcode.Busy, "slave %s is busy", id)
            }
            return err
        }
        if err := state.RemoveDone(slaveDir); err != nil {
            return err
        }

        // Arm watcher BEFORE injecting prompt.
        w, err := fsnotify.NewWatcher()
        if err != nil {
            return err
        }
        defer w.Close()
        if err := w.Add(slaveDir); err != nil {
            return err
        }

        // Inject prompt via paste-buffer.
        promptFile, err := os.CreateTemp("", "conductor-prompt-*.txt")
        if err != nil {
            return err
        }
        defer os.Remove(promptFile.Name())
        if _, err := promptFile.WriteString(prompt); err != nil {
            return err
        }
        promptFile.Close()

        if err := tmux.LoadBufferCmd(promptFile.Name()).Run(); err != nil {
            return CLIError(exitcode.InternalError, "tmux load-buffer: %v", err)
        }
        if err := tmux.PasteBufferCmd(sess, id).Run(); err != nil {
            return CLIError(exitcode.InternalError, "tmux paste-buffer: %v", err)
        }
        if err := tmux.SendKeysCmd(sess, id, "Enter").Run(); err != nil {
            return CLIError(exitcode.InternalError, "tmux send-keys Enter: %v", err)
        }

        // watch-then-check for .done race.
        donePath := filepath.Join(slaveDir, ".done")
        if _, err := os.Stat(donePath); err == nil {
            return finishSend(slaveDir)
        }

        deadline := time.After(time.Duration(sendTimeout) * time.Second)
        liveness := time.NewTicker(1 * time.Second)
        defer liveness.Stop()

        for {
            select {
            case ev := <-w.Events:
                if (ev.Op&fsnotify.Create == fsnotify.Create || ev.Op&fsnotify.Write == fsnotify.Write) &&
                    filepath.Base(ev.Name) == ".done" {
                    return finishSend(slaveDir)
                }
            case err := <-w.Errors:
                return err
            case <-liveness.C:
                dead, _ := tmux.PaneDead(sess, id)
                if dead {
                    _ = state.RemovePending(slaveDir)
                    return CLIError(exitcode.Crash, "slave %s window is dead", id)
                }
            case <-deadline:
                return CLIError(exitcode.Timeout, "slave %s did not complete within %ds (may still be working; use `conductor interrupt %s`)", id, sendTimeout, id)
            }
        }
    },
}

func finishSend(slaveDir string) error {
    content, err := state.ReadDone(slaveDir)
    if err != nil {
        return err
    }
    _ = state.RemovePending(slaveDir)
    fmt.Print(content)
    return nil
}

func init() {
    sendCmd.Flags().IntVar(&sendTimeout, "timeout", 600, "seconds to wait for slave turn completion")
    sendCmd.Flags().BoolVar(&sendQuiet, "quiet", false, "suppress stderr progress")
    Root.AddCommand(sendCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Unit-test the ID lookup error paths**

Create `cmd/send_test.go`:

```go
package cmd

import (
    "testing"
)

// Smoke test: sendCmd exists and has required flags wired.
func TestSendCmd_flags(t *testing.T) {
    f := sendCmd.Flags()
    if f.Lookup("timeout") == nil {
        t.Error("missing --timeout flag")
    }
    if f.Lookup("quiet") == nil {
        t.Error("missing --quiet flag")
    }
}
```

Run: `go test ./cmd/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/send.go cmd/send_test.go
git commit -m "feat(cmd): add send subcommand with sync blocking via fsnotify"
```

---

## Task 11: `interrupt` subcommand

**Files:**
- Create: `cmd/interrupt.go`

- [ ] **Step 1: Implement interrupt**

Create `cmd/interrupt.go`:

```go
package cmd

import (
    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

var interruptCmd = &cobra.Command{
    Use:   "interrupt <slave-id>",
    Short: "Cancel the slave's current turn (sends Escape)",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "%v", err)
        }
        id := args[0]
        if !state.SlaveExists(sess, id) {
            return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
        }
        if err := tmux.SendKeysCmd(sess, id, "Escape").Run(); err != nil {
            return CLIError(exitcode.InternalError, "send Escape: %v", err)
        }
        return nil
    },
}

func init() {
    Root.AddCommand(interruptCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/interrupt.go
git commit -m "feat(cmd): add interrupt subcommand (sends Escape to slave)"
```

---

## Task 12: `kill` subcommand

**Files:**
- Create: `cmd/kill.go`

- [ ] **Step 1: Implement kill**

Create `cmd/kill.go`:

```go
package cmd

import (
    "os"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

var killCmd = &cobra.Command{
    Use:   "kill <slave-id>",
    Short: "Close the slave's tmux window and remove its state dir",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "%v", err)
        }
        id := args[0]
        if !state.SlaveExists(sess, id) {
            return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
        }

        // Kill window (ignore error if already gone).
        _ = tmux.KillWindowCmd(sess, id).Run()

        // Remove state dir.
        if err := os.RemoveAll(state.SlaveDir(sess, id)); err != nil {
            return CLIError(exitcode.InternalError, "remove state dir: %v", err)
        }
        return nil
    },
}

func init() {
    Root.AddCommand(killCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/kill.go
git commit -m "feat(cmd): add kill subcommand"
```

---

## Task 13: `reset` subcommand

**Files:**
- Create: `cmd/reset.go`

Reset = kill the slave's Claude process in the window, then re-spawn it in the **same** window (same ID).

- [ ] **Step 1: Implement reset**

Create `cmd/reset.go`:

```go
package cmd

import (
    "os"
    "path/filepath"
    "time"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/fsnotify/fsnotify"
    "github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
    Use:   "reset <slave-id>",
    Short: "Restart the slave's claude process in its existing window",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "%v", err)
        }
        id := args[0]
        if !state.SlaveExists(sess, id) {
            return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
        }
        slaveDir := state.SlaveDir(sess, id)

        // Clear control files.
        for _, f := range []string{".pending", ".done", ".ready", ".exit-code"} {
            _ = os.Remove(filepath.Join(slaveDir, f))
        }

        // Send Ctrl-C then `exit` to the shell/claude running in the window.
        if err := tmux.SendKeysCmd(sess, id, "C-c").Run(); err != nil {
            return CLIError(exitcode.InternalError, "send C-c: %v", err)
        }
        time.Sleep(300 * time.Millisecond)
        if err := tmux.SendKeysCmd(sess, id, "C-c").Run(); err != nil {
            // best effort; keep going
            _ = err
        }

        // Re-exec run.sh in the same window.
        runShPath := filepath.Join(slaveDir, "run.sh")

        // Arm watcher for .ready BEFORE re-starting.
        w, err := fsnotify.NewWatcher()
        if err != nil {
            return err
        }
        defer w.Close()
        if err := w.Add(slaveDir); err != nil {
            return err
        }

        if err := tmux.SendKeysCmd(sess, id, runShPath, "Enter").Run(); err != nil {
            return CLIError(exitcode.InternalError, "relaunch run.sh: %v", err)
        }

        deadline := time.After(30 * time.Second)
        for {
            select {
            case ev := <-w.Events:
                if ev.Op&fsnotify.Create == fsnotify.Create && filepath.Base(ev.Name) == ".ready" {
                    return nil
                }
            case err := <-w.Errors:
                return err
            case <-deadline:
                return CLIError(exitcode.Crash, "slave did not become ready within 30s after reset")
            }
        }
    },
}

func init() {
    Root.AddCommand(resetCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/reset.go
git commit -m "feat(cmd): add reset subcommand"
```

---

## Task 14: `list` subcommand

**Files:**
- Create: `cmd/list.go`

- [ ] **Step 1: Implement list**

Create `cmd/list.go`:

```go
package cmd

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List active slaves in this conductor session",
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "%v", err)
        }
        entries, err := os.ReadDir(state.SessionDir(sess))
        if err != nil {
            if os.IsNotExist(err) {
                return nil
            }
            return err
        }
        for _, e := range entries {
            if !e.IsDir() || e.Name() == "master" {
                continue
            }
            id := e.Name()
            st := "idle"
            if _, err := os.Stat(filepath.Join(state.SlaveDir(sess, id), ".pending")); err == nil {
                st = "busy"
            }
            fmt.Printf("%s\t%s\n", id, st)
        }
        return nil
    },
}

func init() {
    Root.AddCommand(listCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/list.go
git commit -m "feat(cmd): add list subcommand"
```

---

## Task 15: `last` subcommand

**Files:**
- Create: `cmd/last.go`

- [ ] **Step 1: Implement last**

Create `cmd/last.go`:

```go
package cmd

import (
    "fmt"
    "os"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

var lastCmd = &cobra.Command{
    Use:   "last <slave-id>",
    Short: "Print slave's last assistant response (non-blocking)",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if !tmux.InTmux() {
            return CLIError(exitcode.NotInTmux, "must run inside conductor tmux session")
        }
        sess, err := tmux.CurrentSession()
        if err != nil {
            return CLIError(exitcode.InternalError, "%v", err)
        }
        id := args[0]
        if !state.SlaveExists(sess, id) {
            return CLIError(exitcode.UnknownSlave, "unknown slave %q", id)
        }
        content, err := state.ReadDone(state.SlaveDir(sess, id))
        if err != nil {
            if os.IsNotExist(err) {
                return CLIError(exitcode.InternalError, "no prior response for %s", id)
            }
            return err
        }
        fmt.Print(content)
        return nil
    },
}

func init() {
    Root.AddCommand(lastCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/last.go
git commit -m "feat(cmd): add last subcommand"
```

---

## Task 16: Root bootstrap — `conductor` with no args

**Files:**
- Modify: `cmd/root.go`
- Create: `cmd/bootstrap.go`

Root behavior:
1. If not in tmux: create a detached session named `conductor` running `$0` inside it, then attach.
2. If in tmux: lay out master in window 0 (current), pre-spawn `s1` in window 1.

- [ ] **Step 1: Write SYSTEM.md template constant**

Create `cmd/bootstrap.go`:

```go
package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "time"

    "github.com/cloudchamb3r/claude-conductor/internal/exitcode"
    "github.com/cloudchamb3r/claude-conductor/internal/state"
    "github.com/cloudchamb3r/claude-conductor/internal/tmux"
    "github.com/spf13/cobra"
)

const masterSystemPrompt = `You are the **master** in a claude-conductor session.

## Your role
- The user gives you a high-level GOAL.
- You do NOT perform the work yourself. You delegate to slave Claude sessions via the ` + "`conductor`" + ` CLI (available through Bash).
- Treat each slave as a fresh Claude. Prompts must be self-contained with all needed context.

## Available slaves
- ` + "`s1`" + ` is pre-spawned and ready.
- Spawn more with ` + "`conductor spawn`" + ` if work can be split safely (slaves share the cwd — avoid overlap).

## Workflow
1. Break the goal into delegatable chunks.
2. ` + "`conductor send <slave-id> \"<full prompt>\"`" + ` — blocks; stdout is the slave's final message.
3. Read output, decide:
   - Need more work → ` + "`conductor send`" + ` again (slave's context persists).
   - Slave asked a question → answer via ` + "`conductor send`" + `.
   - Off-track → ` + "`conductor interrupt <id>`" + ` then re-prompt, or ` + "`conductor reset <id>`" + ` for clean slate.
4. Loop until the goal is complete. Report to the user.

## Hard rules
- Do not use Read/Edit/Write/Grep/Glob on project files yourself unless the user speaks directly to you. That is the slaves' job.
- Do not ask the user "what should I do next?" unless the goal is genuinely ambiguous.
- Surface slave errors transparently.

## conductor CLI
- ` + "`conductor spawn [--name sN]`" + ` — new slave
- ` + "`conductor send [--timeout S] <id> \"<prompt>\"`" + ` — blocking
- ` + "`conductor interrupt <id>`" + ` — cancel current turn
- ` + "`conductor reset <id>`" + ` — fresh claude in same window
- ` + "`conductor kill <id>`" + ` — close window
- ` + "`conductor list`" + ` — active slaves
- ` + "`conductor last <id>`" + ` — print prior response
`

func runRootBootstrap(cmd *cobra.Command, args []string) error {
    projectCwd, err := os.Getwd()
    if err != nil {
        return err
    }

    if !tmux.InTmux() {
        // Out of tmux: create new session and re-invoke ourselves inside it, then attach.
        sessionName := "conductor"
        conductorBin, err := os.Executable()
        if err != nil {
            return err
        }
        // has-session: if already exists, just attach.
        if tmux.HasSessionCmd(sessionName).Run() == nil {
            return tmux.AttachSessionCmd(sessionName).Run()
        }
        // new-session runs `conductorBin` (which re-enters this function under $TMUX set).
        newCmd := tmux.NewSessionCmd(sessionName, conductorBin)
        newCmd.Dir = projectCwd
        if err := newCmd.Run(); err != nil {
            return CLIError(exitcode.InternalError, "tmux new-session: %v", err)
        }
        // Attach.
        att := tmux.AttachSessionCmd(sessionName)
        att.Stdin, att.Stdout, att.Stderr = os.Stdin, os.Stdout, os.Stderr
        return att.Run()
    }

    // Inside tmux: set up session dir, master window, pre-spawn s1.
    sess, err := tmux.CurrentSession()
    if err != nil {
        return err
    }
    sessionDir := state.SessionDir(sess)
    masterDir := filepath.Join(sessionDir, "master")
    if err := os.MkdirAll(masterDir, 0o755); err != nil {
        return err
    }
    systemPath := filepath.Join(masterDir, "SYSTEM.md")
    if err := os.WriteFile(systemPath, []byte(masterSystemPrompt), 0o644); err != nil {
        return err
    }

    // Write session.json.
    sessionJSON := fmt.Sprintf(`{"session":"%s","cwd":"%s","started":"%s"}`,
        sess, projectCwd, time.Now().UTC().Format(time.RFC3339))
    if err := os.WriteFile(filepath.Join(sessionDir, "session.json"), []byte(sessionJSON), 0o644); err != nil {
        return err
    }

    // Launch master in current window (window 0).
    masterLaunch := fmt.Sprintf(
        `cd %q && claude --append-system-prompt "$(cat %q)" --dangerously-skip-permissions`,
        projectCwd, systemPath)
    if err := tmux.SendKeysCmd(sess, "0", masterLaunch, "Enter").Run(); err != nil {
        return CLIError(exitcode.InternalError, "launch master: %v", err)
    }

    // Pre-spawn s1 by invoking our own `spawn` subcommand in a subprocess.
    // This keeps bootstrap simple and reuses spawn's logic.
    conductorBin, _ := os.Executable()
    spawnCmd := exec.Command(conductorBin, "spawn", "--name", "s1")
    spawnCmd.Stdout = os.Stdout
    spawnCmd.Stderr = os.Stderr
    if err := spawnCmd.Run(); err != nil {
        return CLIError(exitcode.InternalError, "pre-spawn s1: %v", err)
    }
    return nil
}
```

- [ ] **Step 2: Wire root.RunE**

Edit `cmd/root.go` — inside the existing `Root = &cobra.Command{...}` literal, replace the comment line `// RunE is wired in Task 16 (bootstrap).` with:

```go
    RunE: runRootBootstrap,
```

Do not touch any other field or helper function in that file.

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 4: Commit**

```bash
git add cmd/root.go cmd/bootstrap.go
git commit -m "feat(cmd): root bootstrap — create tmux session, launch master, pre-spawn s1"
```

---

## Task 17: Manual smoke test doc + README + final build check

**Files:**
- Create: `docs/manual-smoketest.md`
- Create: `README.md`

- [ ] **Step 1: Write manual smoke test checklist**

Create `docs/manual-smoketest.md`:

```markdown
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
- [ ] Expect: after ~a few seconds, s1 is back with no prior context.

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
```

- [ ] **Step 2: Write README**

Create `README.md`:

```markdown
# claude-conductor

A master Claude Code session delegates to slave Claude Code sessions via a Go CLI, inside a shared tmux session.

## What it does

You give a high-level goal to the **master** Claude. The master writes prompts for **slaves** running in sibling tmux windows, reads their outputs, and decides follow-ups — all autonomously — until the goal is done.

## Requirements

- tmux
- `claude` (Claude Code CLI, logged in with a Pro/Max subscription)
- Go 1.22+ (to build)

## Install

```bash
go install github.com/cloudchamb3r/claude-conductor@latest
# or from source:
git clone https://github.com/cloudchamb3r/claude-conductor
cd claude-conductor
go install ./...
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
conductor spawn [--name sN]              # new slave
conductor send [--timeout S] <id> <msg>  # send prompt, block on completion
conductor interrupt <id>                 # cancel current turn (Escape)
conductor reset <id>                     # fresh slave in same window
conductor kill <id>                      # close window + clean up
conductor list                           # active slaves
conductor last <id>                      # print slave's last response
```

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
```

- [ ] **Step 3: Final build & test**

Run: `go build ./... && go test ./...`
Expected: build succeeds, all unit tests pass.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/manual-smoketest.md
git commit -m "docs: add README and manual smoke test checklist"
```

---

## Done

At this point:
- A `conductor` binary exists implementing all 8 subcommands from the spec.
- All deterministic logic (transcript parsing, state transitions, tmux command building, hook rendering, audit log) is unit-tested.
- Manual smoke test covers end-to-end behavior.
- Open questions from the spec (hook injection mechanism, Escape semantics, paste-buffer commit) will be validated during the first real smoke test run. If any fails, file an issue and amend the spec before changing the implementation.
