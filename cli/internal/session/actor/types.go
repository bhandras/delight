package actor

import (
	"encoding/json"

	"github.com/bhandras/delight/cli/internal/actor"
)

// NOTE: This package intentionally starts as a type-only skeleton.
// Phase 2 goal: define the state machine vocabulary (state, inputs, effects)
// without wiring it into the production session manager yet.

// Mode is the user-visible control mode.
type Mode string

const (
	// ModeLocal means the desktop controls the session (interactive TUI).
	ModeLocal  Mode = "local"
	// ModeRemote means the phone controls the session (SDK/bridge mode).
	ModeRemote Mode = "remote"
)

// FSMState is the internal session FSM state.
type FSMState string

const (
	// StateLocalStarting indicates the local runner is being started.
	StateLocalStarting  FSMState = "LocalStarting"
	// StateLocalRunning indicates the local runner is active.
	StateLocalRunning   FSMState = "LocalRunning"
	// StateRemoteStarting indicates the remote runner is being started.
	StateRemoteStarting FSMState = "RemoteStarting"
	// StateRemoteRunning indicates the remote runner is active.
	StateRemoteRunning  FSMState = "RemoteRunning"
	// StateClosing indicates shutdown is in progress.
	StateClosing        FSMState = "Closing"
	// StateClosed indicates the session actor has stopped.
	StateClosed         FSMState = "Closed"
)

// State is the loop-owned state for the SessionActor.
type State struct {
	FSM FSMState

	Mode Mode

	// RunnerGen increments each time we (re)start a runner. Runtime completion
	// events must include the generation so stale exits/readies can be ignored.
	RunnerGen int64

	// PendingSwitchReply is completed when a switch finishes (runner ready) or
	// fails (runner exit during starting).
	PendingSwitchReply chan error

	// PendingPermissionReplies are promises keyed by request id.
	PendingPermissionReplies map[string]chan PermissionDecision

	// AgentStateJSON is the durable agent state blob to persist (plaintext JSON).
	AgentStateJSON string

	// AgentStateVersion is the version used for optimistic concurrency control
	// when persisting agent state to the server.
	AgentStateVersion int64

	// PersistRetryRemaining bounds retries on version-mismatch errors. Set to 1
	// when the reducer schedules a persist, and decremented on retry.
	PersistRetryRemaining int
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
	Text   string
	Meta   map[string]any
	Reply  chan error
	LocalID string
}

func (cmdRemoteSend) isSessionCommand() {}

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
	Reply     chan error
}

func (cmdPermissionDecision) isSessionCommand() {}

// cmdPersistAgentState requests that the current agent state be persisted.
// It is used during the migration to actor-owned persistence.
type cmdPersistAgentState struct {
	actor.InputBase
	AgentStateJSON string
}

func (cmdPersistAgentState) isSessionCommand() {}

// Events emitted by the runtime back into the reducer.

type evRunnerReady struct {
	actor.InputBase
	Gen int64
	Mode Mode
}

func (evRunnerReady) isSessionEvent() {}

type evRunnerExited struct {
	actor.InputBase
	Gen int64
	Err error
	Mode Mode
}

func (evRunnerExited) isSessionEvent() {}

type evPermissionRequested struct {
	actor.InputBase
	RequestID string
	ToolName  string
	Input     json.RawMessage
}

func (evPermissionRequested) isSessionEvent() {}

type evDesktopTakeback struct {
	actor.InputBase
}

func (evDesktopTakeback) isSessionEvent() {}

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
	Gen   int64
	Text  string
	Meta  map[string]any
	LocalID string
}

func (effRemoteSend) isSessionEffect() {}

type effRemoteAbort struct {
	actor.EffectBase
	Gen int64
}

func (effRemoteAbort) isSessionEffect() {}

type effPersistAgentState struct {
	actor.EffectBase
	AgentStateJSON string
	ExpectedVersion int64
}

func (effPersistAgentState) isSessionEffect() {}
