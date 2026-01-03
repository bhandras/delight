package types

import (
	"fmt"
	"time"
)

// NewCUID generates a unique identifier (simplified version).
func NewCUID() string {
	return fmt.Sprintf("c%d", time.Now().UnixNano())
}

// Metadata represents session metadata (decrypted)
type Metadata struct {
	Path            string `json:"path"`
	Host            string `json:"host"`
	Version         string `json:"version"`
	OS              string `json:"os"`
	TerminalID      string `json:"terminalId"`
	HomeDir         string `json:"homeDir"`
	DelightHomeDir  string `json:"delightHomeDir"`
	Flavor          string `json:"flavor,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
}

// AgentState represents agent state (decrypted)
type AgentState struct {
	// AgentType identifies which upstream engine implementation is active for
	// this session (e.g. "codex", "claude", "acp", "fake").
	AgentType string `json:"agentType,omitempty"`

	// ControlledByUser reports whether the desktop currently controls the
	// session's agent loop.
	ControlledByUser bool `json:"controlledByUser"`

	// Model is the engine-specific model identifier selected for this session.
	//
	// An empty string means "use engine default".
	Model string `json:"model,omitempty"`

	// ReasoningEffort is the reasoning effort preset selected for this session.
	//
	// This is primarily used by Codex and maps to `model_reasoning_effort`:
	// minimal|low|medium|high|xhigh.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`

	// PermissionMode selects the session's permission/approval mode.
	//
	// Canonical Delight values are: default|read-only|safe-yolo|yolo.
	// An empty string means "default".
	PermissionMode string `json:"permissionMode,omitempty"`

	// Requests contains pending permission requests keyed by request id.
	Requests map[string]AgentPendingRequest `json:"requests,omitempty"`

	// CompletedRequests contains a best-effort history of resolved permission
	// requests keyed by request id.
	CompletedRequests map[string]AgentCompletedRequest `json:"completedRequests,omitempty"`
}

// AgentPendingRequest stores a permission prompt that is awaiting a user
// decision. This is persisted in agent state so mobile clients can recover
// after reconnect.
type AgentPendingRequest struct {
	// ToolName is the tool being requested (e.g. "can_use_tool").
	ToolName string `json:"toolName"`
	// Input is the JSON-encoded tool input string.
	Input string `json:"input"`
	// CreatedAt is the wall-clock timestamp (ms since epoch) when the request
	// was first observed.
	CreatedAt int64 `json:"createdAt"`
}

// AgentCompletedRequest stores a resolved permission request for debugging and
// UI reconciliation.
type AgentCompletedRequest struct {
	// ToolName is the tool that was requested.
	ToolName string `json:"toolName"`
	// Input is the JSON-encoded tool input string.
	Input string `json:"input"`
	// Allow reports whether the request was approved.
	Allow bool `json:"allow"`
	// Message is an optional user-supplied note.
	Message string `json:"message,omitempty"`
	// ResolvedAt is the wall-clock timestamp (ms since epoch) when the request
	// was resolved.
	ResolvedAt int64 `json:"resolvedAt"`
}

// UserMessage represents a message from the user
type UserMessage struct {
	Role    string                 `json:"role"`
	Content map[string]interface{} `json:"content"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
}

// AgentMessage represents a message from Claude
type AgentMessage struct {
	Role    string                 `json:"role"`
	Content map[string]interface{} `json:"content"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
}

// TerminalMetadata represents terminal metadata.
type TerminalMetadata struct {
	Host              string `json:"host"`
	Platform          string `json:"platform"`
	DelightCliVersion string `json:"cliVersion"`
	HomeDir           string `json:"homeDir"`
	DelightHomeDir    string `json:"delightHomeDir"`
}

// DaemonState represents daemon runtime state
type DaemonState struct {
	Status    string `json:"status"` // "running" | "offline" | "shutting-down"
	PID       int    `json:"pid,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
}
