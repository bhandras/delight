package actor

import (
	"encoding/json"
	"strings"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
)

const (
	// persistDebounceTimerName is the timer key used to debounce agent-state persistence.
	persistDebounceTimerName = "persist-agent-state"
	// persistDebounceAfterMs is the debounce delay for agent-state persistence (in ms).
	persistDebounceAfterMs = 150
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
	case cmdInboundUserMessage:
		return reduceInboundUserMessage(state, in)
	case cmdAbortRemote:
		return reduceAbortRemote(state, in)
	case cmdPermissionDecision:
		return reducePermissionDecision(state, in)
	case cmdPermissionAwait:
		return reducePermissionAwait(state, in)
	case cmdPersistAgentState:
		return reducePersistAgentState(state, in)
	case cmdPersistAgentStateImmediate:
		return reducePersistAgentStateImmediate(state, in)
	case cmdSetControlledByUser:
		return reduceSetControlledByUser(state, in)
	case cmdShutdown:
		return reduceShutdown(state, in)

	case evRunnerReady:
		return reduceRunnerReady(state, in)
	case evRunnerExited:
		return reduceRunnerExited(state, in)
	case evEngineSessionIdentified:
		if in.Gen == 0 || in.Gen == state.RunnerGen {
			state.ResumeToken = in.ResumeToken
			// Preserve legacy ClaudeSessionID for bridging and best-effort resume.
			state.ClaudeSessionID = in.ResumeToken
		}
		return state, nil
	case evEngineRolloutPath:
		if in.Gen == 0 || in.Gen == state.RunnerGen {
			state.RolloutPath = in.Path
		}
		return state, nil
	case evPermissionRequested:
		return reducePermissionRequested(state, in)
	case evDesktopTakeback:
		// Desktop takeback is a switch to local.
		return reduceSwitchMode(state, cmdSwitchMode{Target: ModeLocal, Reply: nil})
	case evWSConnected:
		state.WSConnected = true
		// If we reconnect after being offline, ensure the latest agent state is
		// persisted. This is critical for permission prompts: if a tool approval
		// was requested while disconnected, mobile clients can only discover it
		// via the durable `agentState.requests` blob.
		//
		// schedulePersistDebounced is idempotent; it will arm a timer only if one
		// isn't already pending.
		if state.AgentStateJSON != "" {
			return schedulePersistDebounced(state)
		}
		return state, nil
	case evWSDisconnected:
		state.WSConnected = false
		return state, nil
	case evMachineConnected:
		state.MachineConnected = true
		return state, nil
	case evMachineDisconnected:
		state.MachineConnected = false
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

// reduceInboundUserMessage handles a user message delivered from the server
// update stream (typically originating from the phone).
func reduceInboundUserMessage(state State, cmd cmdInboundUserMessage) (State, []actor.Effect) {
	if cmd.LocalID != "" && state.isRecentlySentOutboundUserLocalID(cmd.LocalID, cmd.NowMs) {
		return state, nil
	}

	text := normalizeRemoteInputText(cmd.Text)
	if text == "" {
		return state, nil
	}

	if state.FSM == StateRemoteRunning {
		return state, []actor.Effect{
			effRemoteSend{Gen: state.RunnerGen, Text: cmd.Text, Meta: cmd.Meta, LocalID: cmd.LocalID},
		}
	}

	state.PendingRemoteSends = append(state.PendingRemoteSends, pendingRemoteSend{
		text:    cmd.Text,
		meta:    cmd.Meta,
		localID: cmd.LocalID,
		nowMs:   cmd.NowMs,
	})

	switch state.FSM {
	case StateLocalRunning:
		return reduceSwitchMode(state, cmdSwitchMode{Target: ModeRemote, Reply: nil})
	case StateRemoteStarting:
		return state, nil
	default:
		return reduceSwitchMode(state, cmdSwitchMode{Target: ModeRemote, Reply: nil})
	}
}

// reduceOutboundMessageReady handles a runtime-produced outbound session message.
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

// reducePersistAgentState requests persisting the current agent state JSON
// using an optimistic concurrency version.
func reducePersistAgentState(state State, cmd cmdPersistAgentState) (State, []actor.Effect) {
	if cmd.AgentStateJSON == "" {
		return state, nil
	}
	state.AgentStateJSON = cmd.AgentStateJSON
	state.PersistRetryRemaining = 1
	return schedulePersistDebounced(state)
}

// rememberOutboundUserLocalID records a recently sent user message local id for dedupe.
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

// isRecentlySentOutboundUserLocalID reports whether localID was recently emitted.
func (state *State) isRecentlySentOutboundUserLocalID(localID string, nowMs int64) bool {
	localID = strings.TrimSpace(localID)
	if localID == "" || nowMs <= 0 {
		return false
	}

	const ttlMs = int64(30_000)
	cutoff := nowMs - ttlMs
	for i := len(state.RecentOutboundUserLocalIDs) - 1; i >= 0; i-- {
		rec := state.RecentOutboundUserLocalIDs[i]
		if rec.atMs < cutoff {
			break
		}
		if rec.id == localID {
			return true
		}
	}
	return false
}

// isRecentlyInjectedRemoteInput reports whether text was recently injected into remote mode.
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

// rememberRemoteInput records a remote/mobile input to suppress echo loops.
func (state *State) rememberRemoteInput(text string, nowMs int64) {
	text = normalizeRemoteInputText(text)
	if text == "" || nowMs <= 0 {
		return
	}

	const maxItems = 64
	const ttlMs = int64(20_000)
	cutoff := nowMs - ttlMs

	dst := state.RecentRemoteInputs[:0]
	for _, rec := range state.RecentRemoteInputs {
		if rec.atMs >= cutoff {
			dst = append(dst, rec)
		}
	}
	state.RecentRemoteInputs = dst

	state.RecentRemoteInputs = append(state.RecentRemoteInputs, remoteInputRecord{text: text, atMs: nowMs})
	if len(state.RecentRemoteInputs) > maxItems {
		state.RecentRemoteInputs = state.RecentRemoteInputs[len(state.RecentRemoteInputs)-maxItems:]
	}
}

// normalizeRemoteInputText normalizes user text for comparison and dedupe.
func normalizeRemoteInputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimSpace(text)
}

// reducePersistAgentStateImmediate requests persisting agent state immediately.
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

// reduceSetControlledByUser updates agentState.ControlledByUser without changing runners.
func reduceSetControlledByUser(state State, cmd cmdSetControlledByUser) (State, []actor.Effect) {
	if state.AgentState.ControlledByUser == cmd.ControlledByUser {
		return state, nil
	}
	state.AgentState.ControlledByUser = cmd.ControlledByUser
	state = refreshAgentStateJSON(state)
	return schedulePersistDebounced(state)
}

// reduceSwitchMode transitions the session between local and remote modes.
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
		state.RemoteRunner = runnerHandle{gen: gen, running: false}
		state.AgentState.ControlledByUser = false
		state = refreshAgentStateJSON(state)
		state.PersistRetryRemaining = 1
		state.PersistDebounceTimerArmed = false
		persistEffects := []actor.Effect{
			effCancelTimer{Name: persistDebounceTimerName},
			effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
		}
		effects := []actor.Effect{
			effStopLocalRunner{Gen: gen - 1},
			effStopDesktopTakebackWatcher{},
			effStartRemoteRunner{Gen: gen, Resume: state.ResumeToken},
		}
		effects = append(effects, persistEffects...)
		return state, effects
	case ModeLocal:
		state.Mode = ModeLocal
		state.FSM = StateLocalStarting
		state.LocalRunner = runnerHandle{gen: gen, running: false}
		state.AgentState.ControlledByUser = true
		state = refreshAgentStateJSON(state)
		state.PersistRetryRemaining = 1
		state.PersistDebounceTimerArmed = false
		persistEffects := []actor.Effect{
			effCancelTimer{Name: persistDebounceTimerName},
			effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
		}
		effects := []actor.Effect{
			effStopRemoteRunner{Gen: gen - 1},
			effStopDesktopTakebackWatcher{},
			effStartLocalRunner{Gen: gen, Resume: state.ResumeToken, RolloutPath: state.RolloutPath},
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

// reduceRunnerReady finalizes a runner start for the current generation.
func reduceRunnerReady(state State, ev evRunnerReady) (State, []actor.Effect) {
	if ev.Gen != state.RunnerGen {
		return state, nil
	}
	switch ev.Mode {
	case ModeLocal:
		state.FSM = StateLocalRunning
		state.LocalRunner = runnerHandle{gen: ev.Gen, running: true}
	case ModeRemote:
		state.FSM = StateRemoteRunning
		state.RemoteRunner = runnerHandle{gen: ev.Gen, running: true}
	}
	if state.PendingSwitchReply != nil {
		select {
		case state.PendingSwitchReply <- nil:
		default:
		}
		state.PendingSwitchReply = nil
	}

	var effects []actor.Effect
	if ev.Mode == ModeRemote {
		effects = append(effects, effStartDesktopTakebackWatcher{})
	}

	if ev.Mode == ModeRemote && len(state.PendingRemoteSends) > 0 {
		for _, pending := range state.PendingRemoteSends {
			effects = append(effects, effRemoteSend{
				Gen:     state.RunnerGen,
				Text:    pending.text,
				Meta:    pending.meta,
				LocalID: pending.localID,
			})
		}
		state.PendingRemoteSends = nil
		return state, effects
	}

	if len(effects) > 0 {
		return state, effects
	}
	return state, nil
}

// reduceRunnerExited records runner exits and drives any pending switch replies.
func reduceRunnerExited(state State, ev evRunnerExited) (State, []actor.Effect) {
	if ev.Gen != state.RunnerGen {
		return state, nil
	}
	switch ev.Mode {
	case ModeLocal:
		if state.LocalRunner.gen == ev.Gen {
			state.LocalRunner.running = false
		}
	case ModeRemote:
		if state.RemoteRunner.gen == ev.Gen {
			state.RemoteRunner.running = false
		}
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
		// Conservative: if remote failed to start while we have buffered inbound
		// user messages, fall back to injecting them into the local runner.
		if state.FSM == StateRemoteStarting && len(state.PendingRemoteSends) > 0 {
			var effects []actor.Effect
			for _, pending := range state.PendingRemoteSends {
				state.rememberRemoteInput(pending.text, pending.nowMs)
				effects = append(effects, effLocalSendLine{Gen: state.RunnerGen, Text: pending.text})
			}
			state.PendingRemoteSends = nil
			state.FSM = StateLocalRunning
			state.Mode = ModeLocal

			// Remote never became active; reclaim control for desktop and persist so
			// mobile clients don't get stuck in a "remote" UI state.
			state.AgentState.ControlledByUser = true
			state = refreshAgentStateJSON(state)
			state, persistEffects := schedulePersistDebounced(state)
			effects = append(effects, persistEffects...)
			return state, effects
		}

		// Otherwise: move to local running on failure (runtime will decide).
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
		state.LocalRunner = runnerHandle{gen: gen, running: false}
		state.AgentState.ControlledByUser = true
		state = refreshAgentStateJSON(state)
		state, persistEffects := schedulePersistDebounced(state)
		effects := []actor.Effect{
			effStopRemoteRunner{Gen: gen - 1},
			effStopDesktopTakebackWatcher{},
			effStartLocalRunner{Gen: gen, Resume: state.ResumeToken, RolloutPath: state.RolloutPath},
		}
		effects = append(effects, persistEffects...)
		return state, effects
	case StateLocalRunning:
		// Local runner exited; keep the actor alive but mark closed.
		state.FSM = StateClosed
		if ev.Err != nil {
			state.LastExitErr = ev.Err.Error()
		}
		return state, nil
	default:
		state.FSM = StateClosed
		if ev.Err != nil {
			state.LastExitErr = ev.Err.Error()
		}
		return state, nil
	}
}

// reduceRemoteSend forwards a user message to the remote runner if allowed.
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

// reduceAbortRemote requests aborting the current remote turn.
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

// reducePermissionRequested registers a new permission prompt and emits an ephemeral.
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

// reducePermissionDecision resolves a pending permission request.
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

	if state.PendingPermissionPromises != nil {
		if ch, ok := state.PendingPermissionPromises[cmd.RequestID]; ok {
			delete(state.PendingPermissionPromises, cmd.RequestID)
			if ch != nil {
				select {
				case ch <- PermissionDecision{Allow: cmd.Allow, Message: cmd.Message}:
				default:
				}
			}
		}
	}

	state = refreshAgentStateJSON(state)
	state, persistEffects := schedulePersistDebounced(state)

	var updatedInput json.RawMessage
	if cmd.Allow && input != "" {
		// Best-effort: Claude expects allow responses to include updatedInput
		// matching the original request input. If it's invalid JSON, omit it.
		var probe any
		if json.Unmarshal([]byte(input), &probe) == nil {
			updatedInput = json.RawMessage(input)
		}
	}
	var effects []actor.Effect
	// Only Claude remote mode expects control responses to be written to the
	// remote runner's stdin. Other agents (ACP/Codex) use PendingPermissionPromises.
	if state.FSM == StateRemoteRunning || state.FSM == StateRemoteStarting {
		message := cmd.Message
		// Match legacy behavior: for Claude tool permissions, allow responses
		// should omit the message to satisfy the SDK schema, but we still keep
		// the user-entered message in CompletedRequests.
		if cmd.Allow {
			message = ""
		}
		effects = append(effects, effRemotePermissionDecision{
			Gen:          state.RunnerGen,
			RequestID:    cmd.RequestID,
			Allow:        cmd.Allow,
			Message:      message,
			UpdatedInput: updatedInput,
		})
	}
	effects = append(effects, persistEffects...)
	return state, effects
}

// reducePermissionAwait registers a permission request and returns a decision via Reply.
func reducePermissionAwait(state State, cmd cmdPermissionAwait) (State, []actor.Effect) {
	if cmd.RequestID == "" || cmd.ToolName == "" || cmd.Reply == nil {
		return state, nil
	}

	if state.AgentState.Requests == nil {
		state.AgentState.Requests = make(map[string]types.AgentPendingRequest)
	}
	// Idempotent: if the request already exists, keep it and avoid emitting
	// duplicate durable state changes.
	_, exists := state.AgentState.Requests[cmd.RequestID]
	if !exists {
		state.AgentState.Requests[cmd.RequestID] = types.AgentPendingRequest{
			ToolName:  cmd.ToolName,
			Input:     string(cmd.Input),
			CreatedAt: cmd.NowMs,
		}
		state = refreshAgentStateJSON(state)
	}

	if state.PendingPermissionPromises == nil {
		state.PendingPermissionPromises = make(map[string]chan PermissionDecision)
	}
	state.PendingPermissionPromises[cmd.RequestID] = cmd.Reply

	var effects []actor.Effect
	// Always emit the ephemeral, even if the durable request already exists.
	//
	// Rationale:
	// - Synchronous engines (Codex/ACP) may retry cmdPermissionAwait to recover
	//   from transient websocket issues or missed UI updates.
	// - The phone UI dedupes by requestID, so duplicate ephemerals are safe.
	effects = append(effects, effEmitEphemeral{Payload: map[string]any{
		"type":      "permission-request",
		"id":        state.SessionID,
		"requestId": cmd.RequestID,
		"toolName":  cmd.ToolName,
		"input":     string(cmd.Input),
	}})
	if !exists {
		var persistEffects []actor.Effect
		state, persistEffects = schedulePersistDebounced(state)
		effects = append(effects, persistEffects...)
	}

	// Notify synchronous callers that the request is registered. This must be
	// non-blocking to keep the reducer pure and fast.
	if cmd.Ack != nil {
		select {
		case cmd.Ack <- struct{}{}:
		default:
		}
	}
	return state, effects
}

// reduceAgentStatePersisted applies the server-acknowledged agent state version.
func reduceAgentStatePersisted(state State, ev EvAgentStatePersisted) (State, []actor.Effect) {
	if ev.NewVersion > 0 {
		state.AgentStateVersion = ev.NewVersion
	}
	state.PersistRetryRemaining = 0
	return state, nil
}

// reduceAgentStateVersionMismatch updates local version and retries persistence.
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

// reduceAgentStatePersistFailed records a persistence failure and clears retries.
func reduceAgentStatePersistFailed(state State, ev EvAgentStatePersistFailed) (State, []actor.Effect) {
	_ = ev
	// If the websocket is disconnected, we rely on evWSConnected to trigger a
	// best-effort re-persist. Avoid retry loops while offline.
	if !state.WSConnected {
		return state, nil
	}

	// Best-effort: re-arm a single debounced persist. This recovers from brief
	// socket hiccups without requiring UI polling, but avoids tight loops by
	// relying on debounce.
	return schedulePersistDebounced(state)
}

// reduceTimerFired handles internal debounce timers.
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

// reduceShutdown transitions the actor to Closed and stops runners.
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
		effStopDesktopTakebackWatcher{},
	}
}

// refreshAgentStateJSON refreshes State.AgentStateJSON from State.AgentState.
func refreshAgentStateJSON(state State) State {
	data, err := json.Marshal(state.AgentState)
	if err != nil {
		// Keep existing JSON if we cannot marshal; this should be extremely rare.
		return state
	}
	state.AgentStateJSON = string(data)
	return state
}

// schedulePersistDebounced schedules a debounced persistence attempt.
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
