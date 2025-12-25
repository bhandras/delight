package websocket

import (
	socketio "github.com/googollee/go-socket.io"
)

// ConnectionType represents the type of WebSocket connection
type ConnectionType string

const (
	ConnectionTypeUserScoped    ConnectionType = "user-scoped"
	ConnectionTypeSessionScoped ConnectionType = "session-scoped"
	ConnectionTypeMachineScoped ConnectionType = "machine-scoped"
)

// ClientConnection represents a connected client
type ClientConnection struct {
	Type      ConnectionType
	Socket    socketio.Conn
	UserID    string
	SessionID string // Only for session-scoped
	MachineID string // Only for machine-scoped
}

// AuthData represents the authentication data sent during connection
type AuthData struct {
	Token      string `json:"token"`
	ClientType string `json:"clientType"`
	SessionID  string `json:"sessionId,omitempty"`
	MachineID  string `json:"machineId,omitempty"`
}

// UpdatePayload represents a persistent update event
type UpdatePayload struct {
	ID        string      `json:"id"`
	Seq       int64       `json:"seq"`
	Body      interface{} `json:"body"`
	CreatedAt int64       `json:"createdAt"`
}

// EphemeralPayload represents an ephemeral event (not persisted)
type EphemeralPayload struct {
	Type     string      `json:"type"`
	ID       string      `json:"id"`
	Active   bool        `json:"active"`
	ActiveAt int64       `json:"activeAt"`
	Thinking *bool       `json:"thinking,omitempty"`
	Data     interface{} `json:"data,omitempty"`
}

// RecipientFilter defines who should receive an update
type RecipientFilter struct {
	Type      string
	SessionID string
}

const (
	FilterAllInterestedInSession = "all-interested-in-session"
	FilterUserScopedOnly         = "user-scoped-only"
	FilterAllUserAuthenticated   = "all-user-authenticated-connections"
)

// Message events

type MessageEvent struct {
	SessionID string `json:"sid" binding:"required"`
	Message   string `json:"message" binding:"required"` // Encrypted message content
	LocalID   string `json:"localId,omitempty"`          // Idempotency key
}

type SessionAliveEvent struct {
	SessionID string `json:"sid" binding:"required"`
	Time      int64  `json:"time" binding:"required"`
	Thinking  bool   `json:"thinking"`
}

type SessionEndEvent struct {
	SessionID string `json:"sid" binding:"required"`
	Time      int64  `json:"time" binding:"required"`
}

type UpdateMetadataEvent struct {
	SessionID       string `json:"sid" binding:"required"`
	Metadata        string `json:"metadata" binding:"required"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type UpdateStateEvent struct {
	SessionID       string  `json:"sid" binding:"required"`
	AgentState      *string `json:"agentState"`
	ExpectedVersion int64   `json:"expectedVersion"`
}

// Response types

type UpdateResponse struct {
	Result   string  `json:"result"` // "success", "version-mismatch", "error"
	Version  int64   `json:"version,omitempty"`
	Metadata *string `json:"metadata,omitempty"`
}
