package actor

import (
	"context"
	"fmt"
	"strings"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/agentengine/codexengine"
)

// startCodexRemote starts Codex in remote (MCP) mode.
func (r *Runtime) startCodexRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	if r == nil {
		return
	}

	engine := r.ensureCodexEngine(ctx, emit)
	if engine == nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: fmt.Errorf("codex engine unavailable")})
		return
	}

	workDir := eff.WorkDir
	if strings.TrimSpace(workDir) == "" {
		workDir = r.workDir
	}

	r.mu.Lock()
	r.codexRemoteGen = eff.Gen
	r.codexRemoteActive = true
	r.mu.Unlock()

	if err := engine.Start(ctx, agentengine.EngineStartSpec{
		Agent:   agentengine.AgentCodex,
		WorkDir: workDir,
		Mode:    agentengine.ModeRemote,
	}); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}
}

// stopCodexRemote disables Codex remote mode for the matching generation.
func (r *Runtime) stopCodexRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	gen := r.codexRemoteGen
	active := r.codexRemoteActive
	engine := r.codexEngine
	r.mu.Unlock()

	if !active || engine == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	r.mu.Lock()
	r.codexRemoteActive = false
	r.mu.Unlock()
	_ = engine.Stop(context.Background(), agentengine.ModeRemote)
}

// startCodexLocal starts Codex in local TUI mode and tails its rollout JSONL.
func (r *Runtime) startCodexLocal(ctx context.Context, eff effStartLocalRunner, emit func(framework.Input)) {
	if r == nil {
		return
	}

	engine := r.ensureCodexEngine(ctx, emit)
	if engine == nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: fmt.Errorf("codex engine unavailable")})
		return
	}

	workDir := eff.WorkDir
	if strings.TrimSpace(workDir) == "" {
		workDir = r.workDir
	}

	r.mu.Lock()
	r.codexLocalGen = eff.Gen
	r.codexLocalActive = true
	r.mu.Unlock()

	if err := engine.Start(ctx, agentengine.EngineStartSpec{
		Agent:       agentengine.AgentCodex,
		WorkDir:     workDir,
		Mode:        agentengine.ModeLocal,
		ResumeToken: eff.Resume,
		RolloutPath: eff.RolloutPath,
	}); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}
}

// stopCodexLocal stops Codex local mode for the matching generation.
func (r *Runtime) stopCodexLocal(eff effStopLocalRunner) {
	r.mu.Lock()
	gen := r.codexLocalGen
	active := r.codexLocalActive
	engine := r.codexEngine
	r.mu.Unlock()

	if !active || engine == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	r.mu.Lock()
	r.codexLocalActive = false
	r.mu.Unlock()
	_ = engine.Stop(context.Background(), agentengine.ModeLocal)
}

// codexRemoteSend forwards a user message to the Codex remote engine.
func (r *Runtime) codexRemoteSend(ctx context.Context, eff effRemoteSend, emit func(framework.Input)) {
	engine := r.ensureCodexEngine(ctx, emit)
	if engine == nil {
		return
	}

	r.mu.Lock()
	gen := r.codexRemoteGen
	active := r.codexRemoteActive
	r.mu.Unlock()
	if !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	// Send synchronously so runtime effects remain ordered.
	//
	// The actor reducer loop runs independently from runtime effect execution,
	// so blocking here will not starve state transitions (including permission
	// prompts emitted via cmdPermissionAwait).
	_ = engine.SendUserMessage(ctx, agentengine.UserMessage{
		Text:    eff.Text,
		Meta:    eff.Meta,
		LocalID: eff.LocalID,
		AtMs:    time.Now().UnixMilli(),
	})
}

// ensureCodexEngine creates (if needed) and wires the Codex engine event loop.
func (r *Runtime) ensureCodexEngine(ctx context.Context, emit func(framework.Input)) *codexengine.Engine {
	r.mu.Lock()
	engine := r.codexEngine
	r.mu.Unlock()
	if engine != nil {
		return engine
	}

	engine = codexengine.New(r.workDir, r, r.debug)

	r.mu.Lock()
	// Double-check in case another goroutine already created it.
	if r.codexEngine != nil {
		existing := r.codexEngine
		r.mu.Unlock()
		_ = engine.Close(context.Background())
		return existing
	}
	r.codexEngine = engine
	r.mu.Unlock()

	go r.runCodexEngineEvents(ctx, engine, emit)
	return engine
}

// runCodexEngineEvents forwards Codex engine events into the actor mailbox.
func (r *Runtime) runCodexEngineEvents(ctx context.Context, engine *codexengine.Engine, emit func(framework.Input)) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-engine.Events():
			r.handleCodexEngineEvent(ev, emit)
		}
	}
}

// handleCodexEngineEvent maps an agentengine.Event into session actor inputs.
func (r *Runtime) handleCodexEngineEvent(ev agentengine.Event, emit func(framework.Input)) {
	if ev == nil {
		return
	}

	r.mu.Lock()
	localActive := r.codexLocalActive
	localGen := r.codexLocalGen
	remoteActive := r.codexRemoteActive
	remoteGen := r.codexRemoteGen
	encryptFn := r.encryptFn
	r.mu.Unlock()

	mode := ModeRemote
	gen := remoteGen
	if localActive {
		mode = ModeLocal
		gen = localGen
	} else if remoteActive {
		mode = ModeRemote
		gen = remoteGen
	} else {
		return
	}

	switch v := ev.(type) {
	case agentengine.EvReady:
		emit(evRunnerReady{Gen: gen, Mode: mode})
	case agentengine.EvExited:
		emit(evRunnerExited{Gen: gen, Mode: mode, Err: v.Err})
	case agentengine.EvSessionIdentified:
		emit(evEngineSessionIdentified{Gen: gen, ResumeToken: v.ResumeToken})
	case agentengine.EvRolloutPath:
		emit(evEngineRolloutPath{Gen: gen, Path: v.Path})
	case agentengine.EvOutboundRecord:
		if encryptFn == nil || len(v.Payload) == 0 {
			return
		}
		plaintext := append([]byte(nil), v.Payload...)
		ciphertext, err := encryptFn(plaintext)
		if err != nil {
			return
		}
		nowMs := v.AtMs
		if nowMs == 0 {
			nowMs = time.Now().UnixMilli()
		}
		emit(evOutboundMessageReady{
			Gen:                gen,
			LocalID:            v.LocalID,
			Ciphertext:         ciphertext,
			NowMs:              nowMs,
			UserTextNormalized: v.UserTextNormalized,
		})
	default:
		// Unknown event: ignore.
	}
}
