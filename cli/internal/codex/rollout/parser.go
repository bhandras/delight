package rollout

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Line is a single JSONL record written to a Codex rollout file.
type Line struct {
	// Timestamp is an RFC3339 timestamp string.
	Timestamp string `json:"timestamp"`
	// Type identifies the top-level event kind (e.g. "session_meta").
	Type string `json:"type"`
	// Payload holds the event-specific object.
	Payload json.RawMessage `json:"payload"`
}

const (
	// LineTypeSessionMeta identifies the session metadata line.
	LineTypeSessionMeta = "session_meta"
	// LineTypeEventMsg identifies a small envelope message emitted by Codex.
	LineTypeEventMsg = "event_msg"
	// LineTypeResponseItem identifies a structured response item (messages, etc.).
	LineTypeResponseItem = "response_item"
)

const (
	// EventMsgTypeUserMessage is an event_msg payload subtype for user input.
	EventMsgTypeUserMessage = "user_message"
)

const (
	// ResponseItemTypeMessage is a response_item payload subtype for messages.
	ResponseItemTypeMessage = "message"
)

// Event is a marker interface for parsed rollout events.
type Event interface {
	isRolloutEvent()
}

// EvSessionMeta is emitted when the rollout includes the session id.
type EvSessionMeta struct {
	// SessionID is the Codex session identifier.
	SessionID string
	// AtMs is the wall-clock timestamp (unix millis) for the event.
	AtMs int64
}

// isRolloutEvent marks EvSessionMeta as an Event.
func (EvSessionMeta) isRolloutEvent() {}

// EvUserMessage is emitted for user_message events (user input text).
type EvUserMessage struct {
	// Text is the user input message.
	Text string
	// AtMs is the wall-clock timestamp (unix millis) for the event.
	AtMs int64
}

// isRolloutEvent marks EvUserMessage as an Event.
func (EvUserMessage) isRolloutEvent() {}

// EvAssistantMessage is emitted for assistant message output.
type EvAssistantMessage struct {
	// Text is the assistant output message.
	Text string
	// AtMs is the wall-clock timestamp (unix millis) for the event.
	AtMs int64
}

// isRolloutEvent marks EvAssistantMessage as an Event.
func (EvAssistantMessage) isRolloutEvent() {}

// ParseLine parses a JSONL line from a Codex rollout file.
//
// It returns (event, ok, err). ok is false for unsupported/uninteresting lines.
func ParseLine(line []byte) (Event, bool, error) {
	var rec Line
	if err := json.Unmarshal(bytesTrimSpace(line), &rec); err != nil {
		return nil, false, err
	}

	atMs := parseTimestampMs(rec.Timestamp)

	switch rec.Type {
	case LineTypeSessionMeta:
		var payload struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			return nil, false, err
		}
		if payload.ID == "" {
			return nil, false, fmt.Errorf("session_meta missing id")
		}
		return EvSessionMeta{SessionID: payload.ID, AtMs: atMs}, true, nil

	case LineTypeEventMsg:
		var payload struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			return nil, false, err
		}
		if payload.Type == EventMsgTypeUserMessage && strings.TrimSpace(payload.Message) != "" {
			return EvUserMessage{Text: payload.Message, AtMs: atMs}, true, nil
		}
		return nil, false, nil

	case LineTypeResponseItem:
		var payload struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			return nil, false, err
		}
		if payload.Type != ResponseItemTypeMessage || payload.Role != "assistant" {
			return nil, false, nil
		}
		text := collectTextBlocks(payload.Content, "output_text")
		if strings.TrimSpace(text) == "" {
			return nil, false, nil
		}
		return EvAssistantMessage{Text: text, AtMs: atMs}, true, nil

	default:
		return nil, false, nil
	}
}

// collectTextBlocks concatenates content blocks matching the expected type.
func collectTextBlocks(blocks []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}, wantType string) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Type != wantType {
			continue
		}
		if block.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(block.Text)
	}
	return b.String()
}

// parseTimestampMs converts an RFC3339 timestamp string to unix millis.
//
// If parsing fails, it returns 0 so callers can treat the timestamp as unknown.
func parseTimestampMs(ts string) int64 {
	if ts == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

// bytesTrimSpace returns a trimmed copy of b suitable for json.Unmarshal.
func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
