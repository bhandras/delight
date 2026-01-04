package agentengine

import (
	"context"
	"encoding/json"
)

// AgentType identifies which upstream agent implementation should be started.
type AgentType string

const (
	// AgentClaude selects the Claude Code implementation.
	AgentClaude AgentType = "claude"
	// AgentCodex selects the OpenAI Codex implementation.
	AgentCodex AgentType = "codex"
	// AgentACP selects the ACP-backed implementation.
	AgentACP AgentType = "acp"
	// AgentFake selects the in-process fake agent implementation.
	AgentFake AgentType = "fake"
)

// Mode indicates whether the session is controlled locally (desktop) or remotely
// (phone).
type Mode string

const (
	// ModeLocal means the desktop owns the interactive loop.
	ModeLocal Mode = "local"
	// ModeRemote means the phone owns the interactive loop via a structured protocol.
	ModeRemote Mode = "remote"
)

// EngineStartSpec configures an agent engine start.
type EngineStartSpec struct {
	// Agent is the upstream agent type to start.
	Agent AgentType
	// WorkDir is the effective working directory for the underlying process.
	WorkDir string
	// Mode selects local vs remote behavior.
	Mode Mode

	// ResumeToken is an agent-specific session identifier that can be used to
	// resume an existing session.
	ResumeToken string
	// RolloutPath is an agent-specific event-log path (Codex rollout JSONL).
	RolloutPath string

	// Config holds durable agent configuration (model, permission mode, etc.).
	Config AgentConfig
}

// AgentConfig represents durable, session-scoped agent configuration.
//
// Fields are engine-specific best-effort; engines must tolerate empty values
// (meaning "use default").
type AgentConfig struct {
	// Model selects the upstream model identifier (engine-specific).
	Model string
	// PermissionMode selects the approval/sandbox preset for the engine.
	//
	// Canonical Delight values are: default|read-only|safe-yolo|yolo.
	PermissionMode string
	// ReasoningEffort selects the reasoning effort preset (Codex-specific).
	//
	// Canonical Codex values are: minimal|low|medium|high|xhigh.
	ReasoningEffort string
}

// AgentCapabilities describes which settings are supported by an engine.
type AgentCapabilities struct {
	// Models is the list of supported model identifiers (if known).
	Models []string
	// PermissionModes is the list of supported permission modes (if supported).
	PermissionModes []string
	// ReasoningEfforts is the list of supported reasoning effort presets (if supported).
	ReasoningEfforts []string
}

// UserMessage is a single user-authored message to send to an engine.
type UserMessage struct {
	// Text is the user-visible input text.
	Text string
	// Meta is an optional structured metadata object (e.g. model hints).
	Meta map[string]any
	// LocalID is the client-generated identifier used for dedupe.
	LocalID string
	// AtMs is the wall-clock timestamp (unix millis) associated with the message.
	AtMs int64
}

// PermissionDecision represents a decision for a tool permission prompt.
type PermissionDecision struct {
	// Allow reports whether the tool is approved to run.
	Allow bool
	// Message is an optional user-visible explanation.
	Message string
	// UpdatedInput is an optional JSON-encoded tool input override.
	UpdatedInput json.RawMessage
}

// PermissionRequester blocks until a user resolves a tool permission prompt.
//
// Engines use this interface in remote mode to route permission prompts to the
// phone and wait for a decision without embedding any UI logic.
type PermissionRequester interface {
	AwaitPermission(ctx context.Context, requestID string, toolName string, input json.RawMessage, nowMs int64) (PermissionDecision, error)
}

// Event is a marker interface for engine-emitted events.
type Event interface {
	isAgentEngineEvent()
}

// UIEventKind identifies the kind of UI event emitted by an engine.
type UIEventKind string

const (
	// UIEventThinking indicates a thinking status/log event.
	UIEventThinking UIEventKind = "thinking"
	// UIEventTool indicates a tool lifecycle event.
	UIEventTool UIEventKind = "tool"
)

// UIEventPhase indicates where in its lifecycle a UI event is.
type UIEventPhase string

const (
	// UIEventPhaseStart indicates the event just started.
	UIEventPhaseStart UIEventPhase = "start"
	// UIEventPhaseUpdate indicates the event progressed but has not finished.
	UIEventPhaseUpdate UIEventPhase = "update"
	// UIEventPhaseEnd indicates the event finished.
	UIEventPhaseEnd UIEventPhase = "end"
)

// UIEventStatus describes the best-effort status for a UI event.
type UIEventStatus string

const (
	// UIEventStatusRunning indicates the work is in progress.
	UIEventStatusRunning UIEventStatus = "running"
	// UIEventStatusOK indicates the work completed successfully.
	UIEventStatusOK UIEventStatus = "ok"
	// UIEventStatusError indicates the work failed.
	UIEventStatusError UIEventStatus = "error"
	// UIEventStatusCanceled indicates the work was canceled/aborted.
	UIEventStatusCanceled UIEventStatus = "canceled"
)

// EvUIEvent is an engine-emitted transient UI signal.
//
// Engines render brief/full Markdown strings so clients can display tool and
// thinking updates without parsing engine-specific payloads.
type EvUIEvent struct {
	// Mode indicates which mode runner produced the event.
	Mode Mode
	// EventID uniquely identifies the UI event so clients can update it in-place.
	EventID string
	// Kind identifies which category this UI event represents.
	Kind UIEventKind
	// Phase indicates lifecycle progress for the event.
	Phase UIEventPhase
	// Status indicates success/failure/progress best-effort.
	Status UIEventStatus
	// BriefMarkdown is the brief rendering of the event (gist/one-liner).
	BriefMarkdown string
	// FullMarkdown is the detailed rendering of the event.
	FullMarkdown string
	// AtMs is the wall-clock timestamp (unix millis) when the event was observed.
	AtMs int64
}

// isAgentEngineEvent marks EvUIEvent as an Event.
func (EvUIEvent) isAgentEngineEvent() {}

// EvThinking indicates whether an engine is currently working on a request.
//
// This is emitted as an ephemeral UI signal (e.g. for "thinking…" indicators).
// It should not be persisted as durable session state.
type EvThinking struct {
	// Mode indicates which mode runner the thinking signal applies to.
	Mode Mode
	// Thinking is true while the engine is processing a turn.
	Thinking bool
	// AtMs is the wall-clock timestamp (unix millis) when the state was observed.
	AtMs int64
}

// isAgentEngineEvent marks EvThinking as an Event.
func (EvThinking) isAgentEngineEvent() {}

// EvReady indicates the engine process/protocol is ready to accept input.
type EvReady struct {
	// Mode indicates whether the engine is ready in local or remote mode.
	Mode Mode
}

// isAgentEngineEvent marks EvReady as an Event.
func (EvReady) isAgentEngineEvent() {}

// EvExited indicates the engine stopped.
type EvExited struct {
	// Mode indicates which mode runner exited.
	Mode Mode
	// Err is the process exit error, if any.
	Err error
}

// isAgentEngineEvent marks EvExited as an Event.
func (EvExited) isAgentEngineEvent() {}

// EvOutboundRecord indicates the engine produced a plaintext record that should
// be encrypted and sent to the server.
type EvOutboundRecord struct {
	// Mode indicates which mode produced the record.
	Mode Mode
	// LocalID is the record-local identifier, used for dedupe and correlation.
	LocalID string
	// Payload is the plaintext JSON to encrypt (raw record bytes).
	Payload []byte

	// UserTextNormalized is set for role=user text records to suppress echoes.
	UserTextNormalized string
	// AtMs is the wall-clock timestamp (unix millis) generated by the engine.
	AtMs int64
}

// isAgentEngineEvent marks EvOutboundRecord as an Event.
func (EvOutboundRecord) isAgentEngineEvent() {}

// EvSessionIdentified carries an agent-specific resume token.
type EvSessionIdentified struct {
	// Mode indicates which mode identified the token.
	Mode Mode
	// ResumeToken is the agent session identifier (e.g. Codex UUID).
	ResumeToken string
}

// isAgentEngineEvent marks EvSessionIdentified as an Event.
func (EvSessionIdentified) isAgentEngineEvent() {}

// EvRolloutPath carries an agent-specific event-log path (Codex rollout JSONL).
type EvRolloutPath struct {
	// Mode indicates which mode owns the rollout path.
	Mode Mode
	// Path is the absolute path to the rollout JSONL file.
	Path string
}

// isAgentEngineEvent marks EvRolloutPath as an Event.
func (EvRolloutPath) isAgentEngineEvent() {}

// AgentEngine is the agent-specific runtime used by SessionActor.
//
// Implementations are responsible for process/protocol lifecycle and for
// emitting structured events via Events(). They must not mutate SessionActor
// state directly.
type AgentEngine interface {
	// Start launches the engine in the requested mode.
	Start(ctx context.Context, spec EngineStartSpec) error
	// Stop stops the engine behavior for the given mode.
	//
	// Implementations may choose to keep shared background processes alive when
	// switching modes (e.g. Codex MCP) as long as SendUserMessage semantics match
	// the requested control mode.
	Stop(ctx context.Context, mode Mode) error
	// Close fully shuts down the engine and releases underlying resources.
	Close(ctx context.Context) error
	// SendUserMessage sends a user-authored message to the engine.
	SendUserMessage(ctx context.Context, msg UserMessage) error
	// Abort aborts an in-flight turn in remote mode (best-effort).
	Abort(ctx context.Context) error

	// Capabilities reports which configuration knobs are supported.
	Capabilities() AgentCapabilities
	// CurrentConfig reports the engine's current configuration (best-effort).
	//
	// Implementations should return the most recently applied config even when
	// not currently running, so callers can render UI without forcing a start.
	CurrentConfig() AgentConfig
	// ApplyConfig applies session-scoped configuration to subsequent turns.
	ApplyConfig(ctx context.Context, cfg AgentConfig) error

	// Events returns a channel of engine events. Implementations must not block
	// indefinitely on sends to this channel.
	Events() <-chan Event
	// Wait blocks until the engine exits and returns its exit error.
	Wait() error
}
