package actor

import (
	"encoding/json"
	"strings"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
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
	case cmdWaitForAgentStatePersist:
		return reduceWaitForAgentStatePersist(state, in)
	case cmdSetControlledByUser:
		return reduceSetControlledByUser(state, in)
	case cmdSetAgentConfig:
		return reduceSetAgentConfig(state, in)
	case cmdGetAgentEngineSettings:
		return reduceGetAgentEngineSettings(state, in)
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
	case evEngineThinking:
		return reduceEngineThinking(state, in)
	case evEngineUIEvent:
		return reduceEngineUIEvent(state, in)
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
	case evTerminalConnected:
		state.TerminalConnected = true
		return state, nil
	case evTerminalDisconnected:
		state.TerminalConnected = false
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
	state.PersistInFlight = true
	return state, []actor.Effect{
		effCancelTimer{Name: persistDebounceTimerName},
		effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
	}
}

// reduceWaitForAgentStatePersist requests persisting the current agent state
// immediately and notifies cmd.Reply once persistence succeeds or fails.
func reduceWaitForAgentStatePersist(state State, cmd cmdWaitForAgentStatePersist) (State, []actor.Effect) {
	if cmd.Reply == nil {
		return state, nil
	}
	if state.AgentStateJSON == "" {
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: nil}}
	}

	state.PersistWaiters = append(state.PersistWaiters, cmd.Reply)

	// If a persist is already in flight, just wait for the eventual ack/fail.
	if state.PersistInFlight {
		return state, nil
	}

	state.PersistRetryRemaining = 1
	state.PersistDebounceTimerArmed = false
	state.PersistInFlight = true
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

// currentAgentConfig returns the engine configuration derived from the durable
// session agent state.
func currentAgentConfig(state State) agentengine.AgentConfig {
	return agentengine.AgentConfig{
		Model:           strings.TrimSpace(state.AgentState.Model),
		PermissionMode:  strings.TrimSpace(state.AgentState.PermissionMode),
		ReasoningEffort: strings.TrimSpace(state.AgentState.ReasoningEffort),
	}
}

// reduceSetAgentConfig updates durable agent configuration (model, permission
// mode, reasoning effort) and, when a remote runner is active, requests that
// the engine apply the updated configuration.
func reduceSetAgentConfig(state State, cmd cmdSetAgentConfig) (State, []actor.Effect) {
	// Remote-only: config changes must only happen when the phone controls the
	// session. This prevents surprising local TUI behavior.
	if state.Mode != ModeRemote || state.AgentState.ControlledByUser {
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrNotRemote}}
	}

	changed := false
	if model := strings.TrimSpace(cmd.Model); model != "" && model != state.AgentState.Model {
		state.AgentState.Model = model
		changed = true
	}
	if permissionMode := strings.TrimSpace(cmd.PermissionMode); permissionMode != "" && permissionMode != state.AgentState.PermissionMode {
		state.AgentState.PermissionMode = permissionMode
		changed = true
	}
	if effort := strings.TrimSpace(cmd.ReasoningEffort); effort != "" && effort != state.AgentState.ReasoningEffort {
		state.AgentState.ReasoningEffort = effort
		changed = true
	}

	if !changed {
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: nil}}
	}

	state = refreshAgentStateJSON(state)

	// Persist immediately so mobile clients see the update after pressing Apply.
	state, effects := persistAgentStateImmediately(state)

	// If a remote runner is active, request that the engine apply the updated
	// config best-effort.
	if state.FSM == StateRemoteRunning {
		effects = append(effects, effApplyEngineConfig{Gen: state.RunnerGen, Config: currentAgentConfig(state)})
	}

	if cmd.Reply != nil {
		effects = append(effects, effCompleteReply{Reply: cmd.Reply, Err: nil})
	}
	return state, effects
}

// reduceGetAgentEngineSettings schedules a runtime query for the current agent
// engine capabilities and configuration.
func reduceGetAgentEngineSettings(state State, cmd cmdGetAgentEngineSettings) (State, []actor.Effect) {
	if cmd.Reply == nil {
		return state, nil
	}

	return state, []actor.Effect{
		effQueryAgentEngineSettings{
			Gen:       state.RunnerGen,
			AgentType: agentengine.AgentType(state.AgentState.AgentType),
			Desired:   currentAgentConfig(state),
			Reply:     cmd.Reply,
		},
	}
}

// reduceSwitchMode transitions the session between local and remote modes.
func reduceSwitchMode(state State, cmd cmdSwitchMode) (State, []actor.Effect) {
	// Idempotent behavior.
	if cmd.Target == state.Mode && (state.FSM == StateLocalRunning || state.FSM == StateRemoteRunning) {
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: nil}}
	}

	state.RunnerGen++
	gen := state.RunnerGen
	state.PersistRetryRemaining = 1

	switch cmd.Target {
	case ModeRemote:
		// Only keep the latest pending switch reply (callers should serialize in higher layers).
		state.PendingSwitchReply = cmd.Reply
		state.Mode = ModeRemote
		state.FSM = StateRemoteStarting
		state.Thinking = false
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
			effStartRemoteRunner{Gen: gen, Resume: state.ResumeToken, Config: currentAgentConfig(state)},
		}
		effects = append(effects, persistEffects...)
		return state, effects
	case ModeLocal:
		// Only keep the latest pending switch reply (callers should serialize in higher layers).
		state.PendingSwitchReply = cmd.Reply
		state.Mode = ModeLocal
		state.FSM = StateLocalStarting
		state.Thinking = false
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
			effStartLocalRunner{Gen: gen, Resume: state.ResumeToken, RolloutPath: state.RolloutPath, Config: currentAgentConfig(state)},
		}
		effects = append(effects, persistEffects...)
		return state, effects
	default:
		// Unknown target; respond error if present.
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrInvalidMode}}
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
	var effects []actor.Effect
	if state.PendingSwitchReply != nil {
		effects = append(effects, effCompleteReply{Reply: state.PendingSwitchReply, Err: nil})
		state.PendingSwitchReply = nil
	}
	if ev.Mode == ModeRemote {
		effects = append(effects, effStartDesktopTakebackWatcher{})

		// Best-effort: ensure the running engine has the latest durable agent
		// configuration, even if it was updated while remote startup was still
		// in progress.
		effects = append(effects, effApplyEngineConfig{Gen: state.RunnerGen, Config: currentAgentConfig(state)})
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
	var effects []actor.Effect
	switch ev.Mode {
	case ModeLocal:
		if state.LocalRunner.gen == ev.Gen {
			state.LocalRunner.running = false
		}
	case ModeRemote:
		if state.RemoteRunner.gen == ev.Gen {
			state.RemoteRunner.running = false
		}
		state.Thinking = false
	}
	// If we were starting, fail the pending switch.
	if state.FSM == StateLocalStarting || state.FSM == StateRemoteStarting {
		if state.PendingSwitchReply != nil {
			effects = append(effects, effCompleteReply{Reply: state.PendingSwitchReply, Err: ev.Err})
			state.PendingSwitchReply = nil
		}
		// Conservative: if remote failed to start while we have buffered inbound
		// user messages, fall back to injecting them into the local runner.
		if state.FSM == StateRemoteStarting && len(state.PendingRemoteSends) > 0 {
			outEffects := make([]actor.Effect, 0, len(state.PendingRemoteSends)+len(effects)+2)
			outEffects = append(outEffects, effects...)
			for _, pending := range state.PendingRemoteSends {
				state.rememberRemoteInput(pending.text, pending.nowMs)
				outEffects = append(outEffects, effLocalSendLine{Gen: state.RunnerGen, Text: pending.text})
			}
			state.PendingRemoteSends = nil
			state.FSM = StateLocalRunning
			state.Mode = ModeLocal

			// Remote never became active; reclaim control for desktop and persist so
			// mobile clients don't get stuck in a "remote" UI state.
			state.AgentState.ControlledByUser = true
			state = refreshAgentStateJSON(state)
			state, persistEffects := schedulePersistDebounced(state)
			outEffects = append(outEffects, persistEffects...)
			return state, outEffects
		}

		// Otherwise: move to local running on failure (runtime will decide).
		state.FSM = StateLocalRunning
		state.Mode = ModeLocal
		state.Thinking = false
		return state, effects
	}

	// If we were running, transition to a safe state.
	switch state.FSM {
	case StateRemoteRunning:
		// Remote runner exited unexpectedly; fall back to local runner.
		state.RunnerGen++
		gen := state.RunnerGen
		state.Mode = ModeLocal
		state.FSM = StateLocalStarting
		state.Thinking = false
		state.LocalRunner = runnerHandle{gen: gen, running: false}
		state.AgentState.ControlledByUser = true
		state = refreshAgentStateJSON(state)
		state, persistEffects := schedulePersistDebounced(state)
		effects := []actor.Effect{
			effStopRemoteRunner{Gen: gen - 1},
			effStopDesktopTakebackWatcher{},
			effStartLocalRunner{Gen: gen, Resume: state.ResumeToken, RolloutPath: state.RolloutPath, Config: currentAgentConfig(state)},
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
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrNotRemote}}
	}
	effects := []actor.Effect{
		effRemoteSend{Gen: state.RunnerGen, Text: cmd.Text, Meta: cmd.Meta, LocalID: cmd.LocalID},
	}
	if cmd.Reply != nil {
		effects = append(effects, effCompleteReply{Reply: cmd.Reply, Err: nil})
	}
	return state, effects
}

// reduceEngineThinking applies engine "thinking" signals to the session state
// and emits an activity ephemeral so mobile clients can render status updates.
func reduceEngineThinking(state State, ev evEngineThinking) (State, []actor.Effect) {
	if ev.Gen != 0 && ev.Gen != state.RunnerGen {
		return state, nil
	}
	// Only surface thinking updates while remote mode is running. This avoids
	// toggling mobile UI while the desktop owns the session.
	if ev.Mode != ModeRemote || state.FSM != StateRemoteRunning {
		return state, nil
	}
	if state.SessionID == "" {
		return state, nil
	}
	if state.Thinking == ev.Thinking {
		return state, nil
	}

	state.Thinking = ev.Thinking
	activeAt := ev.NowMs
	if activeAt < 0 {
		activeAt = 0
	}

	return state, []actor.Effect{
		effEmitEphemeral{Payload: wire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       state.SessionID,
			Active:   true,
			Thinking: state.Thinking,
			ActiveAt: activeAt,
		}},
	}
}

// reduceEngineUIEvent forwards rendered UI events (tool/thinking) to mobile clients.
func reduceEngineUIEvent(state State, ev evEngineUIEvent) (State, []actor.Effect) {
	if ev.Gen != 0 && ev.Gen != state.RunnerGen {
		return state, nil
	}
	if ev.Mode != ModeRemote || state.FSM != StateRemoteRunning {
		return state, nil
	}
	if state.SessionID == "" || ev.EventID == "" {
		return state, nil
	}

	if ev.Kind == string(agentengine.UIEventThinking) {
		thinking := ev.Phase != string(agentengine.UIEventPhaseEnd)
		state.Thinking = thinking
	}

	return state, []actor.Effect{
		effEmitEphemeral{Payload: wire.EphemeralUIEventPayload{
			Type:          "ui.event",
			SessionID:     state.SessionID,
			EventID:       ev.EventID,
			Kind:          ev.Kind,
			Phase:         ev.Phase,
			Status:        ev.Status,
			BriefMarkdown: ev.BriefMarkdown,
			FullMarkdown:  ev.FullMarkdown,
			AtMs:          ev.NowMs,
		}},
	}
}

// reduceAbortRemote requests aborting the current remote turn.
func reduceAbortRemote(state State, cmd cmdAbortRemote) (State, []actor.Effect) {
	if state.FSM != StateRemoteRunning {
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrNotRemote}}
	}
	effects := []actor.Effect{effRemoteAbort{Gen: state.RunnerGen}}
	if cmd.Reply != nil {
		effects = append(effects, effCompleteReply{Reply: cmd.Reply, Err: nil})
	}
	return state, effects
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
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrUnknownPermissionRequest}}
	}
	if state.AgentState.Requests == nil {
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrUnknownPermissionRequest}}
	}
	if _, ok := state.AgentState.Requests[cmd.RequestID]; !ok {
		if cmd.Reply == nil {
			return state, nil
		}
		return state, []actor.Effect{effCompleteReply{Reply: cmd.Reply, Err: ErrUnknownPermissionRequest}}
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

	// Engines (Codex/Claude remote) that need to block on approvals use
	// PendingPermissionPromises. The engine itself is responsible for writing
	// any upstream protocol response once the promise resolves.
	if cmd.Reply != nil {
		persistEffects = append(persistEffects, effCompleteReply{Reply: cmd.Reply, Err: nil})
	}
	return state, persistEffects
}

// reducePermissionAwait registers a permission request and returns a decision via Reply.
func reducePermissionAwait(state State, cmd cmdPermissionAwait) (State, []actor.Effect) {
	if cmd.RequestID == "" || cmd.ToolName == "" || cmd.Reply == nil {
		return state, nil
	}

	var effects []actor.Effect

	permissionMode := strings.TrimSpace(state.AgentState.PermissionMode)
	switch permissionMode {
	case "yolo", "safe-yolo":
		// Auto-approve: bypass UI prompts entirely.
		if state.AgentState.CompletedRequests == nil {
			state.AgentState.CompletedRequests = make(map[string]types.AgentCompletedRequest)
		}
		state.AgentState.CompletedRequests[cmd.RequestID] = types.AgentCompletedRequest{
			ToolName:   cmd.ToolName,
			Input:      string(cmd.Input),
			Allow:      true,
			Message:    "",
			ResolvedAt: cmd.NowMs,
		}
		state = refreshAgentStateJSON(state)
		state, effects = persistAgentStateImmediately(state)
		if cmd.Ack != nil {
			effects = append(effects, effSignalAck{Ack: cmd.Ack})
		}

		// Resolve the synchronous promise after state application.
		effects = append(effects, effCompletePermissionDecision{
			Reply:    cmd.Reply,
			Decision: PermissionDecision{Allow: true, Message: ""},
		})
		return state, effects
	case "read-only":
		// Auto-deny: in read-only mode, no tools should run.
		if state.AgentState.CompletedRequests == nil {
			state.AgentState.CompletedRequests = make(map[string]types.AgentCompletedRequest)
		}
		state.AgentState.CompletedRequests[cmd.RequestID] = types.AgentCompletedRequest{
			ToolName:   cmd.ToolName,
			Input:      string(cmd.Input),
			Allow:      false,
			Message:    "denied (read-only mode)",
			ResolvedAt: cmd.NowMs,
		}
		state = refreshAgentStateJSON(state)
		state, effects = persistAgentStateImmediately(state)
		if cmd.Ack != nil {
			effects = append(effects, effSignalAck{Ack: cmd.Ack})
		}

		effects = append(effects, effCompletePermissionDecision{
			Reply:    cmd.Reply,
			Decision: PermissionDecision{Allow: false, Message: "denied (read-only mode)"},
		})
		return state, effects
	default:
		// default: prompt user via phone UI
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

	effects = effects[:0]
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

	// Notify synchronous callers that the request is registered (after state is
	// applied), to avoid races where engines proceed before durable state exists.
	if cmd.Ack != nil {
		effects = append(effects, effSignalAck{Ack: cmd.Ack})
	}
	return state, effects
}

// reduceAgentStatePersisted applies the server-acknowledged agent state version.
func reduceAgentStatePersisted(state State, ev EvAgentStatePersisted) (State, []actor.Effect) {
	if ev.NewVersion > 0 {
		state.AgentStateVersion = ev.NewVersion
	}
	state.PersistRetryRemaining = 0
	state.PersistInFlight = false
	if len(state.PersistWaiters) > 0 {
		effects := make([]actor.Effect, 0, len(state.PersistWaiters))
		for _, waiter := range state.PersistWaiters {
			if waiter == nil {
				continue
			}
			effects = append(effects, effCompleteReply{Reply: waiter, Err: nil})
		}
		state.PersistWaiters = nil
		return state, effects
	}
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
	state.PersistInFlight = true
	return state, []actor.Effect{effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion}}
}

// reduceAgentStatePersistFailed records a persistence failure and clears retries.
func reduceAgentStatePersistFailed(state State, ev EvAgentStatePersistFailed) (State, []actor.Effect) {
	state.PersistInFlight = false
	var effects []actor.Effect
	if len(state.PersistWaiters) > 0 {
		effects = make([]actor.Effect, 0, len(state.PersistWaiters))
		for _, waiter := range state.PersistWaiters {
			if waiter == nil {
				continue
			}
			effects = append(effects, effCompleteReply{Reply: waiter, Err: ev.Err})
		}
		state.PersistWaiters = nil
	}
	// If the websocket is disconnected, we rely on evWSConnected to trigger a
	// best-effort re-persist. Avoid retry loops while offline.
	if !state.WSConnected {
		return state, effects
	}

	// Best-effort: re-arm a single debounced persist. This recovers from brief
	// socket hiccups without requiring UI polling, but avoids tight loops by
	// relying on debounce.
	state, persistEffects := schedulePersistDebounced(state)
	effects = append(effects, persistEffects...)
	return state, effects
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
	// Stop both runners; runtime will emit exit events.
	effects := []actor.Effect{
		effStopLocalRunner{Gen: state.RunnerGen},
		effStopRemoteRunner{Gen: state.RunnerGen, Silent: true},
		effStopDesktopTakebackWatcher{},
	}
	if cmd.Reply != nil {
		effects = append(effects, effCompleteReply{Reply: cmd.Reply, Err: nil})
	}
	return state, effects
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

// persistAgentStateImmediately requests persisting agent state immediately.
//
// This is used for low-frequency updates (like model / permission changes)
// where the mobile UI expects the state to round-trip quickly after an RPC
// call.
func persistAgentStateImmediately(state State) (State, []actor.Effect) {
	if state.AgentStateJSON == "" {
		return state, nil
	}

	state.PersistRetryRemaining = 1
	state.PersistDebounceTimerArmed = false
	state.PersistInFlight = true
	return state, []actor.Effect{
		effCancelTimer{Name: persistDebounceTimerName},
		effPersistAgentState{AgentStateJSON: state.AgentStateJSON, ExpectedVersion: state.AgentStateVersion},
	}
}

// schedulePersistDebounced schedules a debounced persistence attempt.
func schedulePersistDebounced(state State) (State, []actor.Effect) {
	state.PersistRetryRemaining = 1
	// If we have no websocket connection yet, still schedule persistence; the
	// runtime can treat it as a no-op, but the state remains marked dirty.
	state.PersistDebounceTimerArmed = true
	state.PersistInFlight = false
	return state, []actor.Effect{
		effCancelTimer{Name: persistDebounceTimerName},
		effStartTimer{Name: persistDebounceTimerName, AfterMs: persistDebounceAfterMs},
	}
}
