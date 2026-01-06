package actor

import (
	"context"
	"os"
	"sync"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/termutil"
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

	acpURL       string
	acpAgent     string
	acpSessionID string

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
	return r
}

// ACPConfig returns the current ACP runtime configuration.
func (r *Runtime) ACPConfig() (baseURL string, agentName string, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.acpURL, r.acpAgent, r.acpSessionID
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
			r.startEngineLocal(ctx, e, emit)
		case effStopLocalRunner:
			r.stopEngineLocal(e)
		case effStartRemoteRunner:
			r.startEngineRemote(ctx, e, emit)
		case effStopRemoteRunner:
			r.stopEngineRemote(e)
		case effLocalSendLine:
			// Local line injection is currently only supported for Claude.
			r.engineLocalSendLine(e)
		case effRemoteSend:
			r.engineRemoteSend(ctx, e)
		case effRemoteAbort:
			r.engineRemoteAbort(ctx, e)
		case effPersistAgentState:
			r.persistAgentState(ctx, e, emit)
		case effApplyEngineConfig:
			r.applyEngineConfig(ctx, e)
		case effQueryAgentEngineSettings:
			r.queryAgentEngineSettings(ctx, e, emit)
		case effStartDesktopTakebackWatcher:
			r.startDesktopTakebackWatcher(ctx, emit)
		case effStopDesktopTakebackWatcher:
			r.stopDesktopTakebackWatcher()
		case effStartTimer:
			r.startTimer(ctx, e, emit)
		case effCancelTimer:
			r.cancelTimer(e)
		case effCompleteReply:
			if e.Reply != nil {
				select {
				case e.Reply <- e.Err:
				default:
				}
			}
		case effCompletePermissionDecision:
			if e.Reply != nil {
				select {
				case e.Reply <- e.Decision:
				default:
				}
			}
		case effSignalAck:
			if e.Ack != nil {
				select {
				case e.Ack <- struct{}{}:
				default:
				}
			}
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

	if shouldMutateTTY(r.agent) {
		termutil.ResetTTYModes()
	}

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
