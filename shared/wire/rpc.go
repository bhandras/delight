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

// AttachmentUploadBeginRequest starts an attachment upload stream for the
// current session.
type AttachmentUploadBeginRequest struct {
	// UploadID is a client-generated unique upload identifier.
	UploadID string `json:"uploadId"`
	// FileName is the original file name (best-effort).
	FileName string `json:"fileName"`
	// MIMEType is the best-effort media type.
	MIMEType string `json:"mimeType"`
	// SizeBytes is the expected total file size in bytes.
	SizeBytes int64 `json:"sizeBytes"`
}

// AttachmentUploadChunkRequest appends one chunk of file data to an upload.
type AttachmentUploadChunkRequest struct {
	// UploadID identifies the upload initialized by begin.
	UploadID string `json:"uploadId"`
	// ChunkIndex is the zero-based chunk ordinal.
	ChunkIndex int64 `json:"chunkIndex"`
	// DataBase64 is the chunk payload as base64 bytes.
	DataBase64 string `json:"dataBase64"`
}

// AttachmentUploadCommitRequest finalizes an upload and returns a local path.
type AttachmentUploadCommitRequest struct {
	// UploadID identifies the upload initialized by begin.
	UploadID string `json:"uploadId"`
}

// AttachmentUploadCancelRequest cancels and deletes an in-flight upload.
type AttachmentUploadCancelRequest struct {
	// UploadID identifies the upload initialized by begin.
	UploadID string `json:"uploadId"`
}

// AttachmentUploadResponse is a generic response for upload RPC methods.
type AttachmentUploadResponse struct {
	// Success indicates whether the operation succeeded.
	Success bool `json:"success"`
	// Error contains a user-visible error message on failure.
	Error string `json:"error,omitempty"`
	// UploadID echoes the upload identifier when relevant.
	UploadID string `json:"uploadId,omitempty"`
	// Path is the finalized local filesystem path after commit.
	Path string `json:"path,omitempty"`
	// BytesReceived is the total bytes written so far for begin/chunk/commit.
	BytesReceived int64 `json:"bytesReceived,omitempty"`
}

// SetAgentConfigRequest updates durable agent configuration for a session.
//
// This is a session-scoped RPC (mobile -> server -> CLI). Values are optional;
// empty strings mean "no change" for that field.
type SetAgentConfigRequest struct {
	// Model selects the upstream model identifier (engine-specific).
	Model string `json:"model,omitempty"`
	// ReasoningEffort selects the reasoning effort preset (Codex-specific).
	//
	// Canonical Codex values are: minimal|low|medium|high|xhigh.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// PermissionMode selects the approval/sandbox policy preset.
	//
	// Canonical Delight values are: default|read-only|safe-yolo|yolo.
	PermissionMode string `json:"permissionMode,omitempty"`
}

// SetAgentConfigResponse reports the result of applying an agent configuration
// update request.
type SetAgentConfigResponse struct {
	// Success indicates whether the operation succeeded.
	Success bool `json:"success"`
	// AgentState is the updated durable agentState JSON (best-effort).
	AgentState string `json:"agentState,omitempty"`
	// AgentStateVersion is the server version of agentState after persistence.
	AgentStateVersion int64 `json:"agentStateVersion,omitempty"`
	// Error contains an error message when Success is false.
	Error string `json:"error,omitempty"`
}

// AgentCapabilitiesRequest requests the current engine capabilities and
// configuration for a session.
//
// This is a session-scoped RPC (mobile -> server -> CLI). The request is
// intentionally best-effort; the session id in the method name scopes the
// lookup.
type AgentCapabilitiesRequest struct {
	// Model optionally requests capabilities for the provided model selection.
	//
	// This is intended for UI flows that need to refresh dependent settings
	// (e.g. reasoning effort presets) when the user highlights a model, without
	// applying the config to the live session.
	Model string `json:"model,omitempty"`
}

// AgentCapabilities describes which settings can be configured for a session.
type AgentCapabilities struct {
	// Models is the list of supported model identifiers (if known).
	Models []string `json:"models,omitempty"`
	// PermissionModes is the list of supported permission mode presets.
	PermissionModes []string `json:"permissionModes,omitempty"`
	// ReasoningEfforts is the list of supported reasoning effort presets.
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
}

// AgentConfig represents a session-scoped agent configuration snapshot.
type AgentConfig struct {
	// Model selects the upstream model identifier (engine-specific).
	Model string `json:"model,omitempty"`
	// ReasoningEffort selects the reasoning effort preset (Codex-specific).
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// PermissionMode selects the approval/sandbox policy preset.
	PermissionMode string `json:"permissionMode,omitempty"`
}

// AgentCapabilitiesResponse reports the session's agent configuration and the
// engine's supported settings.
type AgentCapabilitiesResponse struct {
	// Success indicates whether the operation succeeded.
	Success bool `json:"success"`
	// AgentType is the session's current agent type (e.g. "codex", "claude").
	AgentType string `json:"agentType,omitempty"`
	// Capabilities is the supported knobs for this agent.
	Capabilities AgentCapabilities `json:"capabilities,omitempty"`
	// DesiredConfig is the durable session config stored in agentState.
	DesiredConfig AgentConfig `json:"desiredConfig,omitempty"`
	// EffectiveConfig is the engine-reported current config (best-effort).
	EffectiveConfig AgentConfig `json:"effectiveConfig,omitempty"`
	// Error contains an error message when Success is false.
	Error string `json:"error,omitempty"`
}

// ThreadGoalStatus identifies the lifecycle state of a Codex thread goal.
type ThreadGoalStatus string

const (
	// ThreadGoalStatusActive means Codex should continue working toward the goal.
	ThreadGoalStatusActive ThreadGoalStatus = "active"
	// ThreadGoalStatusPaused means goal continuation is paused by the user.
	ThreadGoalStatusPaused ThreadGoalStatus = "paused"
	// ThreadGoalStatusBudgetLimited means the goal stopped because its budget was reached.
	ThreadGoalStatusBudgetLimited ThreadGoalStatus = "budgetLimited"
	// ThreadGoalStatusComplete means Codex marked the goal achieved.
	ThreadGoalStatusComplete ThreadGoalStatus = "complete"
)

// ThreadGoalAction identifies a mobile goal command.
type ThreadGoalAction string

const (
	// ThreadGoalActionGet fetches the current goal without changing it.
	ThreadGoalActionGet ThreadGoalAction = "get"
	// ThreadGoalActionSet sets or edits the current goal objective.
	ThreadGoalActionSet ThreadGoalAction = "set"
	// ThreadGoalActionPause pauses an active goal.
	ThreadGoalActionPause ThreadGoalAction = "pause"
	// ThreadGoalActionResume resumes a paused or stopped goal.
	ThreadGoalActionResume ThreadGoalAction = "resume"
	// ThreadGoalActionClear removes the current goal.
	ThreadGoalActionClear ThreadGoalAction = "clear"
)

// ThreadGoal describes Codex's persisted goal metadata for a thread.
type ThreadGoal struct {
	// ThreadID is the Codex app-server thread id.
	ThreadID string `json:"threadId"`
	// Objective is the user-visible goal text.
	Objective string `json:"objective"`
	// Status is the goal lifecycle state.
	Status ThreadGoalStatus `json:"status"`
	// TokenBudget is the optional maximum token budget for the goal.
	TokenBudget *int64 `json:"tokenBudget,omitempty"`
	// TokensUsed is the number of tokens accounted to the goal so far.
	TokensUsed int64 `json:"tokensUsed"`
	// TimeUsedSeconds is wall-clock time accounted to the goal so far.
	TimeUsedSeconds int64 `json:"timeUsedSeconds"`
	// CreatedAt is the upstream Unix timestamp when the goal was created.
	CreatedAt int64 `json:"createdAt"`
	// UpdatedAt is the upstream Unix timestamp when the goal was last changed.
	UpdatedAt int64 `json:"updatedAt"`
}

// ThreadGoalRequest requests a goal operation for a session's Codex thread.
type ThreadGoalRequest struct {
	// Action identifies which goal operation to run.
	Action ThreadGoalAction `json:"action"`
	// Objective is required for the set action.
	Objective string `json:"objective,omitempty"`
}

// ThreadGoalResponse reports the result of a goal operation.
type ThreadGoalResponse struct {
	// Success indicates whether the operation succeeded.
	Success bool `json:"success"`
	// Goal is the current goal after get/set/pause/resume, if one exists.
	Goal *ThreadGoal `json:"goal,omitempty"`
	// Cleared indicates whether clear removed an existing goal.
	Cleared bool `json:"cleared,omitempty"`
	// Message is a short user-visible summary.
	Message string `json:"message,omitempty"`
	// Error contains an error message when Success is false.
	Error string `json:"error,omitempty"`
}

// Terminal-scoped RPC payloads (server -> terminal).

// SpawnSessionRequest requests starting a new CLI session for a directory.
type SpawnSessionRequest struct {
	// Directory is the working directory for the new session.
	Directory string `json:"directory"`
	// ApprovedNewDirectoryCreation indicates the user has approved mkdir.
	ApprovedNewDirectoryCreation bool `json:"approvedNewDirectoryCreation"`
	// SessionID is the existing session id (if any) associated with the request.
	SessionID string `json:"sessionId"`
	// TerminalID is the terminal id for the CLI.
	TerminalID string `json:"terminalId"`
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

// RestartDaemonResponse is the response payload for the "restart-daemon" RPC.
type RestartDaemonResponse struct {
	// Message is a human-readable status message.
	Message string `json:"message"`
}

// PingResponse is the response payload for the "ping" RPC.
type PingResponse struct {
	// Success indicates whether the daemon is reachable.
	Success bool `json:"success"`
}

// SpawnSessionResponse requests or reports the result of spawning a
// session from a terminal-scoped RPC call.
type SpawnSessionResponse struct {
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
