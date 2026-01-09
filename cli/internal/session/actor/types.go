package actor

import (
	"encoding/json"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/pkg/types"
)

// NOTE: This package intentionally starts as a type-only skeleton.
// Phase 2 goal: define the state machine vocabulary (state, inputs, effects)
// without wiring it into the production session manager yet.

// Mode is the user-visible control mode.
type Mode string

const (
	// ModeLocal means the desktop controls the session (interactive TUI).
	ModeLocal Mode = "local"
	// ModeRemote means the phone controls the session (SDK/bridge mode).
	ModeRemote Mode = "remote"
)

// FSMState is the internal session FSM state.
type FSMState string

const (
	// StateLocalStarting indicates the local runner is being started.
	StateLocalStarting FSMState = "LocalStarting"
	// StateLocalRunning indicates the local runner is active.
	StateLocalRunning FSMState = "LocalRunning"
	// StateRemoteStarting indicates the remote runner is being started.
	StateRemoteStarting FSMState = "RemoteStarting"
	// StateRemoteRunning indicates the remote runner is active.
	StateRemoteRunning FSMState = "RemoteRunning"
	// StateClosing indicates shutdown is in progress.
	StateClosing FSMState = "Closing"
	// StateClosed indicates the session actor has stopped.
	StateClosed FSMState = "Closed"
)

// State is the loop-owned state for the SessionActor.
type State struct {
	// SessionID is the owning session id. It is used for socket emission effects.
	SessionID string

	FSM FSMState

	Mode Mode

	// RunnerGen increments each time we (re)start a runner. Runtime completion
	// events must include the generation so stale exits/readies can be ignored.
	RunnerGen int64

	// LocalRunner summarizes the most recent local runner lifecycle observation.
	LocalRunner runnerHandle
	// RemoteRunner summarizes the most recent remote runner lifecycle observation.
	RemoteRunner runnerHandle

	// ResumeToken is an agent-specific session identifier that can be used to
	// resume a conversation (e.g. Claude session id, Codex session UUID).
	ResumeToken string

	// RolloutPath is an agent-specific event-log path used by local-mode viewers.
	// For Codex this is the rollout JSONL path.
	RolloutPath string

	// LastExitErr stores the most recently observed runner exit error string.
	// It is used by Manager.Wait() bridging during the refactor.
	LastExitErr string

	// PendingSwitchReply is completed when a switch finishes (runner ready) or
	// fails (runner exit during starting).
	PendingSwitchReply chan error

	// PendingPermissionPromises contains per-request channels to deliver
	// permission decisions back to synchronous callers (e.g. ACP/Codex runtime
	// adapters). The reducer must never block on these channels.
	PendingPermissionPromises map[string]chan PermissionDecision

	// AgentState is the durable state that is stored on the server as plaintext
	// JSON (mobile clients parse this JSON blob).
	AgentState types.AgentState

	// AgentStateJSON is the cached JSON form of AgentState, used for persistence
	// effects. When AgentState changes, the reducer should update this string.
	AgentStateJSON string

	// AgentStateVersion is the version used for optimistic concurrency control
	// when persisting agent state to the server.
	AgentStateVersion int64

	// PersistDebounceTimerArmed indicates a persist debounce timer is active.
	PersistDebounceTimerArmed bool

	// PersistRetryRemaining bounds retries on version-mismatch errors. Set to 1
	// when the reducer schedules a persist, and decremented on retry.
	PersistRetryRemaining int

	// PersistInFlight reports whether an agent state persistence request is
	// currently in flight.
	PersistInFlight bool

	// PersistWaiters contains callers waiting for agent state persistence to
	// complete. Reducers must never block on these channels.
	PersistWaiters []chan error

	// Connection state (observability + UI gating).
	WSConnected       bool
	TerminalConnected bool

	// Thinking reports whether the remote engine is currently processing a turn.
	// It is surfaced via session keep-alives and activity ephemerals so mobile
	// clients can show a live "thinking" indicator.
	Thinking bool

	// Dedupe windows (populated by events) to suppress message echoes.
	RecentRemoteInputs         []remoteInputRecord
	RecentOutboundUserLocalIDs []outboundLocalIDRecord

	// PendingRemoteSends buffers inbound user messages while switching to (or
	// starting) remote mode.
	PendingRemoteSends []pendingRemoteSend
}

type remoteInputRecord struct {
	text string
	atMs int64
}

type runnerHandle struct {
	gen     int64
	running bool
}

type outboundLocalIDRecord struct {
	id   string
	atMs int64
}

type pendingRemoteSend struct {
	text    string
	meta    map[string]any
	localID string
	nowMs   int64
}

// PermissionDecision is the resolved permission response to send back to the
// remote runner.
type PermissionDecision struct {
	Allow   bool
	Message string
}

// Inputs

// Event is a marker interface for events consumed by the session reducer.
type Event interface {
	actor.Input
	isSessionEvent()
}

// Command is a marker interface for commands consumed by the session reducer.
type Command interface {
	actor.Input
	isSessionCommand()
}

// cmdSwitchMode requests switching the session to the target mode.
type cmdSwitchMode struct {
	actor.InputBase
	Target Mode
	Reply  chan error
}

// isSessionCommand marks cmdSwitchMode as a session command.
func (cmdSwitchMode) isSessionCommand() {}

// cmdRemoteSend sends a user message to the remote runner (remote mode only).
type cmdRemoteSend struct {
	actor.InputBase
	Text    string
	Meta    map[string]any
	Reply   chan error
	LocalID string
}

// isSessionCommand marks cmdRemoteSend as a session command.
func (cmdRemoteSend) isSessionCommand() {}

// cmdInboundUserMessage represents a user message received from the server
// (originating from a mobile client).
type cmdInboundUserMessage struct {
	actor.InputBase
	Text    string
	Meta    map[string]any
	LocalID string
	NowMs   int64
}

// isSessionCommand marks cmdInboundUserMessage as a session command.
func (cmdInboundUserMessage) isSessionCommand() {}

// cmdAbortRemote aborts the current remote turn.
type cmdAbortRemote struct {
	actor.InputBase
	Reply chan error
}

// isSessionCommand marks cmdAbortRemote as a session command.
func (cmdAbortRemote) isSessionCommand() {}

// cmdPermissionDecision submits a decision for a pending permission request.
type cmdPermissionDecision struct {
	actor.InputBase
	RequestID string
	Allow     bool
	Message   string
	NowMs     int64
	Reply     chan error
}

// isSessionCommand marks cmdPermissionDecision as a session command.
func (cmdPermissionDecision) isSessionCommand() {}

// cmdPermissionAwait registers a permission request, emits the permission
// ephemeral to the phone, and returns the decision via Reply.
//
// This command is used by non-Claude runners (ACP/Codex) that need to block
// until the phone replies, without blocking the actor loop.
type cmdPermissionAwait struct {
	actor.InputBase
	RequestID string
	ToolName  string
	Input     json.RawMessage
	NowMs     int64
	Reply     chan PermissionDecision
	// Ack is an optional signal that fires once the reducer registers the request
	// and emits the ephemeral. It allows synchronous callers to detect mailbox
	// drops and avoid stalling upstream engines indefinitely.
	Ack chan struct{}
}

// isSessionCommand marks cmdPermissionAwait as a session command.
func (cmdPermissionAwait) isSessionCommand() {}

// cmdPersistAgentState requests that the current agent state be persisted.
// It is used during the migration to actor-owned persistence.
type cmdPersistAgentState struct {
	actor.InputBase
	AgentStateJSON string
}

// isSessionCommand marks cmdPersistAgentState as a session command.
func (cmdPersistAgentState) isSessionCommand() {}

// cmdPersistAgentStateImmediate requests that the current agent state be
// persisted immediately (no debounce).
type cmdPersistAgentStateImmediate struct {
	actor.InputBase
	AgentStateJSON string
}

// isSessionCommand marks cmdPersistAgentStateImmediate as a session command.
func (cmdPersistAgentStateImmediate) isSessionCommand() {}

// cmdWaitForAgentStatePersist requests that the current agent state be
// persisted immediately and completes Reply once the persistence attempt
// succeeds or fails.
type cmdWaitForAgentStatePersist struct {
	actor.InputBase
	Reply chan error
}

// isSessionCommand marks cmdWaitForAgentStatePersist as a session command.
func (cmdWaitForAgentStatePersist) isSessionCommand() {}

// cmdSetControlledByUser updates AgentState.ControlledByUser and schedules
// persistence without affecting runner lifecycle. This is used by non-Claude
// agents that still need the phone UI to reflect control ownership.
type cmdSetControlledByUser struct {
	actor.InputBase
	ControlledByUser bool
	NowMs            int64
}

// isSessionCommand marks cmdSetControlledByUser as a session command.
func (cmdSetControlledByUser) isSessionCommand() {}

// cmdSetAgentConfig updates durable agent configuration for the session.
//
// The reducer treats empty values as "no change" so callers can update a single
// field without having to round-trip the current configuration.
type cmdSetAgentConfig struct {
	actor.InputBase

	// Model selects the upstream model identifier.
	Model string
	// PermissionMode selects the approval/sandbox preset.
	PermissionMode string
	// ReasoningEffort selects a Codex reasoning effort preset.
	ReasoningEffort string

	// Reply is completed with an error if the config cannot be applied.
	Reply chan error
}

// isSessionCommand marks cmdSetAgentConfig as a session command.
func (cmdSetAgentConfig) isSessionCommand() {}

// AgentEngineSettingsSnapshot is a best-effort snapshot of the agent engine's
// supported settings and current configuration.
//
// The reducer does not compute this directly; it is fulfilled by the runtime so
// the engine remains the single source of truth for capabilities.
type AgentEngineSettingsSnapshot struct {
	// AgentType is the session's current agent type (e.g. codex/claude).
	AgentType agentengine.AgentType
	// Capabilities reports which knobs are supported.
	Capabilities agentengine.AgentCapabilities
	// DesiredConfig is the durable config stored in agentState.
	DesiredConfig agentengine.AgentConfig
	// EffectiveConfig is the engine-reported current config (best-effort).
	EffectiveConfig agentengine.AgentConfig
	// Error contains a human-readable failure, if any.
	Error string
}

// cmdGetAgentEngineSettings requests querying the agent engine for supported
// settings + current configuration.
type cmdGetAgentEngineSettings struct {
	actor.InputBase
	Reply chan AgentEngineSettingsSnapshot
}

// isSessionCommand marks cmdGetAgentEngineSettings as a session command.
func (cmdGetAgentEngineSettings) isSessionCommand() {}

// Events emitted by the runtime back into the reducer.

type evRunnerReady struct {
	actor.InputBase
	Gen  int64
	Mode Mode
}

// isSessionEvent marks evRunnerReady as a session event.
func (evRunnerReady) isSessionEvent() {}

type evRunnerExited struct {
	actor.InputBase
	Gen  int64
	Err  error
	Mode Mode
}

// isSessionEvent marks evRunnerExited as a session event.
func (evRunnerExited) isSessionEvent() {}

// evEngineSessionIdentified indicates that the engine observed an agent resume token.
type evEngineSessionIdentified struct {
	actor.InputBase
	Gen         int64
	Mode        Mode
	ResumeToken string
}

// isSessionEvent marks evEngineSessionIdentified as a session event.
func (evEngineSessionIdentified) isSessionEvent() {}

// evEngineRolloutPath indicates the engine observed a rollout JSONL path.
type evEngineRolloutPath struct {
	actor.InputBase
	Gen  int64
	Path string
}

// isSessionEvent marks evEngineRolloutPath as a session event.
func (evEngineRolloutPath) isSessionEvent() {}

// evEngineThinking indicates whether the active runner is currently processing a turn.
type evEngineThinking struct {
	actor.InputBase
	Gen      int64
	Mode     Mode
	Thinking bool
	NowMs    int64
}

// isSessionEvent marks evEngineThinking as a session event.
func (evEngineThinking) isSessionEvent() {}

// evEngineUIEvent is a rendered UI event emitted by the active engine.
type evEngineUIEvent struct {
	actor.InputBase
	Gen  int64
	Mode Mode

	EventID string
	Kind    string
	Phase   string
	Status  string

	BriefMarkdown string
	FullMarkdown  string

	NowMs int64
}

// isSessionEvent marks evEngineUIEvent as a session event.
func (evEngineUIEvent) isSessionEvent() {}

type evPermissionRequested struct {
	actor.InputBase
	RequestID string
	ToolName  string
	Input     json.RawMessage

	// NowMs is the wall clock timestamp (unix millis) generated by runtime.
	NowMs int64
}

// isSessionEvent marks evPermissionRequested as a session event.
func (evPermissionRequested) isSessionEvent() {}

type evDesktopTakeback struct {
	actor.InputBase
}

// isSessionEvent marks evDesktopTakeback as a session event.
func (evDesktopTakeback) isSessionEvent() {}

type evWSConnected struct {
	actor.InputBase
}

// isSessionEvent marks evWSConnected as a session event.
func (evWSConnected) isSessionEvent() {}

type evWSDisconnected struct {
	actor.InputBase
	Reason string
}

// isSessionEvent marks evWSDisconnected as a session event.
func (evWSDisconnected) isSessionEvent() {}

type evTerminalConnected struct {
	actor.InputBase
}

// isSessionEvent marks evTerminalConnected as a session event.
func (evTerminalConnected) isSessionEvent() {}

type evTerminalDisconnected struct {
	actor.InputBase
	Reason string
}

// isSessionEvent marks evTerminalDisconnected as a session event.
func (evTerminalDisconnected) isSessionEvent() {}

type evTimerFired struct {
	actor.InputBase
	Name  string
	NowMs int64
}

// isSessionEvent marks evTimerFired as a session event.
func (evTimerFired) isSessionEvent() {}

// evOutboundMessageReady indicates that some runtime produced an encrypted
// session message ready to send to the server.
type evOutboundMessageReady struct {
	actor.InputBase
	Gen        int64
	LocalID    string
	Ciphertext string
	NowMs      int64

	// UserTextNormalized is set for "user" messages so the reducer can suppress
	// echoes when the content originated from a remote/mobile injection.
	UserTextNormalized string
}

// isSessionEvent marks evOutboundMessageReady as a session event.
func (evOutboundMessageReady) isSessionEvent() {}

// evSessionUpdate represents an inbound session-update payload from the server.
type evSessionUpdate struct {
	actor.InputBase
	Data map[string]any
}

// isSessionEvent marks evSessionUpdate as a session event.
func (evSessionUpdate) isSessionEvent() {}

// evMessageUpdate represents an inbound update/message payload from the server.
type evMessageUpdate struct {
	actor.InputBase
	Data map[string]any
}

// isSessionEvent marks evMessageUpdate as a session event.
func (evMessageUpdate) isSessionEvent() {}

// evEphemeral represents an inbound ephemeral payload from the server.
type evEphemeral struct {
	actor.InputBase
	Data map[string]any
}

// isSessionEvent marks evEphemeral as a session event.
func (evEphemeral) isSessionEvent() {}

// cmdShutdown requests stopping all runtimes and transitioning to Closed.
type cmdShutdown struct {
	actor.InputBase
	Reply chan error
}

// isSessionCommand marks cmdShutdown as a session command.
func (cmdShutdown) isSessionCommand() {}

// EvAgentStatePersisted indicates the server accepted an agent state update.
// NewVersion is the server-side version after the update.
type EvAgentStatePersisted struct {
	actor.InputBase
	NewVersion int64
}

// isSessionEvent marks EvAgentStatePersisted as a session event.
func (EvAgentStatePersisted) isSessionEvent() {}

// EvAgentStateVersionMismatch indicates the server rejected an agent state update
// due to a version mismatch. ServerVersion is the server's current version.
type EvAgentStateVersionMismatch struct {
	actor.InputBase
	ServerVersion int64
}

// isSessionEvent marks EvAgentStateVersionMismatch as a session event.
func (EvAgentStateVersionMismatch) isSessionEvent() {}

// EvAgentStatePersistFailed indicates persisting agent state failed due to a
// non-version-mismatch error.
type EvAgentStatePersistFailed struct {
	actor.InputBase
	Err error
}

// isSessionEvent marks EvAgentStatePersistFailed as a session event.
func (EvAgentStatePersistFailed) isSessionEvent() {}

// Effects

// Effect is a marker interface for effects emitted by the reducer.
type Effect interface {
	actor.Effect
	isSessionEffect()
}

type effStartLocalRunner struct {
	actor.EffectBase
	Gen         int64
	WorkDir     string
	Resume      string
	RolloutPath string
	Config      agentengine.AgentConfig
}

// isSessionEffect marks effStartLocalRunner as a session effect.
func (effStartLocalRunner) isSessionEffect() {}

type effStopLocalRunner struct {
	actor.EffectBase
	Gen int64
}

// isSessionEffect marks effStopLocalRunner as a session effect.
func (effStopLocalRunner) isSessionEffect() {}

type effStartRemoteRunner struct {
	actor.EffectBase
	Gen     int64
	WorkDir string
	Resume  string
	Config  agentengine.AgentConfig
}

// isSessionEffect marks effStartRemoteRunner as a session effect.
func (effStartRemoteRunner) isSessionEffect() {}

// effApplyEngineConfig requests applying the given agent configuration to the
// currently-running engine (best-effort).
type effApplyEngineConfig struct {
	actor.EffectBase
	Gen    int64
	Config agentengine.AgentConfig
}

// isSessionEffect marks effApplyEngineConfig as a session effect.
func (effApplyEngineConfig) isSessionEffect() {}

// effQueryAgentEngineSettings requests a best-effort snapshot of engine
// capabilities + current configuration.
type effQueryAgentEngineSettings struct {
	actor.EffectBase
	Gen       int64
	AgentType agentengine.AgentType
	Desired   agentengine.AgentConfig
	Reply     chan AgentEngineSettingsSnapshot
}

// isSessionEffect marks effQueryAgentEngineSettings as a session effect.
func (effQueryAgentEngineSettings) isSessionEffect() {}

type effStopRemoteRunner struct {
	actor.EffectBase
	Gen int64
	// Silent suppresses UX banners when the runner is being stopped as part of a
	// full shutdown rather than an interactive mode switch.
	Silent bool
}

// isSessionEffect marks effStopRemoteRunner as a session effect.
func (effStopRemoteRunner) isSessionEffect() {}

type effRemoteSend struct {
	actor.EffectBase
	Gen     int64
	Text    string
	Meta    map[string]any
	LocalID string
}

// isSessionEffect marks effRemoteSend as a session effect.
func (effRemoteSend) isSessionEffect() {}

type effRemoteAbort struct {
	actor.EffectBase
	Gen int64
}

// isSessionEffect marks effRemoteAbort as a session effect.
func (effRemoteAbort) isSessionEffect() {}

type effLocalSendLine struct {
	actor.EffectBase
	Gen  int64
	Text string
}

// isSessionEffect marks effLocalSendLine as a session effect.
func (effLocalSendLine) isSessionEffect() {}

type effPersistAgentState struct {
	actor.EffectBase
	AgentStateJSON  string
	ExpectedVersion int64
}

// isSessionEffect marks effPersistAgentState as a session effect.
func (effPersistAgentState) isSessionEffect() {}

// effPersistLocalSessionInfo persists machine-local session metadata under
// DelightHome.
//
// This is intentionally separate from agent-state persistence because it can
// contain machine-specific paths that should never be sent to the server.
type effPersistLocalSessionInfo struct {
	actor.EffectBase
	SessionID   string
	AgentType   string
	ResumeToken string
	RolloutPath string
}

// isSessionEffect marks effPersistLocalSessionInfo as a session effect.
func (effPersistLocalSessionInfo) isSessionEffect() {}

type effEmitEphemeral struct {
	actor.EffectBase
	Payload any
}

// isSessionEffect marks effEmitEphemeral as a session effect.
func (effEmitEphemeral) isSessionEffect() {}

type effEmitMessage struct {
	actor.EffectBase
	LocalID    string
	Ciphertext string
}

// isSessionEffect marks effEmitMessage as a session effect.
func (effEmitMessage) isSessionEffect() {}

type effStartTimer struct {
	actor.EffectBase
	Name    string
	AfterMs int64
}

// isSessionEffect marks effStartTimer as a session effect.
func (effStartTimer) isSessionEffect() {}

type effCancelTimer struct {
	actor.EffectBase
	Name string
}

// isSessionEffect marks effCancelTimer as a session effect.
func (effCancelTimer) isSessionEffect() {}

// effCompleteReply completes a command reply channel after state has been
// applied by the actor loop.
//
// Reducers should avoid writing to reply channels directly when the caller may
// immediately read actor state after receiving the ack. Sending replies as
// effects ensures the ack happens after the actor loop applies the next state.
type effCompleteReply struct {
	actor.EffectBase
	Reply chan error
	Err   error
}

// isSessionEffect marks effCompleteReply as a session effect.
func (effCompleteReply) isSessionEffect() {}

// effCompletePermissionDecision completes a PermissionDecision reply channel.
//
// This is used by cmdPermissionAwait auto-approve/deny paths, ensuring the
// decision is delivered after the actor state has been applied.
type effCompletePermissionDecision struct {
	actor.EffectBase
	Reply    chan PermissionDecision
	Decision PermissionDecision
}

// isSessionEffect marks effCompletePermissionDecision as a session effect.
func (effCompletePermissionDecision) isSessionEffect() {}

// effSignalAck signals an ack channel after the actor state has been applied.
//
// This is used by cmdPermissionAwait to guarantee the permission request is
// durably registered in actor state before synchronous engines proceed.
type effSignalAck struct {
	actor.EffectBase
	Ack chan struct{}
}

// isSessionEffect marks effSignalAck as a session effect.
func (effSignalAck) isSessionEffect() {}

// effStartDesktopTakebackWatcher starts the "press space twice" desktop watcher
// while the session is in remote mode.
type effStartDesktopTakebackWatcher struct {
	actor.EffectBase
}

// isSessionEffect marks effStartDesktopTakebackWatcher as a session effect.
func (effStartDesktopTakebackWatcher) isSessionEffect() {}

// effStopDesktopTakebackWatcher stops the desktop takeback watcher (best-effort).
type effStopDesktopTakebackWatcher struct {
	actor.EffectBase
}

// isSessionEffect marks effStopDesktopTakebackWatcher as a session effect.
func (effStopDesktopTakebackWatcher) isSessionEffect() {}
