package actor

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/acp"
	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
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
	agent     agentengine.AgentType

	emitFn func(framework.Input)

	stateUpdater  StateUpdater
	socketEmitter SocketEmitter

	encryptFn func([]byte) (string, error)

	timers map[string]*time.Timer

	engineType agentengine.AgentType
	engine     agentengine.AgentEngine

	engineLocalGen         int64
	engineRemoteGen        int64
	engineLocalActive      bool
	engineRemoteActive     bool
	engineLocalInteractive bool

	engineSendMu sync.Mutex

	acpURL          string
	acpAgent        string
	acpSessionID    string
	acpClient       *acp.Client
	acpRemoteGen    int64
	acpRemoteActive bool
	acpLocalGen     int64
	acpLocalActive  bool

	fakeRemoteGen    int64
	fakeRemoteActive bool
	fakeLocalGen     int64
	fakeLocalActive  bool

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
	return &Runtime{workDir: workDir, debug: debug, agent: agentengine.AgentClaude}
}

// WithAgent configures which upstream agent implementation this runtime starts.
func (r *Runtime) WithAgent(agent string) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent == "" {
		r.agent = agentengine.AgentClaude
		return r
	}
	r.agent = agentengine.AgentType(agent)
	return r
}

// WithACPConfig configures the ACP client parameters for agentengine.AgentACP sessions.
func (r *Runtime) WithACPConfig(url string, agent string, sessionID string) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.acpURL = url
	r.acpAgent = agent
	r.acpSessionID = sessionID
	// Recreate client lazily on next use.
	r.acpClient = nil
	return r
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
	r.mu.Lock()
	r.emitFn = emit
	r.mu.Unlock()

	for _, eff := range effects {
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch e := eff.(type) {
		case effStartLocalRunner:
			switch r.agent {
			case agentengine.AgentClaude:
				r.startEngineLocal(ctx, e, emit)
			case agentengine.AgentCodex:
				r.startEngineLocal(ctx, e, emit)
			case agentengine.AgentACP:
				r.startACPLocal(ctx, e, emit)
			case agentengine.AgentFake:
				r.startFakeLocal(ctx, e, emit)
			default:
				r.startEngineLocal(ctx, e, emit)
			}
		case effStopLocalRunner:
			switch r.agent {
			case agentengine.AgentClaude:
				r.stopEngineLocal(e)
			case agentengine.AgentCodex:
				r.stopEngineLocal(e)
			case agentengine.AgentACP:
				r.stopACPLocal(e)
			case agentengine.AgentFake:
				r.stopFakeLocal(e)
			default:
				r.stopEngineLocal(e)
			}
		case effStartRemoteRunner:
			switch r.agent {
			case agentengine.AgentClaude:
				r.startEngineRemote(ctx, e, emit)
			case agentengine.AgentCodex:
				r.startEngineRemote(ctx, e, emit)
			case agentengine.AgentACP:
				r.startACPRemote(ctx, e, emit)
			case agentengine.AgentFake:
				r.startFakeRemote(ctx, e, emit)
			default:
				r.startEngineRemote(ctx, e, emit)
			}
		case effStopRemoteRunner:
			switch r.agent {
			case agentengine.AgentClaude:
				r.stopEngineRemote(e)
			case agentengine.AgentCodex:
				r.stopEngineRemote(e)
			case agentengine.AgentACP:
				r.stopACPRemote(e)
			case agentengine.AgentFake:
				r.stopFakeRemote(e)
			default:
				r.stopEngineRemote(e)
			}
		case effLocalSendLine:
			// Local line injection is currently only supported for Claude.
			r.engineLocalSendLine(e)
		case effRemoteSend:
			switch r.agent {
			case agentengine.AgentClaude:
				r.engineRemoteSend(ctx, e)
			case agentengine.AgentCodex:
				r.engineRemoteSend(ctx, e)
			case agentengine.AgentACP:
				r.acpRemoteSend(ctx, e, emit)
			case agentengine.AgentFake:
				r.fakeRemoteSend(ctx, e, emit)
			default:
				r.engineRemoteSend(ctx, e)
			}
		case effRemoteAbort:
			r.engineRemoteAbort(ctx, e)
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
	engine := r.engine
	r.engine = nil
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

	if engine != nil {
		_ = engine.Close(context.Background())
	}
}
