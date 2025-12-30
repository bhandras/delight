package wire

// Session-scoped RPC payloads (mobile -> server -> CLI).

// SwitchModeRequest requests switching between local/remote mode.
type SwitchModeRequest struct {
	// Mode is the target mode ("local" or "remote").
	Mode string `json:"mode"`
}

// PermissionResponseRequest sends a user's permission decision back to the CLI.
type PermissionResponseRequest struct {
	// RequestID is the permission request identifier.
	RequestID string `json:"requestId"`
	// Allow indicates whether the request is approved.
	Allow bool `json:"allow"`
	// Message is an optional justification/annotation.
	Message string `json:"message"`
}

// Machine-scoped RPC payloads (server -> daemon).

// SpawnHappySessionRequest requests starting a new CLI session for a directory.
type SpawnHappySessionRequest struct {
	// Directory is the working directory for the new session.
	Directory string `json:"directory"`
	// ApprovedNewDirectoryCreation indicates the user has approved mkdir.
	ApprovedNewDirectoryCreation bool `json:"approvedNewDirectoryCreation"`
	// SessionID is the existing session id (if any) associated with the request.
	SessionID string `json:"sessionId"`
	// MachineID is the machine id for the daemon.
	MachineID string `json:"machineId"`
	// Agent is the agent flavor ("claude" or "codex").
	Agent string `json:"agent"`
}

// StopSessionRequest requests stopping an already spawned session.
type StopSessionRequest struct {
	// SessionID is the id of the session to stop.
	SessionID string `json:"sessionId"`
}

// SuccessResponse is a generic JSON response payload with a "success" key.
type SuccessResponse struct {
	// Success indicates whether the operation succeeded.
	Success bool `json:"success"`
}

// ErrorResponse is a generic JSON response payload with an "error" key.
type ErrorResponse struct {
	// Error contains an error message.
	Error string `json:"error"`
}

// StopSessionResponse is the response payload for the "stop-session" RPC.
type StopSessionResponse struct {
	// Message is a human-readable status message.
	Message string `json:"message"`
}

// StopDaemonResponse is the response payload for the "stop-daemon" RPC.
type StopDaemonResponse struct {
	// Message is a human-readable status message.
	Message string `json:"message"`
}

// PingResponse is the response payload for the "ping" RPC.
type PingResponse struct {
	// Success indicates whether the daemon is reachable.
	Success bool `json:"success"`
}

// SpawnHappySessionResponse requests or reports the result of spawning a
// session from a machine-scoped RPC call.
type SpawnHappySessionResponse struct {
	// Type identifies the response kind (e.g. "success").
	Type string `json:"type"`
	// SessionID is the id of the spawned session on success.
	SessionID string `json:"sessionId,omitempty"`
	// Directory is the directory path requiring approval when present.
	Directory string `json:"directory,omitempty"`
}

// ACPAwaitInput is the JSON-encoded tool input used for "acp.await"
// permission requests.
type ACPAwaitInput struct {
	// Await is the ACP await_request object.
	Await map[string]any `json:"await"`
}

// ACPAwaitResume is the JSON payload sent to ACP to resume an awaiting run.
type ACPAwaitResume struct {
	// Allow indicates whether the user approved the await request.
	Allow bool `json:"allow"`
	// Message is an optional user-provided message.
	Message string `json:"message"`
}
