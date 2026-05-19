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

// maxStopHookStdin caps the JSON payload Claude Code feeds the hook so a
// rogue stdin (e.g. user manually invoking the subcommand) cannot OOM us.
const maxStopHookStdin = 1 << 20 // 1 MiB

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
		if err := state.ValidateSlaveID(slaveID); err != nil {
			return fmt.Errorf("invalid slave id %q: %w", slaveID, err)
		}

		raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxStopHookStdin))
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

		slaveDir := state.SlaveDir(sess, slaveID)
		if slaveDir == "" {
			return fmt.Errorf("invalid slave dir for id %q", slaveID)
		}

		// Update the sticky transcript-path file on every fire so the NEXT
		// `conductor send` can compute a meaningful watermark. Best-effort.
		if hookIn.TranscriptPath != "" {
			_ = state.WriteTranscriptPath(slaveDir, hookIn.TranscriptPath)
		}

		// No active .pending → no `conductor send` is waiting for this turn.
		lock, lerr := state.ReadPending(slaveDir)
		if lerr != nil {
			logOrphanHook(slaveDir, hookIn.SessionID, "no .pending lock present")
			return nil
		}
		turnID := lock.TurnID

		// Transcript watermark check: if the send recorded a path + offset and
		// the path matches and the file has NOT grown beyond the offset, this
		// hook was queued before our prompt was processed — the transcript
		// text we'd deliver would belong to the prior turn. Reject.
		if lock.TranscriptPath != "" && lock.TranscriptPath == hookIn.TranscriptPath {
			if fi, statErr := os.Stat(hookIn.TranscriptPath); statErr == nil {
				if fi.Size() <= lock.TranscriptOffset {
					// Stale Stop hook for a prior turn. Don't write .done; the
					// send loop will keep waiting and the slave's NEXT Stop
					// hook (for the new turn) will deliver. Log forensically.
					logOrphanHook(slaveDir, hookIn.SessionID,
						fmt.Sprintf("stale hook: transcript size %d <= watermark %d (path %s)",
							fi.Size(), lock.TranscriptOffset, hookIn.TranscriptPath))
					return nil
				}
			}
		}

		text, terr := transcript.LastAssistantText(hookIn.TranscriptPath)
		if terr != nil {
			if werr := state.WriteDoneError(slaveDir, turnID,
				fmt.Sprintf("transcript read: %v", terr)); werr != nil {
				return werr
			}
			return terr
		}

		if werr := state.WriteDone(slaveDir, turnID, text); werr != nil {
			_ = state.WriteDoneError(slaveDir, turnID, werr.Error())
			return werr
		}

		summary := fmt.Sprintf("%s | turn=%s ended | %d bytes | watermark=%d | size=%s | session=%s\n",
			time.Now().UTC().Format(time.RFC3339), turnID, len(text),
			lock.TranscriptOffset, statSize(hookIn.TranscriptPath), hookIn.SessionID)
		f, err := os.OpenFile(filepath.Join(slaveDir, "transcript.log"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(summary)
			_ = f.Close()
		}
		return nil
	},
}

func statSize(path string) string {
	if path == "" {
		return "(unknown)"
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "(stat err)"
	}
	return fmt.Sprintf("%d", fi.Size())
}

// logOrphanHook records a Stop hook that fired with no usable .pending lock
// (or whose transcript was confirmed stale by the watermark check) to
// transcript.log. We deliberately do NOT write a .done file because there
// is no caller to deliver to, and any .done we wrote could later be
// mis-attributed to a future `conductor send`'s turn.
func logOrphanHook(slaveDir, sessionID, why string) {
	line := fmt.Sprintf("%s | ORPHAN HOOK | %s | session=%s\n",
		time.Now().UTC().Format(time.RFC3339), why, sessionID)
	f, err := os.OpenFile(filepath.Join(slaveDir, "transcript.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

func init() {
	Root.AddCommand(internalStopMarkerCmd)
}
