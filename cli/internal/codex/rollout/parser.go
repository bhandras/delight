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
	// ResponseItemTypeReasoning is a response_item payload subtype for reasoning.
	ResponseItemTypeReasoning = "reasoning"
	// ResponseItemTypeFunctionCall is a response_item payload subtype for tool calls.
	ResponseItemTypeFunctionCall = "function_call"
	// ResponseItemTypeFunctionCallOutput is a response_item payload subtype for tool results.
	ResponseItemTypeFunctionCallOutput = "function_call_output"
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

// EvReasoningSummary is emitted for reasoning response_item output.
//
// Codex local mode writes "reasoning" items (often encrypted) alongside a
// plaintext summary list. We surface the summary text since the full reasoning
// content may be encrypted and not present.
type EvReasoningSummary struct {
	// Text is the plaintext reasoning summary (markdown).
	Text string
	// AtMs is the wall-clock timestamp (unix millis) for the event.
	AtMs int64
}

// isRolloutEvent marks EvReasoningSummary as an Event.
func (EvReasoningSummary) isRolloutEvent() {}

// EvFunctionCall is emitted when Codex requests a tool execution (function call).
type EvFunctionCall struct {
	// CallID is the stable identifier used to correlate with EvFunctionCallOutput.
	CallID string
	// Name is the function/tool name (e.g. "shell_command").
	Name string
	// Arguments is a JSON string payload (Codex stores arguments as a string).
	Arguments string
	// AtMs is the wall-clock timestamp (unix millis) for the event.
	AtMs int64
}

// isRolloutEvent marks EvFunctionCall as an Event.
func (EvFunctionCall) isRolloutEvent() {}

// EvFunctionCallOutput is emitted for the output of a function call.
type EvFunctionCallOutput struct {
	// CallID is the identifier of the corresponding EvFunctionCall.
	CallID string
	// Output is the string output returned by the tool.
	Output string
	// AtMs is the wall-clock timestamp (unix millis) for the event.
	AtMs int64
}

// isRolloutEvent marks EvFunctionCallOutput as an Event.
func (EvFunctionCallOutput) isRolloutEvent() {}

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
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rec.Payload, &envelope); err != nil {
			return nil, false, err
		}

		switch envelope.Type {
		case ResponseItemTypeMessage:
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
			if payload.Role != "assistant" {
				return nil, false, nil
			}
			text := collectTextBlocks(payload.Content, "output_text")
			if strings.TrimSpace(text) == "" {
				return nil, false, nil
			}
			return EvAssistantMessage{Text: text, AtMs: atMs}, true, nil

		case ResponseItemTypeReasoning:
			var payload struct {
				Type    string `json:"type"`
				Summary []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"summary"`
			}
			if err := json.Unmarshal(rec.Payload, &payload); err != nil {
				return nil, false, err
			}
			text := collectTextBlocks(payload.Summary, "summary_text")
			if strings.TrimSpace(text) == "" {
				return nil, false, nil
			}
			return EvReasoningSummary{Text: text, AtMs: atMs}, true, nil

		case ResponseItemTypeFunctionCall:
			var payload struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
			}
			if err := json.Unmarshal(rec.Payload, &payload); err != nil {
				return nil, false, err
			}
			if strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.Name) == "" {
				return nil, false, nil
			}
			return EvFunctionCall{
				CallID:    payload.CallID,
				Name:      payload.Name,
				Arguments: payload.Arguments,
				AtMs:      atMs,
			}, true, nil

		case ResponseItemTypeFunctionCallOutput:
			var payload struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Output string `json:"output"`
			}
			if err := json.Unmarshal(rec.Payload, &payload); err != nil {
				return nil, false, err
			}
			if strings.TrimSpace(payload.CallID) == "" {
				return nil, false, nil
			}
			if payload.Output == "" {
				return nil, false, nil
			}
			return EvFunctionCallOutput{
				CallID: payload.CallID,
				Output: payload.Output,
				AtMs:   atMs,
			}, true, nil
		default:
			return nil, false, nil
		}

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
