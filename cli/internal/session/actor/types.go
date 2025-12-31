package actor

import (
	"encoding/json"

	"github.com/bhandras/delight/cli/internal/actor"
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

	// ClaudeSessionID is the most recently observed Claude session id for the
	// local runner. It is used as a best-effort resume id when starting remote.
	ClaudeSessionID string

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

	// Connection state (observability + UI gating).
	WSConnected      bool
	MachineConnected bool

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

func (cmdSwitchMode) isSessionCommand() {}

// cmdRemoteSend sends a user message to the remote runner (remote mode only).
type cmdRemoteSend struct {
	actor.InputBase
	Text    string
	Meta    map[string]any
	Reply   chan error
	LocalID string
}

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

func (cmdInboundUserMessage) isSessionCommand() {}

// cmdAbortRemote aborts the current remote turn.
type cmdAbortRemote struct {
	actor.InputBase
	Reply chan error
}

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
}

func (cmdPermissionAwait) isSessionCommand() {}

// cmdPersistAgentState requests that the current agent state be persisted.
// It is used during the migration to actor-owned persistence.
type cmdPersistAgentState struct {
	actor.InputBase
	AgentStateJSON string
}

func (cmdPersistAgentState) isSessionCommand() {}

// cmdPersistAgentStateImmediate requests that the current agent state be
// persisted immediately (no debounce).
type cmdPersistAgentStateImmediate struct {
	actor.InputBase
	AgentStateJSON string
}

func (cmdPersistAgentStateImmediate) isSessionCommand() {}

// cmdSetControlledByUser updates AgentState.ControlledByUser and schedules
// persistence without affecting runner lifecycle. This is used by non-Claude
// agents that still need the phone UI to reflect control ownership.
type cmdSetControlledByUser struct {
	actor.InputBase
	ControlledByUser bool
	NowMs            int64
}

func (cmdSetControlledByUser) isSessionCommand() {}

// Events emitted by the runtime back into the reducer.

type evRunnerReady struct {
	actor.InputBase
	Gen  int64
	Mode Mode
}

func (evRunnerReady) isSessionEvent() {}

type evRunnerExited struct {
	actor.InputBase
	Gen  int64
	Err  error
	Mode Mode
}

func (evRunnerExited) isSessionEvent() {}

type evClaudeSessionDetected struct {
	actor.InputBase
	Gen       int64
	SessionID string
}

func (evClaudeSessionDetected) isSessionEvent() {}

type evPermissionRequested struct {
	actor.InputBase
	RequestID string
	ToolName  string
	Input     json.RawMessage

	// NowMs is the wall clock timestamp (unix millis) generated by runtime.
	NowMs int64
}

func (evPermissionRequested) isSessionEvent() {}

type evDesktopTakeback struct {
	actor.InputBase
}

func (evDesktopTakeback) isSessionEvent() {}

type evWSConnected struct {
	actor.InputBase
}

func (evWSConnected) isSessionEvent() {}

type evWSDisconnected struct {
	actor.InputBase
	Reason string
}

func (evWSDisconnected) isSessionEvent() {}

type evMachineConnected struct {
	actor.InputBase
}

func (evMachineConnected) isSessionEvent() {}

type evMachineDisconnected struct {
	actor.InputBase
	Reason string
}

func (evMachineDisconnected) isSessionEvent() {}

type evTimerFired struct {
	actor.InputBase
	Name  string
	NowMs int64
}

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

func (evOutboundMessageReady) isSessionEvent() {}

// evSessionUpdate represents an inbound session-update payload from the server.
type evSessionUpdate struct {
	actor.InputBase
	Data map[string]any
}

func (evSessionUpdate) isSessionEvent() {}

// evMessageUpdate represents an inbound update/message payload from the server.
type evMessageUpdate struct {
	actor.InputBase
	Data map[string]any
}

func (evMessageUpdate) isSessionEvent() {}

// evEphemeral represents an inbound ephemeral payload from the server.
type evEphemeral struct {
	actor.InputBase
	Data map[string]any
}

func (evEphemeral) isSessionEvent() {}

// cmdShutdown requests stopping all runtimes and transitioning to Closed.
type cmdShutdown struct {
	actor.InputBase
	Reply chan error
}

func (cmdShutdown) isSessionCommand() {}

// EvAgentStatePersisted indicates the server accepted an agent state update.
// NewVersion is the server-side version after the update.
type EvAgentStatePersisted struct {
	actor.InputBase
	NewVersion int64
}

func (EvAgentStatePersisted) isSessionEvent() {}

// EvAgentStateVersionMismatch indicates the server rejected an agent state update
// due to a version mismatch. ServerVersion is the server's current version.
type EvAgentStateVersionMismatch struct {
	actor.InputBase
	ServerVersion int64
}

func (EvAgentStateVersionMismatch) isSessionEvent() {}

// EvAgentStatePersistFailed indicates persisting agent state failed due to a
// non-version-mismatch error.
type EvAgentStatePersistFailed struct {
	actor.InputBase
	Err error
}

func (EvAgentStatePersistFailed) isSessionEvent() {}

// Effects

// Effect is a marker interface for effects emitted by the reducer.
type Effect interface {
	actor.Effect
	isSessionEffect()
}

type effStartLocalRunner struct {
	actor.EffectBase
	Gen     int64
	WorkDir string
	Resume  string
}

func (effStartLocalRunner) isSessionEffect() {}

type effStopLocalRunner struct {
	actor.EffectBase
	Gen int64
}

func (effStopLocalRunner) isSessionEffect() {}

type effStartRemoteRunner struct {
	actor.EffectBase
	Gen     int64
	WorkDir string
	Resume  string
}

func (effStartRemoteRunner) isSessionEffect() {}

type effStopRemoteRunner struct {
	actor.EffectBase
	Gen int64
}

func (effStopRemoteRunner) isSessionEffect() {}

type effRemoteSend struct {
	actor.EffectBase
	Gen     int64
	Text    string
	Meta    map[string]any
	LocalID string
}

func (effRemoteSend) isSessionEffect() {}

type effRemoteAbort struct {
	actor.EffectBase
	Gen int64
}

func (effRemoteAbort) isSessionEffect() {}

type effLocalSendLine struct {
	actor.EffectBase
	Gen  int64
	Text string
}

func (effLocalSendLine) isSessionEffect() {}

type effRemotePermissionDecision struct {
	actor.EffectBase
	Gen          int64
	RequestID    string
	Allow        bool
	Message      string
	UpdatedInput json.RawMessage
}

func (effRemotePermissionDecision) isSessionEffect() {}

type effPersistAgentState struct {
	actor.EffectBase
	AgentStateJSON  string
	ExpectedVersion int64
}

func (effPersistAgentState) isSessionEffect() {}

type effEmitEphemeral struct {
	actor.EffectBase
	Payload any
}

func (effEmitEphemeral) isSessionEffect() {}

type effEmitMessage struct {
	actor.EffectBase
	LocalID    string
	Ciphertext string
}

func (effEmitMessage) isSessionEffect() {}

type effStartTimer struct {
	actor.EffectBase
	Name    string
	AfterMs int64
}

func (effStartTimer) isSessionEffect() {}

type effCancelTimer struct {
	actor.EffectBase
	Name string
}

func (effCancelTimer) isSessionEffect() {}

// effStartDesktopTakebackWatcher starts the "press space twice" desktop watcher
// while the session is in remote mode.
type effStartDesktopTakebackWatcher struct {
	actor.EffectBase
}

func (effStartDesktopTakebackWatcher) isSessionEffect() {}

// effStopDesktopTakebackWatcher stops the desktop takeback watcher (best-effort).
type effStopDesktopTakebackWatcher struct {
	actor.EffectBase
}

func (effStopDesktopTakebackWatcher) isSessionEffect() {}
