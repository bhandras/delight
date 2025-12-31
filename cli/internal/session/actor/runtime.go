package actor

import (
	"context"
	"errors"
	"fmt"
	"sync"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/websocket"
)

// Runtime interprets SessionActor effects for the Delight CLI.
//
// Phase 3 scope:
// - runner lifecycle effects (start/stop local and remote runners)
// - remote send/abort effects
//
// Permission request/decision plumbing and agentState persistence are handled
// in later phases and are intentionally not implemented here yet.
//
// IMPORTANT: Runtime must never mutate SessionActor state directly. It only
// emits events back into the actor mailbox via the provided emit function.
type Runtime struct {
	mu sync.Mutex

	sessionID string
	workDir string
	debug   bool

	stateUpdater StateUpdater

	localProc    *claude.Process
	localGen     int64
	remoteBridge *claude.RemoteBridge
	remoteGen    int64
}

// StateUpdater persists session agent state to a server.
type StateUpdater interface {
	UpdateState(sessionID string, agentState string, version int64) (int64, error)
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
	r.stateUpdater = updater
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
		case effRemoteSend:
			r.remoteSend(e)
		case effRemoteAbort:
			r.remoteAbort(e)
		case effPersistAgentState:
			r.persistAgentState(ctx, e, emit)
		default:
			// Unknown effect: ignore.
		}
	}
}

// Stop implements actor.Runtime.
func (r *Runtime) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.remoteBridge != nil {
		r.remoteBridge.Kill()
		r.remoteBridge = nil
	}
	if r.localProc != nil {
		r.localProc.Kill()
		r.localProc = nil
	}
}

func (r *Runtime) startLocal(ctx context.Context, eff effStartLocalRunner, emit func(framework.Input)) {
	workDir := eff.WorkDir
	if workDir == "" {
		workDir = r.workDir
	}
	if workDir == "" {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: fmt.Errorf("missing workDir")})
		return
	}

	proc, err := claude.NewProcess(workDir, r.debug)
	if err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}
	if err := proc.Start(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}

	r.mu.Lock()
	// Replace any existing local proc.
	if r.localProc != nil {
		r.localProc.Kill()
	}
	r.localProc = proc
	r.localGen = eff.Gen
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeLocal})

	go func(gen int64, p *claude.Process) {
		err := p.Wait()
		emit(evRunnerExited{Gen: gen, Mode: ModeLocal, Err: err})
	}(eff.Gen, proc)
}

func (r *Runtime) stopLocal(eff effStopLocalRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.localProc == nil {
		return
	}
	// Best-effort: only stop if it matches the currently tracked generation.
	if r.localGen != 0 && eff.Gen != 0 && eff.Gen != r.localGen {
		return
	}
	r.localProc.Kill()
	r.localProc = nil
	r.localGen = 0
}

func (r *Runtime) startRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	workDir := eff.WorkDir
	if workDir == "" {
		workDir = r.workDir
	}
	bridge, err := claude.NewRemoteBridge(workDir, eff.Resume, r.debug)
	if err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	// Phase 5: permission handler should route through actor promises.
	// For now we keep the default behavior (nil handler => auto-allow) so this
	// runtime is safe to integrate incrementally.

	if err := bridge.Start(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	r.mu.Lock()
	if r.remoteBridge != nil {
		r.remoteBridge.Kill()
	}
	r.remoteBridge = bridge
	r.remoteGen = eff.Gen
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeRemote})

	go func(gen int64, b *claude.RemoteBridge) {
		err := b.Wait()
		emit(evRunnerExited{Gen: gen, Mode: ModeRemote, Err: err})
	}(eff.Gen, bridge)
}

func (r *Runtime) stopRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.remoteBridge == nil {
		return
	}
	if r.remoteGen != 0 && eff.Gen != 0 && eff.Gen != r.remoteGen {
		return
	}
	r.remoteBridge.Kill()
	r.remoteBridge = nil
	r.remoteGen = 0
}

func (r *Runtime) remoteSend(eff effRemoteSend) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	_ = bridge.SendUserMessage(eff.Text, eff.Meta)
}

func (r *Runtime) remoteAbort(eff effRemoteAbort) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	_ = bridge.Abort()
}

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
