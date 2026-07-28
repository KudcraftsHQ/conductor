package session

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// Timeouts for status inference
const (
	toolUseWaitTimeout  = 3 * time.Second  // After tool_use with no file growth → "waiting"
	stuckRunningTimeout = 15 * time.Second // No file growth while "running" → "stale"
)

// jsonlMessage represents a single JSONL entry from Claude Code
type jsonlMessage struct {
	Message *claudeMessage `json:"message,omitempty"`
}

type claudeMessage struct {
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason *string        `json:"stop_reason"`
}

type contentBlock struct {
	Type string `json:"type"` // "text", "tool_use", "tool_result", "thinking"
	Text string `json:"text,omitempty"`
}

// ReadJSONLStatus reads the latest entries from a JSONL file and determines agent status.
// It reads from the given offset (lastSize) to detect new entries.
func ReadJSONLStatus(path string, lastSize int64) (AgentStatus, int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return StatusIdle, lastSize
	}
	currentSize := fi.Size()

	// No growth
	if currentSize <= lastSize {
		return "", currentSize // empty string = no change
	}

	f, err := os.Open(path)
	if err != nil {
		return StatusIdle, lastSize
	}
	defer f.Close()

	// Seek to where we left off
	if lastSize > 0 {
		if _, err := f.Seek(lastSize, io.SeekStart); err != nil {
			return StatusIdle, currentSize
		}
	}

	// Read new lines and determine status from the LAST message
	var lastStatus AgentStatus
	scanner := bufio.NewScanner(f)
	// Allow large lines (JSONL entries can be big)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if status := parseJSONLEntry(line); status != "" {
			lastStatus = status
		}
	}

	if lastStatus == "" {
		lastStatus = StatusRunning // File grew but couldn't parse → assume running
	}
	return lastStatus, currentSize
}

// parseJSONLEntry parses a single JSONL line and returns the inferred status
func parseJSONLEntry(line string) AgentStatus {
	var entry jsonlMessage
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return ""
	}
	if entry.Message == nil {
		return ""
	}

	msg := entry.Message

	if msg.Role == "assistant" {
		// Check content types
		hasToolUse := false
		hasThinking := false
		for _, c := range msg.Content {
			if c.Type == "tool_use" {
				hasToolUse = true
			}
			if c.Type == "thinking" {
				hasThinking = true
			}
		}

		if hasToolUse {
			return StatusToolRunning
		}
		if hasThinking {
			return StatusRunning
		}

		// Check stop reason
		if msg.StopReason == nil {
			return StatusRunning // Still streaming
		}
		if *msg.StopReason == "end_turn" {
			return StatusDone
		}
		if *msg.StopReason == "tool_use" {
			return StatusToolRunning
		}
		return StatusRunning
	}

	if msg.Role == "user" {
		for _, c := range msg.Content {
			// Check for interrupt markers
			if c.Type == "text" && strings.HasPrefix(c.Text, "[Request interrupted by user") {
				return StatusInterrupted
			}
			// Tool results mean agent is about to run again
			if c.Type == "tool_result" {
				return StatusRunning
			}
		}
		// Normal user message = new prompt, agent will start running
		return StatusRunning
	}

	return ""
}

// InferTimeBasedStatus checks for waiting/stale states based on timing
func InferTimeBasedStatus(s *Session, now time.Time) AgentStatus {
	// Check for waiting: tool_use seen but no file growth for 3s
	if s.Status == StatusToolRunning && !s.ToolUseSeenAt.IsZero() {
		if now.Sub(s.ToolUseSeenAt) >= toolUseWaitTimeout {
			return StatusWaiting
		}
	}

	// Check for stale: running but no file growth for 15s
	if s.Status.IsActive() && !s.LastGrowthAt.IsZero() {
		if now.Sub(s.LastGrowthAt) >= stuckRunningTimeout {
			return StatusStale
		}
	}

	return s.Status
}
