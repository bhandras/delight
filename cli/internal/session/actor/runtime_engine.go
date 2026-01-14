package actor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/agentengine/acpengine"
	"github.com/bhandras/delight/cli/internal/agentengine/claudeengine"
	"github.com/bhandras/delight/cli/internal/agentengine/codexengine"
	"github.com/bhandras/delight/cli/internal/agentengine/fakeengine"
	"github.com/bhandras/delight/cli/internal/termutil"
	"github.com/bhandras/delight/shared/logger"
)

const (
	// engineStopTimeout bounds how long we wait when stopping the opposite mode
	// runner during a mode switch (e.g. local->remote). This prevents leftover
	// foreground TUIs (notably Codex) from stealing Ctrl+C and tty input after a
	// switch.
	engineStopTimeout = 2 * time.Second
)

type localLineInjector interface {
	InjectLine(text string) error
}

func (r *Runtime) ensureEngine(ctx context.Context, emit func(framework.Input)) agentengine.AgentEngine {
	r.mu.Lock()
	engine := r.engine
	engineType := r.engineType
	requested := r.agent
	r.mu.Unlock()

	if engine != nil && engineType == requested {
		return engine
	}

	var next agentengine.AgentEngine
	switch requested {
	case agentengine.AgentClaude:
		next = claudeengine.New(r.workDir, r, r.debug)
	case agentengine.AgentCodex:
		next = codexengine.New(r.workDir, r, r.debug)
	case agentengine.AgentACP:
		next = acpengine.New(r, r, r.debug)
	case agentengine.AgentFake:
		next = fakeengine.New()
	default:
		return nil
	}

	r.mu.Lock()
	if r.engine != nil {
		old := r.engine
		r.engine = nil
		r.engineType = ""
		r.mu.Unlock()
		_ = old.Close(context.Background())
		r.mu.Lock()
	}
	// Another goroutine may have created the engine while we were closing old.
	if r.engine != nil {
		existing := r.engine
		r.mu.Unlock()
		_ = next.Close(context.Background())
		return existing
	}
	r.engine = next
	r.engineType = requested
	r.mu.Unlock()

	go r.runEngineEvents(ctx, next, emit)
	return next
}

func (r *Runtime) runEngineEvents(ctx context.Context, engine agentengine.AgentEngine, emit func(framework.Input)) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-engine.Events():
			if !ok || ev == nil {
				return
			}
			r.handleEngineEvent(ev, emit)
		}
	}
}

func (r *Runtime) handleEngineEvent(ev agentengine.Event, emit func(framework.Input)) {
	switch v := ev.(type) {
	case agentengine.EvWorking:
		gen, mode := r.engineGenForMode(v.Mode)
		nowMs := v.AtMs
		if nowMs == 0 {
			nowMs = time.Now().UnixMilli()
		}
		emit(evEngineWorking{Gen: gen, Mode: mode, Working: v.Working, NowMs: nowMs})
	case agentengine.EvUIEvent:
		gen, mode := r.engineGenForMode(v.Mode)
		nowMs := v.AtMs
		if nowMs == 0 {
			nowMs = time.Now().UnixMilli()
		}
		if mode == ModeRemote {
			r.printRemoteUIEventIfApplicable(v)
		}
		emit(evEngineUIEvent{
			Gen:           gen,
			Mode:          mode,
			EventID:       v.EventID,
			Kind:          string(v.Kind),
			Phase:         string(v.Phase),
			Status:        string(v.Status),
			BriefMarkdown: v.BriefMarkdown,
			FullMarkdown:  v.FullMarkdown,
			NowMs:         nowMs,
		})
	case agentengine.EvReady:
		gen, mode := r.engineGenForMode(v.Mode)
		if mode == ModeLocal {
			r.mu.Lock()
			r.engineLocalInteractive = true
			r.mu.Unlock()
		}
		emit(evRunnerReady{Gen: gen, Mode: mode})
	case agentengine.EvExited:
		gen, mode := r.engineGenForMode(v.Mode)
		if mode == ModeLocal {
			r.mu.Lock()
			r.engineLocalInteractive = false
			r.mu.Unlock()
		}
		emit(evRunnerExited{Gen: gen, Mode: mode, Err: v.Err})
	case agentengine.EvSessionIdentified:
		gen, mode := r.engineGenForMode(v.Mode)
		emit(evEngineSessionIdentified{Gen: gen, Mode: mode, ResumeToken: v.ResumeToken})
	case agentengine.EvRolloutPath:
		gen, _ := r.engineGenForMode(v.Mode)
		emit(evEngineRolloutPath{Gen: gen, Path: v.Path})
	case agentengine.EvOutboundRecord:
		gen, mode := r.engineGenForMode(v.Mode)

		r.mu.Lock()
		encryptFn := r.encryptFn
		localActive := r.engineLocalInteractive
		r.mu.Unlock()

		if mode == ModeRemote && !localActive {
			r.printRemoteRecordIfApplicable(v.Payload)
		}
		if len(v.Payload) == 0 {
			return
		}
		if encryptFn == nil {
			logger.Errorf("runtime: encryptFn not configured; dropping outbound record (mode=%s bytes=%d)", mode, len(v.Payload))
			return
		}

		ciphertext, err := encryptFn(append([]byte(nil), v.Payload...))
		if err != nil {
			logger.Errorf("runtime: failed to encrypt outbound record (mode=%s bytes=%d): %v", mode, len(v.Payload), err)
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
		return
	}
}

func (r *Runtime) engineGenForMode(mode agentengine.Mode) (gen int64, sessionMode Mode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch mode {
	case agentengine.ModeLocal:
		return r.engineLocalGen, ModeLocal
	case agentengine.ModeRemote:
		return r.engineRemoteGen, ModeRemote
	default:
		return r.engineRemoteGen, ModeRemote
	}
}

func (r *Runtime) startEngineLocal(ctx context.Context, eff effStartLocalRunner, emit func(framework.Input)) {
	engine := r.ensureEngine(ctx, emit)
	if engine == nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: fmt.Errorf("engine unavailable")})
		return
	}

	workDir := strings.TrimSpace(eff.WorkDir)
	if workDir == "" {
		workDir = r.workDir
	}

	// Clear any prior remote-mode transcript before handing control to a local
	// full-screen TUI.
	if shouldMutateTTY(r.agent) {
		termutil.ResetTTYModes()
	}
	r.clearScreenIfApplicable()

	r.mu.Lock()
	r.engineLocalGen = eff.Gen
	r.engineLocalActive = true
	r.engineLocalInteractive = false
	r.mu.Unlock()

	// Best-effort: ensure remote is stopped before starting local.
	stopCtx, cancel := context.WithTimeout(context.Background(), engineStopTimeout)
	if err := engine.Stop(stopCtx, agentengine.ModeRemote); err != nil && r.debug {
		logger.Debugf("runtime: failed to stop remote runner: %v", err)
	}
	cancel()
	if shouldMutateTTY(r.agent) {
		termutil.EnsureTTYForegroundSelf()
	}

	if err := engine.Start(ctx, agentengine.EngineStartSpec{
		Agent:       r.agent,
		WorkDir:     workDir,
		Mode:        agentengine.ModeLocal,
		ResumeToken: strings.TrimSpace(eff.Resume),
		RolloutPath: strings.TrimSpace(eff.RolloutPath),
		Config:      eff.Config,
	}); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}
}

func (r *Runtime) stopEngineLocal(eff effStopLocalRunner) {
	r.mu.Lock()
	engine := r.engine
	gen := r.engineLocalGen
	active := r.engineLocalActive
	r.mu.Unlock()

	if !active || engine == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	r.mu.Lock()
	r.engineLocalActive = false
	r.engineLocalInteractive = false
	r.mu.Unlock()
	_ = engine.Stop(context.Background(), agentengine.ModeLocal)
}

func (r *Runtime) startEngineRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	engine := r.ensureEngine(ctx, emit)
	if engine == nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: fmt.Errorf("engine unavailable")})
		return
	}

	workDir := strings.TrimSpace(eff.WorkDir)
	if workDir == "" {
		workDir = r.workDir
	}

	r.mu.Lock()
	r.engineRemoteGen = eff.Gen
	r.engineRemoteActive = true
	r.mu.Unlock()

	// Best-effort: ensure local is stopped before starting remote.
	stopCtx, cancel := context.WithTimeout(context.Background(), engineStopTimeout)
	if err := engine.Stop(stopCtx, agentengine.ModeLocal); err != nil && r.debug {
		logger.Debugf("runtime: failed to stop local runner: %v", err)
	}
	cancel()

	// TUIs can leave input-related terminal modes enabled (kitty keyboard
	// protocol, bracketed paste, mouse reporting). Reset them before we rely on
	// Ctrl+C and raw key scanning in remote mode.
	if shouldMutateTTY(r.agent) {
		termutil.ResetTTYModes()
	}
	// Ensure Delight receives tty input (Ctrl+C / takeback) after switching away
	// from a local TUI that may have owned the foreground process group.
	if r.debug {
		logger.Debugf("runtime: ensure tty foreground (remote start)")
	}
	if shouldMutateTTY(r.agent) {
		termutil.EnsureTTYForegroundSelf()
	}

	if err := engine.Start(ctx, agentengine.EngineStartSpec{
		Agent:       r.agent,
		WorkDir:     workDir,
		Mode:        agentengine.ModeRemote,
		ResumeToken: strings.TrimSpace(eff.Resume),
		Config:      eff.Config,
	}); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	// Clear any remnants of a previously-running local full-screen TUI before
	// printing the remote-mode transcript UI.
	r.clearScreenIfApplicable()
	r.printRemoteBannerIfApplicable()
}

func (r *Runtime) stopEngineRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	engine := r.engine
	gen := r.engineRemoteGen
	active := r.engineRemoteActive
	r.mu.Unlock()

	if !active || engine == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	r.mu.Lock()
	r.engineRemoteActive = false
	r.mu.Unlock()
	_ = engine.Stop(context.Background(), agentengine.ModeRemote)
	if !eff.Silent {
		r.printLocalBannerIfApplicable()
	}
}

func (r *Runtime) engineLocalSendLine(eff effLocalSendLine) {
	r.mu.Lock()
	engine := r.engine
	gen := r.engineLocalGen
	r.mu.Unlock()

	if engine == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	injector, ok := engine.(localLineInjector)
	if !ok {
		return
	}
	_ = injector.InjectLine(eff.Text)
}

func (r *Runtime) engineRemoteSend(ctx context.Context, eff effRemoteSend) {
	r.mu.Lock()
	engine := r.engine
	gen := r.engineRemoteGen
	active := r.engineRemoteActive
	r.mu.Unlock()
	if engine == nil {
		return
	}
	if !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	r.printRemoteUserInputIfApplicable(eff.Text)

	// Always send asynchronously so we never deadlock the runtime loop while an
	// engine is blocked on tool approvals (AwaitPermission emits effects).
	go func() {
		r.engineSendMu.Lock()
		defer r.engineSendMu.Unlock()

		err := engine.SendUserMessage(ctx, agentengine.UserMessage{
			Text:    eff.Text,
			Meta:    eff.Meta,
			LocalID: eff.LocalID,
			AtMs:    time.Now().UnixMilli(),
		})
		if err == nil {
			return
		}
		// Aborts are best-effort and should not destabilize the runtime by
		// forcing a runner exit.
		if errors.Is(err, context.Canceled) {
			return
		}

		logger.Errorf("runtime: remote send failed: %v", err)

		// Fail loud: if remote sends consistently fail, the user experiences a
		// "stuck" remote mode. Emit a runner exit so the FSM can recover (fall
		// back to local or allow a clean restart).
		r.mu.Lock()
		emitFn := r.emitFn
		currentGen := r.engineRemoteGen
		remoteActive := r.engineRemoteActive
		r.mu.Unlock()
		if emitFn == nil || !remoteActive || currentGen != gen {
			return
		}
		emitFn(evRunnerExited{Gen: gen, Mode: ModeRemote, Err: err})
	}()
}

func (r *Runtime) engineRemoteAbort(ctx context.Context, eff effRemoteAbort) {
	_ = eff
	r.mu.Lock()
	engine := r.engine
	r.mu.Unlock()
	if engine == nil {
		return
	}
	_ = engine.Abort(ctx)
}

// applyEngineConfig best-effort applies durable agent configuration to a
// running engine.
//
// The reducer emits this only for remote-mode sessions, but the runtime still
// validates the current generation to avoid applying stale updates after a
// restart.
func (r *Runtime) applyEngineConfig(ctx context.Context, eff effApplyEngineConfig) {
	r.mu.Lock()
	engine := r.engine
	gen := r.engineRemoteGen
	active := r.engineRemoteActive
	r.mu.Unlock()

	if engine == nil || !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	// Apply synchronously under the same mutex used for remote sends so the
	// config takes effect before the next outgoing message.
	r.engineSendMu.Lock()
	defer r.engineSendMu.Unlock()
	if err := engine.ApplyConfig(ctx, eff.Config); err != nil && r.debug {
		logger.Debugf("runtime: apply config failed: %v", err)
	}
}

func (r *Runtime) queryAgentEngineSettings(ctx context.Context, eff effQueryAgentEngineSettings, emit func(framework.Input)) {
	if eff.Reply == nil {
		return
	}

	snapshot := AgentEngineSettingsSnapshot{
		AgentType:       eff.AgentType,
		DesiredConfig:   eff.Desired,
		EffectiveConfig: eff.Desired,
	}

	r.mu.Lock()
	existingEngine := r.engine
	engineActive := r.engineLocalActive || r.engineRemoteActive
	r.mu.Unlock()

	// Only Codex/Claude are currently implemented as agentengine.AgentEngine
	// instances. For other agent types (ACP/fake), return stable defaults.
	engine := existingEngine
	if engine == nil {
		engine = r.ensureEngine(ctx, emit)
	}
	if engine == nil {
		snapshot.Capabilities = agentengine.AgentCapabilities{
			PermissionModes: []string{"default", "read-only", "safe-yolo", "yolo"},
		}
	} else {
		snapshot.Capabilities = engine.Capabilities()
		// If the engine is already running (or at least already instantiated by
		// the runtime), treat it as the source of truth for the effective config.
		// Otherwise, report the durable desired config as the effective snapshot
		// to avoid accidental side effects (e.g. mutating a Codex session) during
		// a pure "read" RPC.
		if engineActive {
			snapshot.EffectiveConfig = engine.CurrentConfig()
		}
	}

	select {
	case eff.Reply <- snapshot:
	default:
	}
}
