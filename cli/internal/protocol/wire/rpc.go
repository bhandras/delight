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
