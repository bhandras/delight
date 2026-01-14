package actor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
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
	encryptFn := r.encryptFn
	sessionID := r.sessionID
	debug := r.debug
	r.mu.Unlock()
	if emitter == nil {
		if debug {
			logger.Debugf("session: emit ephemeral skipped (no socket emitter)")
		}
		return
	}
	if debug {
		typ := ""
		if payload, ok := eff.Payload.(map[string]any); ok {
			if t, _ := payload["type"].(string); t != "" {
				typ = t
			} else if t, _ := payload["t"].(string); t != "" {
				typ = t
			}
		}
		if typ != "" {
			logger.Debugf("session: emit ephemeral type=%s", typ)
		} else {
			logger.Debugf("session: emit ephemeral")
		}
	}

	// Persist UI events as encrypted session messages so mobile clients can
	// recover progress updates after reconnecting (e.g. after backgrounding).
	if payload, ok := eff.Payload.(wire.EphemeralUIEventPayload); ok {
		r.persistUIEventMessage(emitter, encryptFn, sessionID, payload, debug)
	}

	// Activity ephemerals ("working"/busy) are transient and may be missed when
	// clients are backgrounded. Instead of forwarding them as raw ephemerals, we
	// route them through the server's durable `session-alive` path so the server
	// can persist turn boundaries and clients can recover correct state after
	// reconnect.
	if payload, ok := eff.Payload.(wire.EphemeralActivityPayload); ok {
		sid := sessionID
		if sid == "" {
			sid = payload.ID
		}
		if sid != "" {
			if err := emitter.EmitRaw("session-alive", wire.SessionAlivePayload{
				SID:      sid,
				Time:     payload.ActiveAt,
				Working:  payload.Working,
			}); err != nil && debug {
				logger.Debugf("session: emit session-alive failed: %v", err)
			}
		}
		return
	}

	if err := emitter.EmitEphemeral(eff.Payload); err != nil && debug {
		logger.Debugf("session: emit ephemeral failed: %v", err)
	}
}

// persistUIEventMessage stores a `ui.event` ephemeral as an encrypted session
// message. The server cannot encrypt on behalf of clients, so the CLI must
// encrypt before persistence.
func (r *Runtime) persistUIEventMessage(
	emitter SocketEmitter,
	encryptFn func([]byte) (string, error),
	sessionID string,
	payload wire.EphemeralUIEventPayload,
	debug bool,
) {
	if emitter == nil || encryptFn == nil {
		return
	}
	if payload.Type != "ui.event" || payload.SessionID == "" || payload.EventID == "" {
		return
	}

	record := wire.NewUIEventRecord(payload)
	plaintext, err := json.Marshal(record)
	if err != nil {
		return
	}
	ciphertext, err := encryptFn(plaintext)
	if err != nil {
		return
	}
	if sessionID == "" {
		sessionID = payload.SessionID
	}
	if sessionID == "" {
		return
	}
	if err := emitter.EmitMessage(wire.OutboundMessagePayload{
		SID:     sessionID,
		Message: ciphertext,
	}); err != nil && debug {
		logger.Debugf("session: persist ui.event message failed: %v", err)
	}
}

// emitMessage emits an encrypted session message to the server.
func (r *Runtime) emitMessage(eff effEmitMessage) {
	r.mu.Lock()
	emitter := r.socketEmitter
	sessionID := r.sessionID
	debug := r.debug
	r.mu.Unlock()
	if emitter == nil {
		if debug {
			logger.Debugf("session: emit message skipped (no socket emitter)")
		}
		return
	}
	if sessionID == "" || eff.Ciphertext == "" {
		if debug {
			logger.Debugf("session: emit message skipped (sessionID=%t ciphertext=%t)", sessionID != "", eff.Ciphertext != "")
		}
		return
	}
	if err := emitter.EmitMessage(wire.OutboundMessagePayload{
		SID:     sessionID,
		LocalID: eff.LocalID,
		Message: eff.Ciphertext,
	}); err != nil {
		logger.Errorf("session: emit message failed: %v", err)
	}
}
