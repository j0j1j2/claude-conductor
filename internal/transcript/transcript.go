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
