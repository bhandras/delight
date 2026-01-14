package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/codex/appserver"
	"github.com/bhandras/delight/cli/internal/codex/rollout"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// codexBinary is the executable name used to start Codex.
	codexBinary = "codex"
	// codexResumeSubcommand is the Codex CLI subcommand to resume a session.
	codexResumeSubcommand = "resume"
)

const (
	// defaultRemoteModel is the Codex model we select when no explicit model is
	// configured. This keeps behavior stable across Codex config changes.
	defaultRemoteModel = "gpt-5.2-codex"
)

const (
	// defaultRemoteReasoningEffort is the Codex reasoning effort we select when
	// no explicit effort is configured. This keeps behavior stable across Codex
	// config changes.
	defaultRemoteReasoningEffort = "medium"
)

// DefaultModel returns the Codex model we select when no explicit model is
// configured.
func DefaultModel() string {
	return defaultRemoteModel
}

// DefaultReasoningEffort returns the Codex reasoning effort we select when no
// explicit effort is configured.
func DefaultReasoningEffort() string {
	return defaultRemoteReasoningEffort
}

const (
	// codexMiniModel is the Codex model identifier that supports only a subset
	// of reasoning effort presets.
	codexMiniModel = "gpt-5.1-codex-mini"
)

const (
	// codexApprovalOnRequest requires approval before executing tools.
	codexApprovalOnRequest = "on-request"
	// codexApprovalNever disables approval prompts.
	codexApprovalNever = "never"
)

const (
	// rolloutDiscoveryTimeout bounds how long we wait for Codex to create a rollout file.
	rolloutDiscoveryTimeout = 8 * time.Second
	// rolloutDiscoveryPollInterval bounds how often we poll for a new rollout file.
	rolloutDiscoveryPollInterval = 200 * time.Millisecond
)

const (
	// permissionModeDefault mirrors Codex's default permission behavior.
	permissionModeDefault = "default"
)

const (
	// remoteShutdownTimeout bounds how long we wait for an in-flight remote
	// Codex exec process to exit when leaving remote mode.
	remoteShutdownTimeout = 2 * time.Second

	// localShutdownTimeout bounds how long we wait for the Codex local TUI to exit
	// after requesting a stop when switching away from local mode.
	localShutdownTimeout = 2 * time.Second
)

// remoteTurnStartedAckTimeout bounds how long we wait for the app-server to emit
// turn/started after successfully issuing a turn/start request.
//
// Delight treats turn/started and turn/completed as the busy/working boundaries.
var remoteTurnStartedAckTimeout = 10 * time.Second

const (
	// rolloutRootRelative is the default relative directory containing Codex rollout JSONLs.
	rolloutRootRelative = ".codex/sessions"
)

var acquireTTYForegroundFn = acquireTTYForeground
var buildLocalCodexCommandFn = buildLocalCodexCommand

const (
	// engineEventBufferSize bounds the number of engine events we can queue
	// before dropping. Local mode can emit bursts of events (tool calls + outputs)
	// while the rollout file is being tailed.
	engineEventBufferSize = 512

	// criticalEmitTimeout bounds how long we wait to enqueue critical events
	// (assistant/user transcript + session identification) before dropping.
	criticalEmitTimeout = 250 * time.Millisecond

	// localToolArgsMaxChars bounds the size of tool arguments we include in UI
	// events to avoid oversize ephemeral payloads.
	localToolArgsMaxChars = 8_000

	// localToolOutputMaxChars bounds the size of tool output we include in UI
	// events to avoid oversize ephemeral payloads.
	localToolOutputMaxChars = 24_000
)

var (
	// localStartupExitProbeDelay bounds how long we wait after starting Codex local
	// mode before assuming it successfully launched.
	//
	// This needs to be long enough to catch immediate exits (e.g. bad resume
	// token) but short enough to not noticeably delay startup.
	//
	// It is a var (not a const) so tests can increase it to avoid flakes on slow
	// CI machines.
	localStartupExitProbeDelay = 150 * time.Millisecond
)

// Engine adapts Codex (local TUI + rollout JSONL, remote `codex exec --json`)
// to the AgentEngine interface.
type Engine struct {
	mu sync.Mutex

	workDir string
	debug   bool

	requester agentengine.PermissionRequester

	events chan agentengine.Event

	remoteSessionActive   bool
	remoteEnabled         bool
	remotePermissionMode  string
	remoteModel           string
	remoteReasoningEffort string
	remoteResumeToken     string
	remoteWorking         bool
	remoteThreadID        string
	remoteActiveTurnID    string
	remoteTurnID          string
	remoteTurnStartedCh   chan struct{}

	remoteAppServer *appserver.Client

	remoteAgentMessageItems map[string]*remoteTextAccumulator
	remoteInFlightItems     map[string]struct{}

	localCmd               *exec.Cmd
	localCancel            context.CancelFunc
	localTailer            *rollout.Tailer
	localRestoreForeground func()
	localWaitCh            chan error
	localToolCalls         map[string]localToolCall

	waitOnce sync.Once
	waitErr  error
	waitCh   chan struct{}
}

type localToolCall struct {
	name      string
	arguments string
	atMs      int64
}

const (
	// remoteAgentMessageMaxBytes bounds the amount of streamed agentMessage text
	// we buffer per in-flight item. The remote app-server can stream very large
	// responses; capping this keeps Delight stable even if upstream output is
	// unexpectedly huge.
	remoteAgentMessageMaxBytes = 4 * 1024 * 1024
)

// remoteTextAccumulator buffers streamed text incrementally and supports
// truncation once a size limit is exceeded.
type remoteTextAccumulator struct {
	builder   strings.Builder
	truncated bool
	dropped   int
}

// Append appends delta to the accumulator, truncating once the maximum buffer
// size is reached.
func (a *remoteTextAccumulator) Append(delta string) {
	if a == nil || delta == "" {
		return
	}
	if a.truncated {
		a.dropped += len(delta)
		return
	}

	remaining := remoteAgentMessageMaxBytes - a.builder.Len()
	if remaining <= 0 {
		a.truncated = true
		a.dropped += len(delta)
		return
	}
	if len(delta) > remaining {
		a.builder.WriteString(delta[:remaining])
		a.truncated = true
		a.dropped += len(delta) - remaining
		return
	}
	a.builder.WriteString(delta)
}

// String returns the accumulated text, including a truncation notice if needed.
func (a *remoteTextAccumulator) String() string {
	if a == nil {
		return ""
	}
	s := a.builder.String()
	if !a.truncated {
		return s
	}
	if a.dropped <= 0 {
		return s + "\n… (truncated)"
	}
	return fmt.Sprintf("%s\n… (truncated; %d chars omitted)", s, a.dropped)
}

// New returns a Codex engine.
func New(workDir string, requester agentengine.PermissionRequester, debug bool) *Engine {
	return &Engine{
		workDir:   workDir,
		debug:     debug,
		requester: requester,
		events:    make(chan agentengine.Event, engineEventBufferSize),
		remoteTurnStartedCh: make(chan struct{}),
		waitCh:    make(chan struct{}),
	}
}

// Events implements agentengine.AgentEngine.
func (e *Engine) Events() <-chan agentengine.Event {
	return e.events
}

// Start implements agentengine.AgentEngine.
func (e *Engine) Start(ctx context.Context, spec agentengine.EngineStartSpec) error {
	if e == nil {
		return fmt.Errorf("codex engine is nil")
	}

	switch spec.Mode {
	case agentengine.ModeRemote:
		return e.startRemote(ctx, spec)
	case agentengine.ModeLocal:
		return e.startLocal(ctx, spec)
	default:
		return fmt.Errorf("unsupported mode: %q", spec.Mode)
	}
}

// Stop implements agentengine.AgentEngine.
func (e *Engine) Stop(ctx context.Context, mode agentengine.Mode) error {
	if e == nil {
		return nil
	}

	switch mode {
	case agentengine.ModeLocal:
		return e.stopLocalAndWait(ctx)
	case agentengine.ModeRemote:
		return e.stopRemoteAndWait(ctx)
	default:
		return fmt.Errorf("unsupported stop mode: %q", mode)
	}
}

// Close implements agentengine.AgentEngine.
func (e *Engine) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}

	localErr := e.stopLocalAndWait(ctx)
	remoteErr := e.stopRemoteAndWait(ctx)
	if localErr != nil {
		return localErr
	}
	if remoteErr != nil {
		return remoteErr
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// SendUserMessage implements agentengine.AgentEngine.
func (e *Engine) SendUserMessage(ctx context.Context, msg agentengine.UserMessage) error {
	if ctx == nil {
		ctx = context.Background()
	}

	e.mu.Lock()
	enabled := e.remoteEnabled
	busy := e.remoteBusyLocked()
	e.mu.Unlock()
	if !enabled {
		return fmt.Errorf("codex remote mode not active")
	}
	if busy {
		e.emitRemoteAssistantError("Codex is still working on the previous request. Press Stop to abort, then retry.")
		return nil
	}
	err := e.startRemoteTurnViaAppServer(ctx, msg)
	if err == nil {
		return nil
	}
	// Make failures visible to the user even if the runtime decides to fall back
	// to local mode to recover.
	if !errors.Is(err, context.Canceled) {
		e.emitRemoteAssistantError(fmt.Sprintf("Remote Codex request failed: %v", err))
	}
	return err
}

// Abort implements agentengine.AgentEngine.
func (e *Engine) Abort(ctx context.Context) error {
	if e == nil {
		return nil
	}
	return e.interruptRemoteTurn(ctx)
}

// Wait implements agentengine.AgentEngine.
func (e *Engine) Wait() error {
	e.waitOnce.Do(func() {
		defer close(e.waitCh)

		e.mu.Lock()
		localWaitCh := e.localWaitCh
		e.mu.Unlock()

		if localWaitCh != nil {
			e.waitErr = <-localWaitCh
			return
		}
		e.waitErr = nil
	})
	<-e.waitCh
	return e.waitErr
}

// startRemote starts (or reuses) the Codex MCP client.
func (e *Engine) startRemote(ctx context.Context, spec agentengine.EngineStartSpec) error {
	return e.startRemoteAppServer(ctx, spec)
}

// Capabilities implements agentengine.AgentEngine.
func (e *Engine) Capabilities() agentengine.AgentCapabilities {
	e.mu.Lock()
	model := strings.TrimSpace(e.remoteModel)
	e.mu.Unlock()
	reasoningEfforts := ReasoningEffortsForModel(model)

	return agentengine.AgentCapabilities{
		Models: []string{
			"gpt-5.2-codex",
			"gpt-5.1-codex-max",
			"gpt-5.1-codex-mini",
			"gpt-5.2",
		},
		PermissionModes: []string{
			"default",
			"read-only",
			"safe-yolo",
			"yolo",
		},
		ReasoningEfforts: reasoningEfforts,
	}
}

// CurrentConfig implements agentengine.AgentEngine.
func (e *Engine) CurrentConfig() agentengine.AgentConfig {
	if e == nil {
		return agentengine.AgentConfig{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return agentengine.AgentConfig{
		Model:           e.remoteModel,
		PermissionMode:  e.remotePermissionMode,
		ReasoningEffort: e.remoteReasoningEffort,
	}
}

// ApplyConfig implements agentengine.AgentEngine.
func (e *Engine) ApplyConfig(ctx context.Context, cfg agentengine.AgentConfig) error {
	_ = ctx

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		// Treat empty model as "engine default" so applying the current session
		// config does not spuriously clear resume state after switching modes.
		model = defaultRemoteModel
	}
	permissionMode := normalizePermissionMode(cfg.PermissionMode)
	reasoningEffort := strings.TrimSpace(cfg.ReasoningEffort)
	if reasoningEffort == "" {
		// Treat empty effort as "engine default" so config application is stable.
		reasoningEffort = defaultRemoteReasoningEffort
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.remoteModel == model && e.remotePermissionMode == permissionMode && e.remoteReasoningEffort == reasoningEffort {
		return nil
	}

	// Best-effort: Codex applies model/effort/sandbox policy at turn start.
	// For app-server remote mode, the resume token is the thread id and must
	// remain stable across configuration updates.
	e.remoteModel = model
	e.remotePermissionMode = permissionMode
	e.remoteReasoningEffort = reasoningEffort
	return nil
}

// ReasoningEffortsForModel returns the supported reasoning effort presets for a
// given Codex model identifier.
func ReasoningEffortsForModel(model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultRemoteModel
	}

	switch model {
	case codexMiniModel:
		// Codex mini only supports medium/high.
		return []string{"medium", "high"}
	default:
		return []string{"low", "medium", "high", "xhigh"}
	}
}

// normalizePermissionMode returns a stable permission mode value for Codex.
func normalizePermissionMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return permissionModeDefault
	}
	switch mode {
	case permissionModeDefault, "read-only", "safe-yolo", "yolo":
		return mode
	default:
		return permissionModeDefault
	}
}

// startLocal starts a Codex local TUI process and a rollout tailer.
func (e *Engine) startLocal(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx
	resumeToken := strings.TrimSpace(spec.ResumeToken)

	rolloutPath := spec.RolloutPath
	rolloutPath = strings.TrimSpace(rolloutPath)
	if rolloutPath != "" {
		if _, err := os.Stat(rolloutPath); err != nil {
			rolloutPath = ""
		}
	}

	localCtx, cancel := context.WithCancel(context.Background())

	var cmd *exec.Cmd
	if resumeToken != "" {
		cmd = buildLocalCodexCommandFn(resumeToken, spec.Config)
	} else {
		cmd = buildLocalCodexCommandFn("", spec.Config)
	}
	workDir := e.workDir
	if spec.WorkDir != "" {
		workDir = spec.WorkDir
	}
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Ensure we can tear down the full Codex local process tree on stop. Some
	// Codex builds may spawn child processes; killing only the parent can leave
	// the child running in the background after a mode switch.
	//
	// On Unix-like systems we start Codex in its own process group and kill the
	// process group on stop. On Windows, we fall back to killing the parent.
	if runtime.GOOS != "windows" {
		configureLocalCmdProcessGroup(cmd)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	restoreForeground := acquireTTYForegroundFn(cmd)
	foregroundOwnedByEngine := false
	defer func() {
		if !foregroundOwnedByEngine && restoreForeground != nil {
			restoreForeground()
		}
	}()

	// Use a single wait goroutine so:
	// - we can detect immediate startup failures (no TUI appears)
	// - stopLocalCmd never calls cmd.Wait (avoids double-wait panics)
	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
	}()

	startAtEnd := true
	if resumeToken == "" {
		// For a fresh session, read from the beginning to capture session_meta
		// and provide the phone a transcript of what happened while local.
		startAtEnd = false
		rolloutPath = ""
	}
	if strings.TrimSpace(rolloutPath) == "" {
		var (
			discovered string
			err        error
		)
		if resumeToken != "" {
			discovered, err = waitForRolloutPathForSessionID(localCtx, resumeToken)
		} else {
			discovered, err = waitForRolloutPath(localCtx)
		}
		if err != nil {
			_ = cmd.Process.Kill()
			cancel()
			return err
		}
		rolloutPath = discovered
	}

	e.tryEmit(agentengine.EvRolloutPath{Mode: agentengine.ModeLocal, Path: rolloutPath})

	tailer := rollout.NewTailer(rolloutPath, rollout.TailerOptions{StartAtEnd: startAtEnd})
	if err := tailer.Start(localCtx); err != nil {
		_ = cmd.Process.Kill()
		cancel()
		return err
	}

	// If Codex exits immediately (e.g. invalid resume token), fail the mode switch
	// instead of emitting EvReady and "switching" into a dead TUI.
	select {
	case err := <-waitErrCh:
		cancel()
		return fmt.Errorf("codex local exited early: %w", err)
	case <-time.After(localStartupExitProbeDelay):
	}

	e.mu.Lock()
	old := e.detachLocalLocked()
	e.localCmd = cmd
	e.localCancel = cancel
	e.localTailer = tailer
	e.localRestoreForeground = restoreForeground
	e.localWaitCh = waitErrCh
	e.localToolCalls = make(map[string]localToolCall)
	e.mu.Unlock()
	foregroundOwnedByEngine = true
	old.stop(context.Background())

	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeLocal})

	go e.forwardRollout(localCtx, tailer)
	go func() {
		err := <-waitErrCh
		cancel()
		if restoreForeground != nil {
			restoreForeground()
		}
		e.tryEmit(agentengine.EvExited{Mode: agentengine.ModeLocal, Err: err})
	}()

	return nil
}

// buildLocalCodexCommand builds the `codex` interactive command line for local
// mode, including user-selected model/effort/permission settings.
func buildLocalCodexCommand(resumeToken string, cfg agentengine.AgentConfig) *exec.Cmd {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultRemoteModel
	}

	reasoningEffort := strings.TrimSpace(cfg.ReasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = defaultRemoteReasoningEffort
	}
	if !containsString(ReasoningEffortsForModel(model), reasoningEffort) {
		reasoningEffort = defaultRemoteReasoningEffort
	}

	permissionMode := normalizePermissionMode(cfg.PermissionMode)
	sandbox := sandboxPolicy(permissionMode)
	approval := approvalPolicy(permissionMode)

	args := []string{
		"-a", approval,
		"-s", sandbox,
		"-m", model,
		"-c", fmt.Sprintf("model_reasoning_effort=%q", reasoningEffort),
	}
	if token := strings.TrimSpace(resumeToken); token != "" {
		args = append(args, codexResumeSubcommand, token)
	}
	return exec.Command(codexBinary, args...)
}

// approvalPolicy maps Delight's permissionMode value to a Codex approval policy.
func approvalPolicy(permissionMode string) string {
	switch permissionMode {
	case "safe-yolo", "yolo":
		return codexApprovalNever
	default:
		return codexApprovalOnRequest
	}
}

// containsString reports whether items contains needle.
func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

type localStopHandle struct {
	cancel            context.CancelFunc
	cmd               *exec.Cmd
	tailer            *rollout.Tailer
	restoreForeground func()
	waitCh            chan error
}

func (h localStopHandle) stop(ctx context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}
	// Tailer exits on ctx cancellation.
	if h.restoreForeground != nil {
		h.restoreForeground()
	}
	if h.cmd != nil && h.cmd.Process != nil {
		stopLocalCmd(h.cmd)
	}
	if h.waitCh == nil {
		return nil
	}

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > localShutdownTimeout {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(waitCtx, localShutdownTimeout)
		defer cancel()
	}

	select {
	case <-waitCtx.Done():
		return waitCtx.Err()
	case err := <-h.waitCh:
		return err
	}
}

// stopLocalAndWait stops the local Codex TUI and bounds how long we wait for exit.
func (e *Engine) stopLocalAndWait(ctx context.Context) error {
	e.mu.Lock()
	handle := e.detachLocalLocked()
	e.mu.Unlock()
	return handle.stop(ctx)
}

// detachLocalLocked clears local state and returns a handle to stop the process
// without holding Engine.mu.
func (e *Engine) detachLocalLocked() localStopHandle {
	handle := localStopHandle{
		cancel:            e.localCancel,
		cmd:               e.localCmd,
		tailer:            e.localTailer,
		restoreForeground: e.localRestoreForeground,
		waitCh:            e.localWaitCh,
	}

	e.localCancel = nil
	e.localCmd = nil
	e.localTailer = nil
	e.localRestoreForeground = nil
	e.localWaitCh = nil
	e.localToolCalls = nil

	return handle
}

func (e *Engine) stopRemoteAndWait(ctx context.Context) error {
	e.mu.Lock()
	wasWorking := e.remoteWorking
	e.remoteEnabled = false
	e.remoteSessionActive = false
	e.remoteResumeToken = ""
	e.remoteWorking = false
	e.remoteThreadID = ""
	e.remoteActiveTurnID = ""
	e.remoteTurnID = ""
	if e.remoteTurnStartedCh != nil {
		close(e.remoteTurnStartedCh)
	}
	e.remoteTurnStartedCh = make(chan struct{})
	app := e.remoteAppServer
	e.remoteAppServer = nil
	e.remoteAgentMessageItems = nil
	e.mu.Unlock()

	if wasWorking {
		e.tryEmit(agentengine.EvWorking{
			Mode:     agentengine.ModeRemote,
			Working:  false,
			AtMs:     time.Now().UnixMilli(),
		})
	}

	if app == nil {
		return nil
	}

	_ = app.Close()

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > remoteShutdownTimeout {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(waitCtx, remoteShutdownTimeout)
		defer cancel()
	}

	done := make(chan error, 1)
	go func() { done <- app.Wait() }()

	select {
	case <-waitCtx.Done():
		return waitCtx.Err()
	case err := <-done:
		if isExpectedRemoteShutdownError(err) {
			return nil
		}
		return err
	}
}

// isExpectedRemoteShutdownError reports whether err is an expected error from
// waiting on a remotely stopped Codex app-server subprocess (for example when
// we kill it during shutdown).
func isExpectedRemoteShutdownError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	// We currently stop `codex app-server` by killing the subprocess.
	// In that case cmd.Wait() reports a signal exit that should not be treated
	// as an actionable error for higher layers.
	msg := err.Error()
	return strings.Contains(msg, "signal: killed") || strings.Contains(msg, "signal: terminated")
}

// forwardRollout maps rollout events into outbound wire records.
func (e *Engine) forwardRollout(ctx context.Context, tailer *rollout.Tailer) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-tailer.Events():
			if !ok {
				return
			}
			e.handleRolloutEvent(ev)
		}
	}
}

// handleRolloutEvent converts a rollout.Event into agentengine events.
func (e *Engine) handleRolloutEvent(ev rollout.Event) {
	switch v := ev.(type) {
	case rollout.EvSessionMeta:
		e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeLocal, ResumeToken: v.SessionID})
	case rollout.EvUserMessage:
		e.tryEmit(agentengine.EvWorking{
			Mode:     agentengine.ModeLocal,
			Working:  true,
			AtMs:     v.AtMs,
		})
		raw, err := marshalUserTextRecord(v.Text, nil)
		if err != nil {
			return
		}
		e.tryEmit(agentengine.EvOutboundRecord{
			Mode:               agentengine.ModeLocal,
			LocalID:            types.NewCUID(),
			Payload:            raw,
			UserTextNormalized: normalizeUserText(v.Text),
			AtMs:               v.AtMs,
		})
	case rollout.EvAssistantMessage:
		e.tryEmit(agentengine.EvWorking{
			Mode:     agentengine.ModeLocal,
			Working:  false,
			AtMs:     v.AtMs,
		})
		raw, err := marshalAssistantTextRecord(v.Text, "unknown")
		if err != nil {
			return
		}
		e.tryEmit(agentengine.EvOutboundRecord{
			Mode:    agentengine.ModeLocal,
			LocalID: types.NewCUID(),
			Payload: raw,
			AtMs:    v.AtMs,
		})
	case rollout.EvReasoningSummary:
		text := strings.TrimSpace(v.Text)
		if text == "" {
			return
		}
		e.tryEmit(agentengine.EvUIEvent{
			Mode:          agentengine.ModeLocal,
			EventID:       types.NewCUID(),
			Kind:          agentengine.UIEventThinking,
			Phase:         agentengine.UIEventPhaseEnd,
			Status:        agentengine.UIEventStatusOK,
			BriefMarkdown: firstMarkdownLine(text),
			FullMarkdown:  text,
			AtMs:          v.AtMs,
		})
	case rollout.EvFunctionCall:
		e.handleLocalFunctionCall(v)
	case rollout.EvFunctionCallOutput:
		e.handleLocalFunctionCallOutput(v)
	default:
		return
	}
}

func (e *Engine) handleLocalFunctionCall(ev rollout.EvFunctionCall) {
	if e == nil {
		return
	}
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" {
		callID = types.NewCUID()
	}
	name := strings.TrimSpace(ev.Name)
	if name == "" {
		name = "tool"
	}

	brief := fmt.Sprintf("Tool: `%s`", name)
	full := fmt.Sprintf("Tool: `%s`", name)

	// Try to show a concise command preview for shell_command.
	if name == "shell_command" {
		if cmd := extractShellCommand(ev.Arguments); cmd != "" {
			brief = fmt.Sprintf("Tool: `shell_command`\n\n```sh\n%s\n```", cmd)
			full = fmt.Sprintf("Tool: `shell_command`\n\n```sh\n%s\n```", cmd)
		}
	}

	if name == "update_plan" {
		brief = "Tool: `update_plan`"
		full = renderUpdatePlanMarkdownStart(ev.Arguments)
	}

	e.mu.Lock()
	if e.localToolCalls == nil {
		e.localToolCalls = make(map[string]localToolCall)
	}
	e.localToolCalls[callID] = localToolCall{name: name, arguments: ev.Arguments, atMs: ev.AtMs}
	e.mu.Unlock()

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeLocal,
		EventID:       callID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseStart,
		Status:        agentengine.UIEventStatusRunning,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          ev.AtMs,
	})
}

func (e *Engine) handleLocalFunctionCallOutput(ev rollout.EvFunctionCallOutput) {
	if e == nil {
		return
	}
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" {
		callID = types.NewCUID()
	}

	var call localToolCall
	var ok bool
	e.mu.Lock()
	if e.localToolCalls != nil {
		call, ok = e.localToolCalls[callID]
		delete(e.localToolCalls, callID)
	}
	e.mu.Unlock()

	name := strings.TrimSpace(call.name)
	if name == "" {
		name = "tool"
	}

	brief := fmt.Sprintf("Tool: `%s`", name)
	full := fmt.Sprintf("Tool: `%s`", name)

	output := strings.TrimSpace(ev.Output)
	outputSnippet := truncateText(output, localToolOutputMaxChars)
	if outputSnippet != "" {
		full = fmt.Sprintf("%s\n\nOutput:\n\n```\n%s\n```", full, outputSnippet)
	}

	if name == "shell_command" {
		if cmd := extractShellCommand(call.arguments); cmd != "" {
			brief = fmt.Sprintf("Tool: `shell_command`\n\n```sh\n%s\n```", cmd)
			full = fmt.Sprintf("Tool: `shell_command`\n\n```sh\n%s\n```", cmd)
			if outputSnippet != "" {
				full = fmt.Sprintf("%s\n\nOutput:\n\n```\n%s\n```", full, outputSnippet)
			}
		}
	}

	if name == "update_plan" {
		brief = "Tool: `update_plan`"
		full = renderUpdatePlanMarkdownEnd(call.arguments, outputSnippet)
	}

	status := agentengine.UIEventStatusOK
	if exitCode, ok := parseExitCode(output); ok && exitCode != 0 {
		status = agentengine.UIEventStatusError
	}

	atMs := ev.AtMs
	if atMs == 0 && ok {
		atMs = call.atMs
	}

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeLocal,
		EventID:       callID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseEnd,
		Status:        status,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          atMs,
	})
}

// updatePlanArguments is the structured payload expected by the `update_plan`
// tool.
type updatePlanArguments struct {
	Explanation string `json:"explanation"`
	Plan        []struct {
		Status string `json:"status"`
		Step   string `json:"step"`
	} `json:"plan"`
}

// renderUpdatePlanMarkdownStart renders a compact, mobile-friendly preview for
// a plan update tool call start event.
func renderUpdatePlanMarkdownStart(arguments string) string {
	return "Tool: `update_plan`\n\nUpdating plan…"
}

// renderUpdatePlanMarkdownEnd renders a markdown todo list for a plan update
// tool call end event.
func renderUpdatePlanMarkdownEnd(arguments string, output string) string {
	args, ok := parseUpdatePlanArguments(arguments)
	if !ok {
		argsText := truncateText(prettyJSON(arguments), localToolArgsMaxChars)
		if strings.TrimSpace(argsText) == "" {
			return "Tool: `update_plan`"
		}
		return fmt.Sprintf("Tool: `update_plan`\n\n```json\n%s\n```", argsText)
	}

	var b strings.Builder
	b.WriteString("Tool: `update_plan`\n\n")
	if strings.TrimSpace(args.Explanation) != "" {
		b.WriteString(strings.TrimSpace(args.Explanation))
		b.WriteString("\n\n")
	}

	// Prefer a task list so the phone UI renders checkboxes.
	b.WriteString("Plan:\n")
	for _, item := range args.Plan {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(item.Status))
		switch status {
		case "completed":
			fmt.Fprintf(&b, "- [x] %s\n", step)
		case "in_progress":
			fmt.Fprintf(&b, "- [ ] %s (in progress)\n", step)
		default:
			fmt.Fprintf(&b, "- [ ] %s\n", step)
		}
	}

	// If the tool emitted output, keep it as a separate block. This is not
	// expected for update_plan, but supports future debugging.
	if strings.TrimSpace(output) != "" {
		fmt.Fprintf(&b, "\nOutput:\n\n```\n%s\n```", output)
	}

	return strings.TrimSpace(b.String())
}

// parseUpdatePlanArguments decodes a JSON `update_plan` tool invocation.
func parseUpdatePlanArguments(arguments string) (updatePlanArguments, bool) {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return updatePlanArguments{}, false
	}
	var v updatePlanArguments
	if err := json.Unmarshal([]byte(arguments), &v); err != nil {
		return updatePlanArguments{}, false
	}
	return v, true
}

func prettyJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return string(b)
}

func extractShellCommand(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Command)
}

var exitCodeRegex = regexp.MustCompile(`(?m)^Exit code:\\s*(\\d+)\\s*$`)

func parseExitCode(output string) (int, bool) {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, false
	}
	matches := exitCodeRegex.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

// startRemoteTurnLocked resets per-turn state used for UI events (thinking log).
//
// Caller must hold e.mu.
func (e *Engine) startRemoteTurnLocked(turnID string) {
	if e == nil {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = types.NewCUID()
	}
	e.remoteTurnID = turnID
}

// setRemoteWorking updates the cached remote working state and emits a
// best-effort EvWorking when the value changes.
func (e *Engine) setRemoteWorking(working bool, atMs int64) {
	if e == nil {
		return
	}
	if atMs == 0 {
		atMs = time.Now().UnixMilli()
	}

	e.mu.Lock()
	debug := e.debug
	prevWorking := e.remoteWorking
	activeTurnID := e.remoteActiveTurnID
	inFlightItems := len(e.remoteInFlightItems)
	agentMessageItems := len(e.remoteAgentMessageItems)
	if e.remoteWorking == working {
		e.mu.Unlock()
		return
	}
	e.remoteWorking = working
	e.mu.Unlock()

	if debug {
		logger.Debugf(
			"codex: remote working %t -> %t (turn=%q inFlight=%d agentMsg=%d atMs=%d)",
			prevWorking,
			working,
			strings.TrimSpace(activeTurnID),
			inFlightItems,
			agentMessageItems,
			atMs,
		)
	}

	e.tryEmit(agentengine.EvWorking{
		Mode:     agentengine.ModeRemote,
		Working:  working,
		AtMs:     atMs,
	})

	e.mu.Lock()
	turnID := e.remoteTurnID
	e.mu.Unlock()
	if strings.TrimSpace(turnID) == "" {
		return
	}

	phase := agentengine.UIEventPhaseUpdate
	status := agentengine.UIEventStatusRunning
	if !working {
		phase = agentengine.UIEventPhaseEnd
		status = agentengine.UIEventStatusOK
	}
	brief := "Thinking…"
	if !working {
		brief = ""
	}

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       "thinking-" + turnID,
		Kind:          agentengine.UIEventThinking,
		Phase:         phase,
		Status:        status,
		BriefMarkdown: brief,
		FullMarkdown:  "",
		AtMs:          atMs,
	})
}

// remoteBusyLocked reports whether the engine is currently busy executing a
// remote request.
//
// Caller must hold e.mu.
func (e *Engine) remoteBusyLocked() bool {
	if e == nil {
		return false
	}
	if strings.TrimSpace(e.remoteActiveTurnID) != "" {
		return true
	}
	return false
}

// updateRemoteWorkingFromState recomputes the best-effort "working" signal
// from the engine's remote state and emits EvWorking/UI events when it changes.
func (e *Engine) updateRemoteWorkingFromState(atMs int64) {
	if e == nil {
		return
	}
	if atMs == 0 {
		atMs = time.Now().UnixMilli()
	}
	e.mu.Lock()
	busy := e.remoteBusyLocked()
	e.mu.Unlock()
	e.setRemoteWorking(busy, atMs)
}

func truncateText(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	if len(text) <= maxChars {
		return text
	}
	omitted := len(text) - maxChars
	return fmt.Sprintf("%s\n… (truncated; %d chars omitted)", text[:maxChars], omitted)
}

// firstMarkdownLine returns the first non-empty line from a Markdown string.
func firstMarkdownLine(md string) string {
	md = strings.TrimSpace(md)
	if md == "" {
		return ""
	}
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line
	}
	return ""
}

// tryEmit enqueues an engine event without blocking.
func (e *Engine) tryEmit(ev agentengine.Event) {
	if e == nil {
		return
	}

	critical := false
	switch ev.(type) {
	case agentengine.EvOutboundRecord, agentengine.EvReady, agentengine.EvExited, agentengine.EvSessionIdentified:
		critical = true
	}

	if !critical {
		select {
		case e.events <- ev:
		default:
		}
		return
	}

	timer := time.NewTimer(criticalEmitTimeout)
	defer timer.Stop()

	select {
	case e.events <- ev:
		return
	case <-timer.C:
		return
	}
}

// emitRemoteAssistantError publishes a best-effort assistant message for remote
// mode describing an error condition.
func (e *Engine) emitRemoteAssistantError(text string) {
	if e == nil {
		return
	}
	raw, err := marshalAssistantTextRecord(text, "unknown")
	if err != nil {
		return
	}
	e.tryEmit(agentengine.EvOutboundRecord{
		Mode:    agentengine.ModeRemote,
		LocalID: types.NewCUID(),
		Payload: raw,
		AtMs:    time.Now().UnixMilli(),
	})
}

// marshalAssistantTextRecord encodes a plain assistant text message as an AgentOutputRecord.
func marshalAssistantTextRecord(text string, model string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("assistant text required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}

	rec := wire.AgentOutputRecord{
		Role: "agent",
		Content: wire.AgentOutputContent{
			Type: "output",
			Data: wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             types.NewCUID(),
				ParentUUID:       nil,
				Message: wire.AgentMessage{
					Role:  "assistant",
					Model: model,
					Content: []wire.ContentBlock{
						{Type: "text", Text: text},
					},
				},
			},
		},
	}
	return json.Marshal(rec)
}

// marshalUserTextRecord encodes a user text record matching mobile plaintext payloads.
func marshalUserTextRecord(text string, meta map[string]any) ([]byte, error) {
	rec := wire.UserTextRecord{
		Role: "user",
		Meta: meta,
	}
	rec.Content.Type = "text"
	rec.Content.Text = text
	return json.Marshal(rec)
}

// resolveMode extracts permissionMode/model from meta and reports if they changed.
// sandboxPolicy maps Delight's permissionMode value to a Codex sandbox policy.
func sandboxPolicy(permissionMode string) string {
	switch permissionMode {
	case "read-only":
		return "read-only"
	case "yolo":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

// normalizeUserText normalizes user input text for echo suppression.
func normalizeUserText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// discoverLatestRolloutPathAfter returns the most recently modified rollout JSONL after since.
// waitForRolloutPath waits for Codex to create a rollout JSONL and returns its path.
func waitForRolloutPath(ctx context.Context) (string, error) {
	deadline := time.NewTimer(rolloutDiscoveryTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(rolloutDiscoveryPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("timed out waiting for codex rollout jsonl")
		case <-ticker.C:
			path, err := discoverLatestRolloutPathImpl()
			if err != nil {
				continue
			}
			if strings.TrimSpace(path) != "" {
				return path, nil
			}
		}
	}
}

// waitForRolloutPathForSessionID waits for Codex to create a rollout JSONL for
// the given session id and returns its path.
func waitForRolloutPathForSessionID(ctx context.Context, sessionID string) (string, error) {
	deadline := time.NewTimer(rolloutDiscoveryTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(rolloutDiscoveryPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("timed out waiting for codex rollout jsonl")
		case <-ticker.C:
			path, err := discoverLatestRolloutPathForSessionIDImpl(sessionID)
			if err != nil {
				continue
			}
			if strings.TrimSpace(path) != "" {
				return path, nil
			}
		}
	}
}

// discoverLatestRolloutPathImpl finds the latest rollout JSONL.
func discoverLatestRolloutPathImpl() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, rolloutRootRelative)

	var bestPath string
	var bestMod time.Time

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(bestMod) {
			bestMod = info.ModTime()
			bestPath = path
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, os.ErrNotExist) {
			return "", fmt.Errorf("codex rollout directory not found: %s", root)
		}
		return "", walkErr
	}

	if bestPath == "" {
		return "", fmt.Errorf("no codex rollout jsonl found under %s", root)
	}
	return bestPath, nil
}

// discoverLatestRolloutPathForSessionIDImpl finds the latest rollout JSONL for
// the given Codex session id.
func discoverLatestRolloutPathForSessionIDImpl(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session id is required")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, rolloutRootRelative)

	var bestPath string
	var bestMod time.Time

	wantSuffix := "-" + sessionID + ".jsonl"

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, wantSuffix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(bestMod) {
			bestMod = info.ModTime()
			bestPath = path
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, os.ErrNotExist) {
			return "", fmt.Errorf("codex rollout directory not found: %s", root)
		}
		return "", walkErr
	}

	if bestPath == "" {
		return "", fmt.Errorf("no codex rollout jsonl found under %s for session %s", root, sessionID)
	}
	return bestPath, nil
}
