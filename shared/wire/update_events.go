package wire

// UpdateEvent is the persisted Socket.IO "update" event envelope.
//
// The server emits these to keep user and session clients in sync.
// Body is a discriminated JSON object with a `t` field.
type UpdateEvent struct {
	// ID is the unique update id.
	ID string `json:"id"`
	// Seq is the user-scoped update sequence number.
	Seq int64 `json:"seq"`
	// Body is the typed update payload.
	Body any `json:"body"`
	// CreatedAt is a wall-clock timestamp in milliseconds since epoch.
	CreatedAt int64 `json:"createdAt"`
}

// EncryptedEnvelope is the ciphertext wrapper stored on the server and sent in
// updates.
type EncryptedEnvelope struct {
	// T is the envelope type (currently "encrypted").
	T string `json:"t"`
	// C is the ciphertext.
	C string `json:"c"`
}

// VersionedString is a versioned string value used for optimistic concurrency.
type VersionedString struct {
	// Value is the string payload.
	Value string `json:"value"`
	// Version is the monotonic version.
	Version int64 `json:"version"`
}

// UpdateBodyNewMessage is the body for `t == "new-message"`.
type UpdateBodyNewMessage struct {
	// T must be "new-message".
	T string `json:"t"`
	// SID is the session id.
	SID string `json:"sid"`
	// Message is the message payload.
	Message UpdateNewMessage `json:"message"`
}

// UpdateNewMessage is the message payload inside a new-message update.
type UpdateNewMessage struct {
	// ID is the message id.
	ID string `json:"id"`
	// Seq is the session-scoped message sequence.
	Seq int64 `json:"seq"`
	// LocalID is the client idempotency key; null when absent.
	LocalID *string `json:"localId"`
	// Content is the encrypted message envelope.
	Content EncryptedEnvelope `json:"content"`
	// CreatedAt is the message creation time in ms since epoch.
	CreatedAt int64 `json:"createdAt"`
	// UpdatedAt is the message update time in ms since epoch.
	UpdatedAt int64 `json:"updatedAt"`
}

// UpdateBodyUpdateSession is the body for `t == "update-session"`.
type UpdateBodyUpdateSession struct {
	// T must be "update-session".
	T string `json:"t"`
	// ID is the session id.
	ID string `json:"id"`
	// Metadata is the updated encrypted metadata.
	Metadata *VersionedString `json:"metadata,omitempty"`
	// AgentState is the updated encrypted agent state.
	AgentState *VersionedString `json:"agentState,omitempty"`
}
