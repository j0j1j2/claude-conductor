package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/j0j1j2/claude-conductor/internal/state"
	"github.com/j0j1j2/claude-conductor/internal/transcript"
	"github.com/j0j1j2/claude-conductor/internal/tmux"
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
			text = "(no assistant text in transcript)"
		}

		slaveDir := state.SlaveDir(sess, slaveID)
		if err := state.WriteDone(slaveDir, text); err != nil {
			return err
		}
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
