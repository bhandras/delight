package webapi

import "encoding/json"

// APIError is a stable error envelope returned by bridge endpoints.
type APIError struct {
	// Code is a machine-readable error code.
	Code string `json:"code"`
	// Message is a human-readable error description.
	Message string `json:"message"`
}

// StreamEvent is the browser-facing event envelope for /api/stream.
type StreamEvent struct {
	// EventID is a monotonically increasing event cursor within one bridge
	// process lifetime.
	EventID int64 `json:"eventID"`
	// Kind names the event category (for example: connected, update, error).
	Kind string `json:"kind"`
	// TS is the Unix timestamp in milliseconds when the bridge emitted this
	// event.
	TS int64 `json:"ts"`
	// Payload is the event body encoded as JSON.
	Payload json.RawMessage `json:"payload"`
}

// TranscriptSettings stores transcript rendering preferences.
type TranscriptSettings struct {
	// ShowToolUse controls whether tool-use events are shown.
	ShowToolUse bool `json:"showToolUse"`
	// ShowToolOutput controls whether tool output is shown.
	ShowToolOutput bool `json:"showToolOutput"`
	// ShowReasoningSummaries controls whether reasoning summaries are shown.
	ShowReasoningSummaries bool `json:"showReasoningSummaries"`
	// FontSize controls transcript font size.
	FontSize float64 `json:"fontSize"`
}

// Preferences stores appearance and transcript settings.
type Preferences struct {
	// AppearanceMode stores one of system, light, or dark.
	AppearanceMode string `json:"appearanceMode"`
	// GlobalTranscript stores global transcript settings.
	GlobalTranscript TranscriptSettings `json:"globalTranscript"`
	// PerTerminalTranscript stores transcript overrides keyed by terminal ID.
	PerTerminalTranscript map[string]TranscriptSettings `json:"perTerminalTranscript"`
}

// PairTerminalReceipt captures pairing details shown after approval.
type PairTerminalReceipt struct {
	// ServerURL is the configured Delight server URL.
	ServerURL string `json:"serverURL"`
	// Host is the host metadata parsed from the pairing URL, when present.
	Host string `json:"host,omitempty"`
	// TerminalID is the terminal ID metadata parsed from the pairing URL.
	TerminalID string `json:"terminalID,omitempty"`
	// TerminalKey is the approved terminal public key in base64 form.
	TerminalKey string `json:"terminalKey"`
}

// LogEntry is a timestamped bridge diagnostic log line.
type LogEntry struct {
	// TS is the Unix timestamp in milliseconds when the line was recorded.
	TS int64 `json:"ts"`
	// Level is the log level label.
	Level string `json:"level"`
	// Message is the log message content.
	Message string `json:"message"`
}
