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

		// Ensure the project-level hook settings are installed (no-op if already written).
		if err := installProjectHooks(projectCwd, conductorBin); err != nil {
			return CLIError(exitcode.InternalError, "install project hooks: %v", err)
		}

		runShPath := filepath.Join(slaveDir, "run.sh")
		runShContent := hooks.RenderRunScript(slaveDir, projectCwd, id)
		if err := os.WriteFile(runShPath, []byte(runShContent), 0o755); err != nil {
			return err
		}

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
