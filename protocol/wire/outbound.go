package wire

import "encoding/json"

// OutboundMessagePayload is the client -> server payload for the Socket.IO
// "message" event.
type OutboundMessagePayload struct {
	// SID is the session id for the target session.
	SID string `json:"sid"`
	// LocalID is an optional idempotency key for optimistic reconciliation.
	LocalID string `json:"localId,omitempty"`
	// Message is the ciphertext envelope as a base64 string.
	Message string `json:"message"`
}

// UnmarshalJSON accepts legacy field names seen across clients.
//
// Observed variants:
// - sid vs sessionId
// - message vs content
func (p *OutboundMessagePayload) UnmarshalJSON(data []byte) error {
	type compat struct {
		SID       string `json:"sid"`
		SessionID string `json:"sessionId"`
		LocalID   string `json:"localId"`
		Message   string `json:"message"`
		Content   string `json:"content"`
	}
	var tmp compat
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	p.SID = tmp.SID
	if p.SID == "" {
		p.SID = tmp.SessionID
	}
	p.LocalID = tmp.LocalID
	p.Message = tmp.Message
	if p.Message == "" {
		p.Message = tmp.Content
	}
	return nil
}

// SessionAlivePayload is the client -> server payload for the "session-alive"
// event.
type SessionAlivePayload struct {
	// SID is the session id for the keep-alive.
	SID string `json:"sid"`
	// Time is a wall-clock timestamp in milliseconds since epoch.
	Time int64 `json:"time"`
	// Thinking indicates whether the session is currently busy.
	Thinking bool `json:"thinking"`
}

// MachineAlivePayload is the client -> server payload for the "machine-alive"
// event.
type MachineAlivePayload struct {
	// MachineID identifies the machine emitting the keep-alive.
	MachineID string `json:"machineId"`
	// Time is a wall-clock timestamp in milliseconds since epoch.
	Time int64 `json:"time"`
}

// EphemeralActivityPayload is the payload for a user-scoped "ephemeral" event
// of type "activity".
type EphemeralActivityPayload struct {
	// Type must be "activity".
	Type string `json:"type"`
	// ID is the session id the activity corresponds to.
	ID string `json:"id"`
	// Active is true when the session is active.
	Active bool `json:"active"`
	// Thinking indicates whether the session is currently busy.
	Thinking bool `json:"thinking"`
	// ActiveAt is a wall-clock timestamp in milliseconds since epoch.
	ActiveAt int64 `json:"activeAt"`
}

// PermissionRequestPayload is the client -> server payload for the
// "permission-request" event.
type PermissionRequestPayload struct {
	// SID is the session id requesting permission.
	SID string `json:"sid"`
	// RequestID correlates the request/response pair.
	RequestID string `json:"requestId"`
	// ToolName is the tool being requested (e.g. "acp.await").
	ToolName string `json:"toolName"`
	// Input is a JSON-encoded string payload for the tool input.
	Input string `json:"input"`
}

// PermissionRequestEphemeralPayload is the payload for a user-scoped
// "ephemeral" event of type "permission-request".
type PermissionRequestEphemeralPayload struct {
	// Type must be "permission-request".
	Type string `json:"type"`
	// ID is the session id the permission request corresponds to.
	ID string `json:"id"`
	// RequestID correlates the request/response pair.
	RequestID string `json:"requestId"`
	// ToolName is the tool being requested (e.g. "acp.await").
	ToolName string `json:"toolName"`
	// Input is a JSON-encoded string payload for the tool input.
	Input string `json:"input"`
}

// UsageReportPayload is the client -> server payload for the "usage-report"
// event.
type UsageReportPayload struct {
	// Key identifies the source of the usage report.
	Key string `json:"key"`
	// SessionID identifies the session the usage report corresponds to.
	SessionID string `json:"sessionId"`
	// Tokens contains token counts.
	Tokens UsageReportTokens `json:"tokens"`
	// Cost contains cost information when available.
	Cost UsageReportCost `json:"cost"`
}

// UsageReportTokens contains token counts for a usage report.
type UsageReportTokens struct {
	// Total is the total tokens across all subcategories.
	Total int `json:"total"`
	// Input is input tokens.
	Input int `json:"input"`
	// Output is output tokens.
	Output int `json:"output"`
	// CacheCreation is cache creation tokens.
	CacheCreation int `json:"cache_creation"`
	// CacheRead is cache read tokens.
	CacheRead int `json:"cache_read"`
}

// UsageReportCost contains cost information for a usage report.
type UsageReportCost struct {
	// Total is total cost.
	Total float64 `json:"total"`
	// Input is input cost.
	Input float64 `json:"input"`
	// Output is output cost.
	Output float64 `json:"output"`
}

// UpdateMetadataPayload is the client -> server payload for the
// "update-metadata" event.
type UpdateMetadataPayload struct {
	// SID is the session id to update.
	SID string `json:"sid"`
	// Metadata is the encrypted metadata payload.
	Metadata string `json:"metadata"`
	// ExpectedVersion is the optimistic concurrency version.
	ExpectedVersion int64 `json:"expectedVersion"`
}

// UpdateStatePayload is the client -> server payload for the "update-state"
// event.
type UpdateStatePayload struct {
	// SID is the session id to update.
	SID string `json:"sid"`
	// AgentState is the encrypted agent state payload.
	AgentState string `json:"agentState"`
	// ExpectedVersion is the optimistic concurrency version.
	ExpectedVersion int64 `json:"expectedVersion"`
}

// SocketAuthPayload is the client -> server Socket.IO auth payload used during
// handshake for session-, machine-, and user-scoped sockets.
type SocketAuthPayload struct {
	// Token is the bearer token for the authenticated user.
	Token string `json:"token"`
	// ClientType selects the connection scope ("user-scoped", "session-scoped",
	// or "machine-scoped").
	ClientType string `json:"clientType"`
	// SessionID scopes a session-scoped socket.
	SessionID string `json:"sessionId,omitempty"`
	// MachineID scopes a machine-scoped socket.
	MachineID string `json:"machineId,omitempty"`
}

// RPCRegisterPayload is the client -> server payload for the "rpc-register"
// event.
type RPCRegisterPayload struct {
	// Method is the RPC method name being registered.
	Method string `json:"method"`
}

// MachineUpdateStatePayload is the client -> server payload for the
// "machine-update-state" event.
type MachineUpdateStatePayload struct {
	// MachineID identifies the machine being updated.
	MachineID string `json:"machineId"`
	// DaemonState is the encrypted daemon state payload.
	DaemonState string `json:"daemonState"`
	// ExpectedVersion is the optimistic concurrency version.
	ExpectedVersion int64 `json:"expectedVersion"`
}

// MachineUpdateMetadataPayload is the client -> server payload for the
// "machine-update-metadata" event.
type MachineUpdateMetadataPayload struct {
	// MachineID identifies the machine being updated.
	MachineID string `json:"machineId"`
	// Metadata is the encrypted machine metadata payload.
	Metadata string `json:"metadata"`
	// ExpectedVersion is the optimistic concurrency version.
	ExpectedVersion int64 `json:"expectedVersion"`
}
