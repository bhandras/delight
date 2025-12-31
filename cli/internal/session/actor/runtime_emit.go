package actor

import (
	"context"
	"errors"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/protocol/wire"
)

// persistAgentState performs an asynchronous agent state update against the server.
func (r *Runtime) persistAgentState(ctx context.Context, eff effPersistAgentState, emit func(framework.Input)) {
	r.mu.Lock()
	updater := r.stateUpdater
	sessionID := r.sessionID
	r.mu.Unlock()

	if updater == nil || sessionID == "" {
		// Not configured yet; treat as no-op.
		return
	}

	// Persist asynchronously to avoid blocking the actor loop on socket IO.
	go func() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		newVersion, err := updater.UpdateState(sessionID, eff.AgentStateJSON, eff.ExpectedVersion)
		if err == nil {
			emit(EvAgentStatePersisted{NewVersion: newVersion})
			return
		}

		// Special case: version mismatch should be retried by the reducer with the server version.
		if errors.Is(err, websocket.ErrVersionMismatch) {
			if newVersion <= 0 {
				// If the server didn't provide a version, treat as a generic failure.
				emit(EvAgentStatePersistFailed{Err: err})
				return
			}
			emit(EvAgentStateVersionMismatch{ServerVersion: newVersion})
			return
		}
		emit(EvAgentStatePersistFailed{Err: err})
	}()
}

// startTimer schedules a single named timer and emits evTimerFired when it fires.
func (r *Runtime) startTimer(ctx context.Context, eff effStartTimer, emit func(framework.Input)) {
	if eff.Name == "" || eff.AfterMs <= 0 {
		return
	}

	r.mu.Lock()
	if r.timers == nil {
		r.timers = make(map[string]*time.Timer)
	}
	if prev := r.timers[eff.Name]; prev != nil {
		prev.Stop()
	}
	after := time.Duration(eff.AfterMs) * time.Millisecond
	r.timers[eff.Name] = time.AfterFunc(after, func() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		emit(evTimerFired{Name: eff.Name, NowMs: time.Now().UnixMilli()})
	})
	r.mu.Unlock()
}

// cancelTimer cancels a previously started named timer.
func (r *Runtime) cancelTimer(eff effCancelTimer) {
	if eff.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timers == nil {
		return
	}
	if t := r.timers[eff.Name]; t != nil {
		t.Stop()
	}
	delete(r.timers, eff.Name)
}

// emitEphemeral emits a session-scoped ephemeral payload to the server.
func (r *Runtime) emitEphemeral(eff effEmitEphemeral) {
	r.mu.Lock()
	emitter := r.socketEmitter
	r.mu.Unlock()
	if emitter == nil {
		return
	}
	_ = emitter.EmitEphemeral(eff.Payload)
}

// emitMessage emits an encrypted session message to the server.
func (r *Runtime) emitMessage(eff effEmitMessage) {
	r.mu.Lock()
	emitter := r.socketEmitter
	sessionID := r.sessionID
	r.mu.Unlock()
	if emitter == nil {
		return
	}
	if sessionID == "" || eff.Ciphertext == "" {
		return
	}
	_ = emitter.EmitMessage(wire.OutboundMessagePayload{
		SID:     sessionID,
		LocalID: eff.LocalID,
		Message: eff.Ciphertext,
	})
}

