package actor

import (
	"context"
	"os"
	"sync"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/claude"
	"golang.org/x/term"
)

// Runtime interprets SessionActor effects for the Delight CLI.
//
// IMPORTANT: Runtime must never mutate SessionActor state directly. It only
// emits events back into the actor mailbox via the provided emit function.
type Runtime struct {
	mu sync.Mutex

	sessionID string
	workDir   string
	debug     bool

	stateUpdater  StateUpdater
	socketEmitter SocketEmitter

	encryptFn func([]byte) (string, error)

	timers map[string]*time.Timer

	localProc    *claude.Process
	localGen     int64
	localScanner *claude.Scanner
	remoteBridge *claude.RemoteBridge
	remoteGen    int64

	takebackCancel chan struct{}
	takebackDone   chan struct{}
	takebackTTY    *os.File
	takebackState  *term.State
}

// StateUpdater persists session agent state to a server.
type StateUpdater interface {
	UpdateState(sessionID string, agentState string, version int64) (int64, error)
}

// SocketEmitter emits session-scoped messages + ephemerals to the server.
type SocketEmitter interface {
	EmitEphemeral(data any) error
	EmitMessage(data any) error
	EmitRaw(event string, data any) error
}

// NewRuntime returns a Runtime that executes runner effects in the given workDir.
func NewRuntime(workDir string, debug bool) *Runtime {
	return &Runtime{workDir: workDir, debug: debug}
}

// WithSessionID configures the session id used for state persistence effects.
func (r *Runtime) WithSessionID(sessionID string) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = sessionID
	return r
}

// WithStateUpdater configures the persistence adapter used for agent state updates.
func (r *Runtime) WithStateUpdater(updater StateUpdater) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Beware: an interface can be non-nil while holding a nil concrete pointer.
	// This can happen when Manager wires the runtime before websocket client
	// construction; persist effects must treat that as "no updater".
	if isNilInterface(updater) {
		r.stateUpdater = nil
	} else {
		r.stateUpdater = updater
	}
	return r
}

// WithSocketEmitter configures the socket emitter used for emit-message and
// emit-ephemeral effects.
func (r *Runtime) WithSocketEmitter(emitter SocketEmitter) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	if isNilInterface(emitter) {
		r.socketEmitter = nil
	} else {
		r.socketEmitter = emitter
	}
	return r
}

// WithEncryptFn configures the encryption function used for outbound session
// messages (plaintext JSON -> ciphertext string).
func (r *Runtime) WithEncryptFn(fn func([]byte) (string, error)) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.encryptFn = fn
	return r
}

// HandleEffects implements actor.Runtime.
func (r *Runtime) HandleEffects(ctx context.Context, effects []framework.Effect, emit func(framework.Input)) {
	for _, eff := range effects {
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch e := eff.(type) {
		case effStartLocalRunner:
			r.startLocal(ctx, e, emit)
		case effStopLocalRunner:
			r.stopLocal(e)
		case effStartRemoteRunner:
			r.startRemote(ctx, e, emit)
		case effStopRemoteRunner:
			r.stopRemote(e)
		case effLocalSendLine:
			r.localSendLine(e)
		case effRemoteSend:
			r.remoteSend(e)
		case effRemoteAbort:
			r.remoteAbort(e)
		case effRemotePermissionDecision:
			r.remotePermissionDecision(e)
		case effPersistAgentState:
			r.persistAgentState(ctx, e, emit)
		case effStartDesktopTakebackWatcher:
			r.startDesktopTakebackWatcher(ctx, emit)
		case effStopDesktopTakebackWatcher:
			r.stopDesktopTakebackWatcher()
		case effStartTimer:
			r.startTimer(ctx, e, emit)
		case effCancelTimer:
			r.cancelTimer(e)
		case effEmitEphemeral:
			r.emitEphemeral(e)
		case effEmitMessage:
			r.emitMessage(e)
		default:
			// Unknown effect: ignore.
		}
	}
}

// Stop implements actor.Runtime.
func (r *Runtime) Stop() {
	r.mu.Lock()
	cancel, done, tty, state := r.stopDesktopTakebackWatcherLocked()
	for _, timer := range r.timers {
		timer.Stop()
	}
	r.timers = nil
	if r.remoteBridge != nil {
		r.remoteBridge.Kill()
		r.remoteBridge = nil
	}
	if r.localProc != nil {
		r.localProc.Kill()
		r.localProc = nil
	}
	r.mu.Unlock()

	if cancel != nil {
		func() {
			defer func() { recover() }()
			close(cancel)
		}()
	}
	if state != nil && tty != nil {
		_ = term.Restore(int(tty.Fd()), state)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(takebackShutdownWait):
		}
	}
}
