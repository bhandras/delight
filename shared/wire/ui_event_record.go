package wire

// UIEventRecord is a plaintext record that is encrypted with the session data
// key and stored as part of the durable session message stream.
//
// The phone UI uses these records to reconstruct tool/thinking progress updates
// after it reconnects (e.g. after being backgrounded).
type UIEventRecord struct {
	// Role identifies the record role. Must be "event".
	Role string `json:"role"`
	// Type identifies the record kind. Must be "ui.event".
	Type string `json:"type"`

	// SessionID identifies which session this event belongs to.
	SessionID string `json:"sessionId"`
	// EventID uniquely identifies the UI event.
	EventID string `json:"eventId"`

	// Kind identifies the UI event kind (e.g. "thinking" or "tool").
	Kind string `json:"kind"`
	// Phase indicates lifecycle progress (e.g. "start", "update", "end").
	Phase string `json:"phase"`
	// Status is a best-effort status label (e.g. "running", "ok", "error").
	Status string `json:"status,omitempty"`

	// BriefMarkdown is a compact Markdown rendering used for brief LOD.
	BriefMarkdown string `json:"briefMarkdown,omitempty"`
	// FullMarkdown is a detailed Markdown rendering used for full LOD.
	FullMarkdown string `json:"fullMarkdown,omitempty"`

	// AtMs is a wall-clock timestamp in milliseconds since epoch.
	AtMs int64 `json:"atMs,omitempty"`
}

// NewUIEventRecord converts an ephemeral UI event payload into a durable record.
func NewUIEventRecord(payload EphemeralUIEventPayload) UIEventRecord {
	return UIEventRecord{
		Role:          "event",
		Type:          "ui.event",
		SessionID:     payload.SessionID,
		EventID:       payload.EventID,
		Kind:          payload.Kind,
		Phase:         payload.Phase,
		Status:        payload.Status,
		BriefMarkdown: payload.BriefMarkdown,
		FullMarkdown:  payload.FullMarkdown,
		AtMs:          payload.AtMs,
	}
}
