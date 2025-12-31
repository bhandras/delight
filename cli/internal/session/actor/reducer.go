package actor

import (
	"github.com/bhandras/delight/cli/internal/actor"
)

// Reduce is the SessionActor reducer.
//
// This is Phase 2 scaffolding: it defines the shape of transitions without
// being wired into the production session manager yet.
func Reduce(state State, input actor.Input) (State, []actor.Effect) {
	switch in := input.(type) {
	case cmdSwitchMode:
		return reduceSwitchMode(state, in)
	case cmdRemoteSend:
		return reduceRemoteSend(state, in)
	case cmdAbortRemote:
		return reduceAbortRemote(state, in)
	case cmdPermissionDecision:
		return reducePermissionDecision(state, in)

	case evRunnerReady:
		return reduceRunnerReady(state, in)
	case evRunnerExited:
		return reduceRunnerExited(state, in)
	case evPermissionRequested:
		return reducePermissionRequested(state, in)
	case evDesktopTakeback:
		// Desktop takeback is a switch to local.
		return reduceSwitchMode(state, cmdSwitchMode{Target: ModeLocal, Reply: nil})
	case evAgentStatePersisted:
		return reduceAgentStatePersisted(state, in)
	case evAgentStateVersionMismatch:
		return reduceAgentStateVersionMismatch(state, in)
	case evAgentStatePersistFailed:
		return reduceAgentStatePersistFailed(state, in)
	default:
		return state, nil
	}
}

func reduceSwitchMode(state State, cmd cmdSwitchMode) (State, []actor.Effect) {
	// Idempotent behavior.
	if cmd.Target == state.Mode && (state.FSM == StateLocalRunning || state.FSM == StateRemoteRunning) {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- nil:
			default:
			}
		}
		return state, nil
	}

	state.RunnerGen++
	gen := state.RunnerGen

	// Only keep the latest pending switch reply (callers should serialize in higher layers).
	state.PendingSwitchReply = cmd.Reply
	state.PersistRetryRemaining = 1

	switch cmd.Target {
	case ModeRemote:
		state.Mode = ModeRemote
		state.FSM = StateRemoteStarting
		return state, []actor.Effect{
			effStopLocalRunner{Gen: gen - 1},
			effStartRemoteRunner{Gen: gen},
			effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
		}
	case ModeLocal:
		state.Mode = ModeLocal
		state.FSM = StateLocalStarting
		return state, []actor.Effect{
			effStopRemoteRunner{Gen: gen - 1},
			effStartLocalRunner{Gen: gen},
			effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
		}
	default:
		// Unknown target; respond error if present.
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrInvalidMode:
			default:
			}
		}
		return state, nil
	}
}

func reduceRunnerReady(state State, ev evRunnerReady) (State, []actor.Effect) {
	if ev.Gen != state.RunnerGen {
		return state, nil
	}
	switch ev.Mode {
	case ModeLocal:
		state.FSM = StateLocalRunning
	case ModeRemote:
		state.FSM = StateRemoteRunning
	}
	if state.PendingSwitchReply != nil {
		select {
		case state.PendingSwitchReply <- nil:
		default:
		}
		state.PendingSwitchReply = nil
	}
	return state, nil
}

func reduceRunnerExited(state State, ev evRunnerExited) (State, []actor.Effect) {
	if ev.Gen != state.RunnerGen {
		return state, nil
	}
	// If we were starting, fail the pending switch.
	if state.FSM == StateLocalStarting || state.FSM == StateRemoteStarting {
		if state.PendingSwitchReply != nil {
			select {
			case state.PendingSwitchReply <- ev.Err:
			default:
			}
			state.PendingSwitchReply = nil
		}
		// Conservative: move to local running on failure (runtime will decide).
		state.FSM = StateLocalRunning
		state.Mode = ModeLocal
		return state, nil
	}
	// If we were running, mark closed (runtime may rehydrate/restart).
	state.FSM = StateClosed
	return state, nil
}

func reduceRemoteSend(state State, cmd cmdRemoteSend) (State, []actor.Effect) {
	if state.FSM != StateRemoteRunning {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrNotRemote:
			default:
			}
		}
		return state, nil
	}
	return state, []actor.Effect{
		effRemoteSend{Gen: state.RunnerGen, Text: cmd.Text, Meta: cmd.Meta, LocalID: cmd.LocalID},
	}
}

func reduceAbortRemote(state State, cmd cmdAbortRemote) (State, []actor.Effect) {
	if state.FSM != StateRemoteRunning {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrNotRemote:
			default:
			}
		}
		return state, nil
	}
	return state, []actor.Effect{effRemoteAbort{Gen: state.RunnerGen}}
}

func reducePermissionRequested(state State, ev evPermissionRequested) (State, []actor.Effect) {
	if state.PendingPermissionReplies == nil {
		state.PendingPermissionReplies = make(map[string]chan PermissionDecision)
	}
	if _, exists := state.PendingPermissionReplies[ev.RequestID]; exists {
		return state, nil
	}
	state.PendingPermissionReplies[ev.RequestID] = make(chan PermissionDecision, 1)
	// Phase 2: durability/ephemeral emission is wired in Phase 4/5.
	return state, nil
}

func reducePermissionDecision(state State, cmd cmdPermissionDecision) (State, []actor.Effect) {
	if state.PendingPermissionReplies == nil {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrUnknownPermissionRequest:
			default:
			}
		}
		return state, nil
	}
	ch, ok := state.PendingPermissionReplies[cmd.RequestID]
	if !ok {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrUnknownPermissionRequest:
			default:
			}
		}
		return state, nil
	}
	delete(state.PendingPermissionReplies, cmd.RequestID)
	select {
	case ch <- PermissionDecision{Allow: cmd.Allow, Message: cmd.Message}:
	default:
	}
	if cmd.Reply != nil {
		select {
		case cmd.Reply <- nil:
		default:
		}
	}
	// Phase 2: runtime effect to send control_response is wired in Phase 5.
	return state, nil
}

func reduceAgentStatePersisted(state State, ev evAgentStatePersisted) (State, []actor.Effect) {
	if ev.NewVersion > 0 {
		state.AgentStateVersion = ev.NewVersion
	}
	state.PersistRetryRemaining = 0
	return state, nil
}

func reduceAgentStateVersionMismatch(state State, ev evAgentStateVersionMismatch) (State, []actor.Effect) {
	if ev.ServerVersion > 0 {
		state.AgentStateVersion = ev.ServerVersion
	}
	if state.PersistRetryRemaining <= 0 {
		return state, nil
	}
	state.PersistRetryRemaining--
	return state, []actor.Effect{
		effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
	}
}

func reduceAgentStatePersistFailed(state State, ev evAgentStatePersistFailed) (State, []actor.Effect) {
	_ = ev
	// Phase 4 minimal behavior: keep version as-is and allow a future tick/debounce
	// mechanism to retry. We don't retry immediately on arbitrary errors because
	// it can create tight loops during outages.
	state.PersistRetryRemaining = 0
	return state, nil
}
