package actor

import (
	"encoding/json"
	"strings"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
)

const (
	persistDebounceTimerName = "persist-agent-state"
	persistDebounceAfterMs   = 150
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
	case cmdPersistAgentState:
		return reducePersistAgentState(state, in)
	case cmdPersistAgentStateImmediate:
		return reducePersistAgentStateImmediate(state, in)
	case cmdShutdown:
		return reduceShutdown(state, in)

	case evRunnerReady:
		return reduceRunnerReady(state, in)
	case evRunnerExited:
		return reduceRunnerExited(state, in)
	case evPermissionRequested:
		return reducePermissionRequested(state, in)
	case evDesktopTakeback:
		// Desktop takeback is a switch to local.
		return reduceSwitchMode(state, cmdSwitchMode{Target: ModeLocal, Reply: nil})
	case evWSConnected:
		state.WSConnected = true
		return state, nil
	case evWSDisconnected:
		state.WSConnected = false
		return state, nil
	case evTimerFired:
		return reduceTimerFired(state, in)
	case evOutboundMessageReady:
		return reduceOutboundMessageReady(state, in)
	case evSessionUpdate:
		// Phase 1/2: treat as observability-only.
		return state, nil
	case evMessageUpdate:
		// Phase 1/2: treat as observability-only.
		return state, nil
	case evEphemeral:
		// Phase 1/2: treat as observability-only.
		return state, nil
	case EvAgentStatePersisted:
		return reduceAgentStatePersisted(state, in)
	case EvAgentStateVersionMismatch:
		return reduceAgentStateVersionMismatch(state, in)
	case EvAgentStatePersistFailed:
		return reduceAgentStatePersistFailed(state, in)
	default:
		return state, nil
	}
}

func reduceOutboundMessageReady(state State, ev evOutboundMessageReady) (State, []actor.Effect) {
	if ev.Gen != 0 && ev.Gen != state.RunnerGen {
		return state, nil
	}
	if ev.Ciphertext == "" {
		return state, nil
	}

	// If this is a user message originating from remote injection, suppress it.
	if ev.UserTextNormalized != "" && state.isRecentlyInjectedRemoteInput(ev.UserTextNormalized, ev.NowMs) {
		return state, nil
	}

	if ev.LocalID != "" {
		state.rememberOutboundUserLocalID(ev.LocalID, ev.NowMs)
	}

	return state, []actor.Effect{
		effEmitMessage{LocalID: ev.LocalID, Ciphertext: ev.Ciphertext},
	}
}

func reducePersistAgentState(state State, cmd cmdPersistAgentState) (State, []actor.Effect) {
	if cmd.AgentStateJSON == "" {
		return state, nil
	}
	state.AgentStateJSON = cmd.AgentStateJSON
	state.PersistRetryRemaining = 1
	return schedulePersistDebounced(state)
}

func (state *State) rememberOutboundUserLocalID(localID string, nowMs int64) {
	localID = strings.TrimSpace(localID)
	if localID == "" || nowMs <= 0 {
		return
	}

	const maxItems = 128
	const ttlMs = int64(30_000)

	cutoff := nowMs - ttlMs
	dst := state.RecentOutboundUserLocalIDs[:0]
	for _, rec := range state.RecentOutboundUserLocalIDs {
		if rec.atMs >= cutoff {
			dst = append(dst, rec)
		}
	}
	state.RecentOutboundUserLocalIDs = dst

	state.RecentOutboundUserLocalIDs = append(state.RecentOutboundUserLocalIDs, outboundLocalIDRecord{id: localID, atMs: nowMs})
	if len(state.RecentOutboundUserLocalIDs) > maxItems {
		state.RecentOutboundUserLocalIDs = state.RecentOutboundUserLocalIDs[len(state.RecentOutboundUserLocalIDs)-maxItems:]
	}
}

func (state *State) isRecentlyInjectedRemoteInput(text string, nowMs int64) bool {
	text = normalizeRemoteInputText(text)
	if text == "" || nowMs <= 0 {
		return false
	}

	const ttlMs = int64(20_000)
	cutoff := nowMs - ttlMs
	for i := len(state.RecentRemoteInputs) - 1; i >= 0; i-- {
		rec := state.RecentRemoteInputs[i]
		if rec.atMs < cutoff {
			break
		}
		if rec.text == text {
			return true
		}
	}
	return false
}

func normalizeRemoteInputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimSpace(text)
}

func reducePersistAgentStateImmediate(state State, cmd cmdPersistAgentStateImmediate) (State, []actor.Effect) {
	if cmd.AgentStateJSON == "" {
		return state, nil
	}
	state.AgentStateJSON = cmd.AgentStateJSON
	state.PersistRetryRemaining = 1
	state.PersistDebounceTimerArmed = false
	return state, []actor.Effect{
		effCancelTimer{Name: persistDebounceTimerName},
		effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
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
		state.AgentState.ControlledByUser = false
		state = refreshAgentStateJSON(state)
		state, persistEffects := schedulePersistDebounced(state)
		effects := []actor.Effect{
			effStopLocalRunner{Gen: gen - 1},
			effStartRemoteRunner{Gen: gen},
		}
		effects = append(effects, persistEffects...)
		return state, effects
	case ModeLocal:
		state.Mode = ModeLocal
		state.FSM = StateLocalStarting
		state.AgentState.ControlledByUser = true
		state = refreshAgentStateJSON(state)
		state, persistEffects := schedulePersistDebounced(state)
		effects := []actor.Effect{
			effStopRemoteRunner{Gen: gen - 1},
			effStartLocalRunner{Gen: gen},
		}
		effects = append(effects, persistEffects...)
		return state, effects
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

	// If we were running, transition to a safe state.
	switch state.FSM {
	case StateRemoteRunning:
		// Remote runner exited unexpectedly; fall back to local runner.
		state.RunnerGen++
		gen := state.RunnerGen
		state.Mode = ModeLocal
		state.FSM = StateLocalStarting
		state.AgentState.ControlledByUser = true
		state = refreshAgentStateJSON(state)
		state, persistEffects := schedulePersistDebounced(state)
		effects := []actor.Effect{
			effStopRemoteRunner{Gen: gen - 1},
			effStartLocalRunner{Gen: gen},
		}
		effects = append(effects, persistEffects...)
		return state, effects
	case StateLocalRunning:
		// Local runner exited; keep the actor alive but mark closed.
		state.FSM = StateClosed
		return state, nil
	default:
		state.FSM = StateClosed
		return state, nil
	}
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
	if cmd.Reply != nil {
		select {
		case cmd.Reply <- nil:
		default:
		}
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
	if cmd.Reply != nil {
		select {
		case cmd.Reply <- nil:
		default:
		}
	}
	return state, []actor.Effect{effRemoteAbort{Gen: state.RunnerGen}}
}

func reducePermissionRequested(state State, ev evPermissionRequested) (State, []actor.Effect) {
	if ev.RequestID == "" {
		return state, nil
	}

	// Only accept permission prompts while remote is running.
	if state.FSM != StateRemoteRunning {
		return state, nil
	}

	if state.AgentState.Requests == nil {
		state.AgentState.Requests = make(map[string]types.AgentPendingRequest)
	}
	if _, exists := state.AgentState.Requests[ev.RequestID]; exists {
		return state, nil
	}
	state.AgentState.Requests[ev.RequestID] = types.AgentPendingRequest{
		ToolName:  ev.ToolName,
		Input:     string(ev.Input),
		CreatedAt: ev.NowMs,
	}
	state = refreshAgentStateJSON(state)
	state, persistEffects := schedulePersistDebounced(state)

	var effects []actor.Effect
	effects = append(effects, effEmitEphemeral{Payload: map[string]any{
		"type":      "permission-request",
		"id":        state.SessionID,
		"requestId": ev.RequestID,
		"toolName":  ev.ToolName,
		"input":     string(ev.Input),
	}})
	effects = append(effects, persistEffects...)
	return state, effects
}

func reducePermissionDecision(state State, cmd cmdPermissionDecision) (State, []actor.Effect) {
	if cmd.RequestID == "" {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrUnknownPermissionRequest:
			default:
			}
		}
		return state, nil
	}
	if state.AgentState.Requests == nil {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrUnknownPermissionRequest:
			default:
			}
		}
		return state, nil
	}
	if _, ok := state.AgentState.Requests[cmd.RequestID]; !ok {
		if cmd.Reply != nil {
			select {
			case cmd.Reply <- ErrUnknownPermissionRequest:
			default:
			}
		}
		return state, nil
	}
	if cmd.Reply != nil {
		select {
		case cmd.Reply <- nil:
		default:
		}
	}

	// Durable bookkeeping mirrors the legacy Manager path.
	toolName := ""
	input := ""
	if state.AgentState.Requests != nil {
		if prev, exists := state.AgentState.Requests[cmd.RequestID]; exists {
			toolName = prev.ToolName
			input = prev.Input
		}
		delete(state.AgentState.Requests, cmd.RequestID)
	}
	if state.AgentState.CompletedRequests == nil {
		state.AgentState.CompletedRequests = make(map[string]types.AgentCompletedRequest)
	}
	if len(state.AgentState.CompletedRequests) > 200 {
		state.AgentState.CompletedRequests = make(map[string]types.AgentCompletedRequest)
	}
	state.AgentState.CompletedRequests[cmd.RequestID] = types.AgentCompletedRequest{
		ToolName:   toolName,
		Input:      input,
		Allow:      cmd.Allow,
		Message:    cmd.Message,
		ResolvedAt: cmd.NowMs,
	}

	state = refreshAgentStateJSON(state)
	state, persistEffects := schedulePersistDebounced(state)
	effects := []actor.Effect{
		effRemotePermissionDecision{
			Gen:       state.RunnerGen,
			RequestID: cmd.RequestID,
			Allow:     cmd.Allow,
			Message:   cmd.Message,
		},
	}
	effects = append(effects, persistEffects...)
	return state, effects
}

func reduceAgentStatePersisted(state State, ev EvAgentStatePersisted) (State, []actor.Effect) {
	if ev.NewVersion > 0 {
		state.AgentStateVersion = ev.NewVersion
	}
	state.PersistRetryRemaining = 0
	return state, nil
}

func reduceAgentStateVersionMismatch(state State, ev EvAgentStateVersionMismatch) (State, []actor.Effect) {
	if ev.ServerVersion > 0 {
		state.AgentStateVersion = ev.ServerVersion
	}
	if state.PersistRetryRemaining <= 0 {
		return state, nil
	}
	state.PersistRetryRemaining--
	// Retry immediately (no debounce) on version mismatch; the new expected version
	// must be applied promptly to avoid leaving stateDirty set.
	state.PersistDebounceTimerArmed = false
	return state, []actor.Effect{effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion}}
}

func reduceAgentStatePersistFailed(state State, ev EvAgentStatePersistFailed) (State, []actor.Effect) {
	_ = ev
	// Phase 4 minimal behavior: keep version as-is and allow a future tick/debounce
	// mechanism to retry. We don't retry immediately on arbitrary errors because
	// it can create tight loops during outages.
	state.PersistRetryRemaining = 0
	return state, nil
}

func reduceTimerFired(state State, ev evTimerFired) (State, []actor.Effect) {
	switch ev.Name {
	case persistDebounceTimerName:
		state.PersistDebounceTimerArmed = false
		if state.AgentStateJSON == "" {
			return state, nil
		}
		state.PersistRetryRemaining = 1
		return state, []actor.Effect{effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion}}
	default:
		return state, nil
	}
}

func reduceShutdown(state State, cmd cmdShutdown) (State, []actor.Effect) {
	state.FSM = StateClosing
	if cmd.Reply != nil {
		select {
		case cmd.Reply <- nil:
		default:
		}
	}
	// Stop both runners; runtime will emit exit events.
	return state, []actor.Effect{
		effStopLocalRunner{Gen: state.RunnerGen},
		effStopRemoteRunner{Gen: state.RunnerGen},
	}
}

func refreshAgentStateJSON(state State) State {
	data, err := json.Marshal(state.AgentState)
	if err != nil {
		// Keep existing JSON if we cannot marshal; this should be extremely rare.
		return state
	}
	state.AgentStateJSON = string(data)
	return state
}

func schedulePersistDebounced(state State) (State, []actor.Effect) {
	state.PersistRetryRemaining = 1
	// If we have no websocket connection yet, still schedule persistence; the
	// runtime can treat it as a no-op, but the state remains marked dirty.
	state.PersistDebounceTimerArmed = true
	return state, []actor.Effect{
		effCancelTimer{Name: persistDebounceTimerName},
		effStartTimer{Name: persistDebounceTimerName, AfterMs: persistDebounceAfterMs},
	}
}
