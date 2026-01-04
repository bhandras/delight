package wire

// CreateSessionRequest is the HTTP POST /v1/sessions request body.
type CreateSessionRequest struct {
	// Tag is the stable client-generated session tag.
	Tag string `json:"tag"`
	// TerminalID is the client-stable terminal id that owns the session.
	TerminalID string `json:"terminalId"`
	// Metadata is the encrypted metadata payload (base64-encoded).
	Metadata string `json:"metadata"`
	// AgentState is the plaintext agent state JSON payload.
	//
	// This is optional, but sending it during session creation lets the server
	// update stale sessions (for example when the CLI restarts with a different
	// agent) before any websocket-based state persistence occurs.
	AgentState *string `json:"agentState,omitempty"`
	// DataEncryptionKey is the session data key (base64-encoded 32 bytes).
	//
	// When present, clients may use this key to encrypt session payloads using
	// AES-256-GCM.
	DataEncryptionKey *string `json:"dataEncryptionKey,omitempty"`
}

// CreateSessionResponse is the HTTP POST /v1/sessions response body.
type CreateSessionResponse struct {
	// Session contains the created session object.
	Session CreateSessionResponseSession `json:"session"`
}

// CreateSessionResponseSession is the session object returned in a
// CreateSessionResponse.
type CreateSessionResponseSession struct {
	// ID is the server-assigned session id.
	ID string `json:"id"`
	// DataEncryptionKey is the session data key (base64-encoded 32 bytes) when present.
	DataEncryptionKey *string `json:"dataEncryptionKey,omitempty"`
}

// CreateTerminalRequest is the HTTP POST /v1/terminals request body.
type CreateTerminalRequest struct {
	// ID is the client-stable terminal id.
	ID string `json:"id"`
	// Metadata is the encrypted terminal metadata payload (base64-encoded).
	Metadata string `json:"metadata"`
	// DaemonState is the encrypted daemon state payload (base64-encoded).
	DaemonState string `json:"daemonState"`
}

// CreateTerminalResponse is the HTTP POST /v1/terminals response body.
type CreateTerminalResponse struct {
	// Terminal contains the created/updated terminal object.
	Terminal CreateTerminalResponseTerminal `json:"terminal"`
}

// CreateTerminalResponseTerminal is the terminal object returned in a
// CreateTerminalResponse.
type CreateTerminalResponseTerminal struct {
	// MetadataVersion is the current terminal metadata version.
	MetadataVersion int64 `json:"metadataVersion"`
	// DaemonStateVersion is the current daemon state version.
	DaemonStateVersion int64 `json:"daemonStateVersion"`
}
