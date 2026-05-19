package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/exitcode"
	"github.com/j0j1j2/claude-conductor/internal/hooks"
	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
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
		if err := state.ValidateSlaveID(id); err != nil {
			return CLIError(exitcode.UnknownSlave, "invalid slave id %q: %v", id, err)
		}

		slaveDir := state.SlaveDir(sess, id)
		if slaveDir == "" {
			return CLIError(exitcode.UnknownSlave, "invalid slave id %q", id)
		}

		// Atomic claim: Mkdir (not MkdirAll) returns EEXIST if another
		// concurrent spawn beat us. This is the gating step against two
		// `conductor spawn` calls racing for the same auto-allocated name.
		if err := os.MkdirAll(state.SessionDir(sess), 0o755); err != nil {
			return CLIError(exitcode.InternalError, "mkdir session dir: %v", err)
		}
		if err := os.Mkdir(slaveDir, 0o755); err != nil {
			if os.IsExist(err) {
				return CLIError(exitcode.InternalError, "slave %q already exists", id)
			}
			return CLIError(exitcode.InternalError, "mkdir slave dir: %v", err)
		}

		windowCreated := false
		spawnSucceeded := false
		var restoreHooks func()
		defer func() {
			if spawnSucceeded {
				return
			}
			if windowCreated {
				_ = tmux.KillWindowCmd(sess, id).Run()
			}
			if restoreHooks != nil {
				restoreHooks()
			}
			_ = os.RemoveAll(slaveDir)
		}()

		conductorBin, err := os.Executable()
		if err != nil {
			return CLIError(exitcode.InternalError, "os.Executable: %v", err)
		}
		projectCwd, err := os.Getwd()
		if err != nil {
			return CLIError(exitcode.InternalError, "os.Getwd: %v", err)
		}

		restoreHooks, err = installProjectHooks(projectCwd, conductorBin)
		if err != nil {
			return CLIError(exitcode.InternalError, "install project hooks: %v", err)
		}

		runShPath := filepath.Join(slaveDir, "run.sh")
		runShContent := hooks.RenderRunScript(slaveDir, projectCwd, id)
		if err := os.WriteFile(runShPath, []byte(runShContent), 0o755); err != nil {
			return CLIError(exitcode.InternalError, "write run.sh: %v", err)
		}

		w, err := fsnotify.NewWatcher()
		if err != nil {
			return CLIError(exitcode.InternalError, "fsnotify: %v", err)
		}
		defer w.Close()
		if err := w.Add(slaveDir); err != nil {
			return CLIError(exitcode.InternalError, "fsnotify watch: %v", err)
		}

		if err := tmux.NewWindowCmd(sess, id, runShPath).Run(); err != nil {
			return CLIError(exitcode.InternalError, "tmux new-window: %v", err)
		}
		windowCreated = true

		readyPath := filepath.Join(slaveDir, ".ready")
		if _, err := os.Stat(readyPath); err == nil {
			spawnSucceeded = true
			fmt.Println(id)
			return nil
		}
		timeout := time.After(30 * time.Second)
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify channel closed")
				}
				if ev.Op&fsnotify.Create == fsnotify.Create &&
					filepath.Base(ev.Name) == ".ready" {
					spawnSucceeded = true
					fmt.Println(id)
					return nil
				}
				// SessionStart hook failed and dropped a .ready-error.
				if filepath.Base(ev.Name) == ".ready-error" {
					b, _ := os.ReadFile(ev.Name)
					return CLIError(exitcode.Crash, "slave failed during SessionStart hook: %s", strings.TrimSpace(string(b)))
				}
			case werr, ok := <-w.Errors:
				if !ok {
					return CLIError(exitcode.InternalError, "fsnotify error channel closed")
				}
				return CLIError(exitcode.InternalError, "fsnotify: %v", werr)
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
