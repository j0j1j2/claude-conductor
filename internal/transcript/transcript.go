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
		Usage   *usage          `json:"usage"`
	} `json:"message"`
}

type usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
}

// Usage is the token spend of a single agent (one slave's transcript),
// summed across every assistant turn it produced.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	Turns               int
}

// Total is every token the model processed for this agent. Cache reads
// dominate long sessions, so leaving them out would under-report by orders of
// magnitude -- a cached turn can read 180k and freshly input 2.
func (u Usage) Total() int {
	return u.InputTokens + u.OutputTokens + u.CacheCreationTokens + u.CacheReadTokens
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
			continue
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

// TokensUsed sums the token usage of every assistant turn in a transcript,
// giving the per-agent spend for one slave. A transcript with no usage-bearing
// assistant turn returns a zero Usage and a nil error -- an idle slave costs
// nothing, which is not an error.
func TokensUsed(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, err
	}
	defer f.Close()

	var total Usage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.Type != "assistant" && e.Message.Role != "assistant" {
			continue
		}
		if e.Message.Usage == nil {
			continue
		}
		u := e.Message.Usage
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheCreationTokens += u.CacheCreationTokens
		total.CacheReadTokens += u.CacheReadTokens
		total.Turns++
	}
	if err := scanner.Err(); err != nil {
		return Usage{}, err
	}
	return total, nil
}

func extractText(raw json.RawMessage) string {
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
