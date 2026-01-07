package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/internal/codex/rollout"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// codexBinary is the executable name used to start Codex.
	codexBinary = "codex"
	// codexResumeSubcommand is the Codex CLI subcommand to resume a session.
	codexResumeSubcommand = "resume"
)

const (
	// rolloutDiscoveryTimeout bounds how long we wait for Codex to create a rollout file.
	rolloutDiscoveryTimeout = 8 * time.Second
	// rolloutDiscoveryPollInterval bounds how often we poll for a new rollout file.
	rolloutDiscoveryPollInterval = 200 * time.Millisecond
)

const (
	// localStartupExitProbeDelay bounds how long we wait after starting Codex local
	// before considering it "ready enough" to switch the CLI into local mode.
	localStartupExitProbeDelay = 150 * time.Millisecond
)

const (
	// permissionModeDefault mirrors Codex's default permission behavior.
	permissionModeDefault = "default"
)

const (
	// remoteShutdownTimeout bounds how long we wait for the Codex MCP server to exit
	// after requesting a shutdown when switching away from remote mode.
	remoteShutdownTimeout = 2 * time.Second

	// localShutdownTimeout bounds how long we wait for the Codex local TUI to exit
	// after requesting a stop when switching away from local mode.
	localShutdownTimeout = 2 * time.Second
)

const (
	// codexDecisionApproved is the Codex ReviewDecision string for approval.
	codexDecisionApproved = "approved"
	// codexDecisionDenied is the Codex ReviewDecision string for denial.
	codexDecisionDenied = "denied"
)

const (
	// configKeyPermissionMode is the meta key used by Delight to choose Codex permissions.
	configKeyPermissionMode = "permissionMode"
	// configKeyModel is the meta key used by Delight to choose the Codex model.
	configKeyModel = "model"
)

const (
	// rolloutRootRelative is the default relative directory containing Codex rollout JSONLs.
	rolloutRootRelative = ".codex/sessions"
)

// Engine adapts Codex (MCP + rollout JSONL) to the AgentEngine interface.
type Engine struct {
	mu sync.Mutex

	workDir string
	debug   bool

	requester agentengine.PermissionRequester

	events chan agentengine.Event

	remoteClient          *codex.Client
	remoteSessionActive   bool
	remoteEnabled         bool
	remotePermissionMode  string
	remoteModel           string
	remoteReasoningEffort string
	remoteThinking        bool
	remoteTurnID          string
	remoteThinkingSteps   []string
	remoteWaitStarted     bool
	remoteCancel          context.CancelFunc
	remoteCancelID        int64
	remoteCancelNextID    int64

	localCmd               *exec.Cmd
	localCancel            context.CancelFunc
	localTailer            *rollout.Tailer
	localRestoreForeground func()
	localWaitCh            chan error

	waitOnce sync.Once
	waitErr  error
	waitCh   chan struct{}
}

// New returns a Codex engine.
func New(workDir string, requester agentengine.PermissionRequester, debug bool) *Engine {
	return &Engine{
		workDir:   workDir,
		debug:     debug,
		requester: requester,
		events:    make(chan agentengine.Event, 128),
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
	client := e.remoteClient
	active := e.remoteSessionActive
	enabled := e.remoteEnabled
	permissionMode := e.remotePermissionMode
	model := e.remoteModel
	reasoningEffort := e.remoteReasoningEffort
	e.mu.Unlock()
	if !enabled {
		return fmt.Errorf("codex remote mode not active")
	}
	if client == nil {
		var err error
		client, err = e.ensureRemoteClient()
		if err != nil {
			return err
		}
	}

	turnCtx, cancel := context.WithCancel(ctx)
	cancelID := atomic.AddInt64(&e.remoteCancelNextID, 1)
	e.mu.Lock()
	e.remoteCancel = cancel
	e.remoteCancelID = cancelID
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		if e.remoteCancelID == cancelID {
			e.remoteCancel = nil
			e.remoteCancelID = 0
		}
		e.mu.Unlock()
	}()

	e.startRemoteTurn()
	e.setRemoteThinking(true, time.Now().UnixMilli())

	permissionMode = normalizePermissionMode(permissionMode)
	model = strings.TrimSpace(model)
	reasoningEffort = strings.TrimSpace(reasoningEffort)

	cfg := codex.SessionConfig{
		Prompt:         msg.Text,
		ApprovalPolicy: approvalPolicy(permissionMode),
		Sandbox:        sandboxPolicy(permissionMode),
		Cwd:            e.workDir,
		Model:          model,
	}
	if reasoningEffort != "" {
		cfg.Config = map[string]interface{}{
			"model_reasoning_effort": reasoningEffort,
		}
	}

	if !active {
		if _, err := client.StartSession(turnCtx, cfg); err != nil {
			e.setRemoteThinking(false, time.Now().UnixMilli())
			return err
		}
		e.mu.Lock()
		e.remoteSessionActive = true
		e.remotePermissionMode = permissionMode
		e.remoteModel = model
		e.mu.Unlock()
		e.emitIdentifiers()
		return nil
	}

	if _, err := client.ContinueSession(turnCtx, msg.Text); err != nil {
		if errors.Is(err, context.Canceled) {
			e.setRemoteThinking(false, time.Now().UnixMilli())
			return err
		}
		e.mu.Lock()
		e.remoteSessionActive = false
		e.mu.Unlock()
		e.setRemoteThinking(false, time.Now().UnixMilli())
		return err
	}
	e.emitIdentifiers()
	return nil
}

// Abort implements agentengine.AgentEngine.
func (e *Engine) Abort(ctx context.Context) error {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	client := e.remoteClient
	wasThinking := e.remoteThinking
	cancel := e.remoteCancel
	e.remoteCancel = nil
	e.remoteCancelID = 0
	e.remoteThinking = false
	e.remoteTurnID = ""
	e.remoteThinkingSteps = nil
	e.remoteWaitStarted = false
	e.mu.Unlock()

	// First, unblock any in-flight SendUserMessage call immediately. This is
	// independent of whether Codex honors the MCP cancellation notification.
	if cancel != nil {
		cancel()
	}

	if wasThinking {
		e.tryEmit(agentengine.EvThinking{
			Mode:     agentengine.ModeRemote,
			Thinking: false,
			AtMs:     time.Now().UnixMilli(),
		})
	}

	if client == nil {
		return nil
	}

	// Best-effort: request that Codex interrupts the currently running tool call.
	// This preserves remote session continuity (resume token) for future turns.
	_ = client.CancelInFlight(ctx, "user abort")
	return nil
}

// Wait implements agentengine.AgentEngine.
func (e *Engine) Wait() error {
	e.waitOnce.Do(func() {
		defer close(e.waitCh)

		e.mu.Lock()
		localWaitCh := e.localWaitCh
		client := e.remoteClient
		e.mu.Unlock()

		if localWaitCh != nil {
			e.waitErr = <-localWaitCh
			return
		}
		if client != nil {
			e.waitErr = client.Wait()
			return
		}
		e.waitErr = nil
	})
	<-e.waitCh
	return e.waitErr
}

// startRemote starts (or reuses) the Codex MCP client.
func (e *Engine) startRemote(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx

	e.mu.Lock()
	client := e.remoteClient
	workDir := e.workDir
	if spec.WorkDir != "" {
		workDir = spec.WorkDir
	}
	debug := e.debug
	requester := e.requester
	e.mu.Unlock()

	if client == nil {
		client = codex.NewClient(workDir, debug)
		client.SetEventHandler(func(event map[string]interface{}) {
			e.handleMCPEvent(event)
		})
		client.SetPermissionHandler(func(requestID string, toolName string, input map[string]interface{}) (*codex.PermissionDecision, error) {
			return e.handlePermission(requestID, toolName, input, requester)
		})
		if err := client.Start(); err != nil {
			return err
		}

		e.mu.Lock()
		e.remoteClient = client
		e.remotePermissionMode = normalizePermissionMode(spec.Config.PermissionMode)
		e.remoteModel = strings.TrimSpace(spec.Config.Model)
		e.remoteReasoningEffort = strings.TrimSpace(spec.Config.ReasoningEffort)
		e.remoteEnabled = true
		e.remoteWaitStarted = false
		e.mu.Unlock()
	} else {
		e.mu.Lock()
		e.remoteEnabled = true
		e.remotePermissionMode = normalizePermissionMode(spec.Config.PermissionMode)
		e.remoteModel = strings.TrimSpace(spec.Config.Model)
		e.remoteReasoningEffort = strings.TrimSpace(spec.Config.ReasoningEffort)
		e.mu.Unlock()
	}

	e.mu.Lock()
	if e.remoteClient == client && !e.remoteWaitStarted {
		e.remoteWaitStarted = true
		go func(c *codex.Client) {
			err := c.Wait()
			e.tryEmit(agentengine.EvExited{Mode: agentengine.ModeRemote, Err: err})
		}(client)
	}
	e.mu.Unlock()

	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeRemote})
	return nil
}

// ensureRemoteClient returns an initialized Codex MCP client, starting one if
// necessary.
//
// This is used to recover after a best-effort abort, which tears down the MCP
// process so the user can continue with a fresh session while remaining in
// remote mode.
func (e *Engine) ensureRemoteClient() (*codex.Client, error) {
	if e == nil {
		return nil, fmt.Errorf("codex engine is nil")
	}

	e.mu.Lock()
	client := e.remoteClient
	workDir := e.workDir
	debug := e.debug
	requester := e.requester
	e.mu.Unlock()

	if client != nil {
		return client, nil
	}

	client = codex.NewClient(workDir, debug)
	client.SetEventHandler(func(event map[string]interface{}) {
		e.handleMCPEvent(event)
	})
	client.SetPermissionHandler(func(requestID string, toolName string, input map[string]interface{}) (*codex.PermissionDecision, error) {
		return e.handlePermission(requestID, toolName, input, requester)
	})
	if err := client.Start(); err != nil {
		return nil, err
	}

	e.mu.Lock()
	if e.remoteClient != nil {
		existing := e.remoteClient
		e.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	e.remoteClient = client
	e.remoteWaitStarted = false
	e.mu.Unlock()

	e.mu.Lock()
	if e.remoteClient == client && !e.remoteWaitStarted {
		e.remoteWaitStarted = true
		go func(c *codex.Client) {
			err := c.Wait()
			e.tryEmit(agentengine.EvExited{Mode: agentengine.ModeRemote, Err: err})
		}(client)
	}
	e.mu.Unlock()

	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeRemote})
	return client, nil
}

// Capabilities implements agentengine.AgentEngine.
func (e *Engine) Capabilities() agentengine.AgentCapabilities {
	e.mu.Lock()
	model := strings.TrimSpace(e.remoteModel)
	e.mu.Unlock()

	reasoningEfforts := []string{
		"low",
		"medium",
		"high",
		"xhigh",
	}
	if model == "gpt-5.1-codex-mini" {
		// Codex mini only supports medium/high.
		reasoningEfforts = []string{
			"medium",
			"high",
		}
	}

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
	permissionMode := normalizePermissionMode(cfg.PermissionMode)
	reasoningEffort := strings.TrimSpace(cfg.ReasoningEffort)

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.remoteModel == model && e.remotePermissionMode == permissionMode && e.remoteReasoningEffort == reasoningEffort {
		return nil
	}

	// Best-effort: Codex applies model/effort/approval policy at session start.
	// Clearing the session ensures subsequent turns use the updated config.
	if e.remoteClient != nil {
		e.remoteClient.ClearSession()
	}
	e.remoteSessionActive = false
	e.remoteModel = model
	e.remotePermissionMode = permissionMode
	e.remoteReasoningEffort = reasoningEffort
	return nil
}

// normalizePermissionMode returns a stable permission mode value for Codex.
func normalizePermissionMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return permissionModeDefault
	}
	return mode
}

// startLocal starts a Codex local TUI process and a rollout tailer.
func (e *Engine) startLocal(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx
	resumeToken := strings.TrimSpace(spec.ResumeToken)

	rolloutPath := spec.RolloutPath
	if strings.TrimSpace(rolloutPath) == "" && resumeToken != "" {
		discovered, err := discoverLatestRolloutPath()
		if err != nil {
			return err
		}
		rolloutPath = discovered
	}

	localCtx, cancel := context.WithCancel(context.Background())

	var cmd *exec.Cmd
	if resumeToken == "" {
		cmd = exec.Command(codexBinary)
	} else {
		cmd = exec.Command(codexBinary, codexResumeSubcommand, resumeToken)
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

	restoreForeground := acquireTTYForeground(cmd)

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
		discovered, err := waitForRolloutPath(localCtx)
		if err != nil {
			_ = cmd.Process.Kill()
			cancel()
			return err
		}
		rolloutPath = discovered
	}

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
	e.mu.Unlock()
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

	return handle
}

func (e *Engine) stopRemoteAndWait(ctx context.Context) error {
	e.mu.Lock()
	client := e.remoteClient
	wasThinking := e.remoteThinking
	e.remoteClient = nil
	e.remoteEnabled = false
	e.remoteSessionActive = false
	e.remoteThinking = false
	e.remoteWaitStarted = false
	e.mu.Unlock()

	if wasThinking {
		e.tryEmit(agentengine.EvThinking{
			Mode:     agentengine.ModeRemote,
			Thinking: false,
			AtMs:     time.Now().UnixMilli(),
		})
	}

	// Only one Codex process should be active at a time. When leaving remote
	// mode, fully close the MCP server so switching to local doesn't leave
	// a background `codex mcp-server` running.
	if client == nil {
		return nil
	}
	_ = client.Shutdown(ctx)

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
	go func() { done <- client.Wait() }()
	select {
	case <-waitCtx.Done():
		// Best-effort: if graceful shutdown timed out, hard-kill and return the timeout.
		_ = client.Close()
		return waitCtx.Err()
	case err := <-done:
		return err
	}
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
	default:
		return
	}
}

// handleMCPEvent converts an MCP codex/event payload into an outbound wire record.
func (e *Engine) handleMCPEvent(event map[string]interface{}) {
	if event == nil {
		return
	}

	evtType, _ := event["type"].(string)

	// Emit identifiers opportunistically.
	e.emitIdentifiers()

	switch evtType {
	case "agent_message":
		e.setRemoteThinking(false, time.Now().UnixMilli())
		message := coerceString(event, "message")
		if strings.TrimSpace(message) == "" {
			return
		}
		raw, err := marshalAssistantTextRecord(message, "unknown")
		if err != nil {
			return
		}
		e.tryEmit(agentengine.EvOutboundRecord{
			Mode:    agentengine.ModeRemote,
			LocalID: types.NewCUID(),
			Payload: raw,
			AtMs:    time.Now().UnixMilli(),
		})
	case "agent_reasoning":
		e.setRemoteThinking(true, time.Now().UnixMilli())
		text := coerceString(event, "text", "message")
		if strings.TrimSpace(text) == "" {
			return
		}
		e.appendRemoteThinkingStep(text)
	case "exec_command_begin", "exec_approval_request":
		e.setRemoteThinking(true, time.Now().UnixMilli())
		callID := coerceString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		e.emitRemoteToolUIEventStart(callID, event)
	case "exec_command_end":
		// Keep thinking=true until we see an agent_message; Codex may execute
		// multiple tools per turn before producing the final assistant message.
		callID := coerceString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		e.emitRemoteToolUIEventEnd(callID, event)
	}
}

// startRemoteTurn resets per-turn state used for UI events (thinking log).
func (e *Engine) startRemoteTurn() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.remoteTurnID = types.NewCUID()
	e.remoteThinkingSteps = nil
	e.mu.Unlock()
}

// appendRemoteThinkingStep records a best-effort "thinking" status snippet and emits a UI event.
func (e *Engine) appendRemoteThinkingStep(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	const maxSteps = 16
	const maxStepLen = 120
	if len(text) > maxStepLen {
		text = text[:maxStepLen] + "…"
	}

	e.mu.Lock()
	if len(e.remoteThinkingSteps) > 0 && e.remoteThinkingSteps[len(e.remoteThinkingSteps)-1] == text {
		e.mu.Unlock()
		return
	}
	e.remoteThinkingSteps = append(e.remoteThinkingSteps, text)
	if len(e.remoteThinkingSteps) > maxSteps {
		e.remoteThinkingSteps = e.remoteThinkingSteps[len(e.remoteThinkingSteps)-maxSteps:]
	}
	turnID := e.remoteTurnID
	steps := append([]string(nil), e.remoteThinkingSteps...)
	e.mu.Unlock()

	if strings.TrimSpace(turnID) == "" {
		return
	}

	brief := "Thinking…"
	full := "### Thinking\n"
	for _, step := range steps {
		full += "- " + step + "\n"
		brief = step
	}

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       "thinking-" + turnID,
		Kind:          agentengine.UIEventThinking,
		Phase:         agentengine.UIEventPhaseUpdate,
		Status:        agentengine.UIEventStatusRunning,
		BriefMarkdown: brief,
		FullMarkdown:  strings.TrimSpace(full),
		AtMs:          time.Now().UnixMilli(),
	})
}

// emitRemoteToolUIEventStart emits a best-effort tool start UI event for Codex exec commands.
func (e *Engine) emitRemoteToolUIEventStart(callID string, event map[string]interface{}) {
	command := coerceString(event, "command", "cmd", "codex_command")
	if strings.TrimSpace(command) == "" {
		command = "exec"
	}
	brief := "🔧 bash: " + strings.TrimSpace(command)
	full := "### Tool: bash\n\n```sh\n" + strings.TrimSpace(command) + "\n```"

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       "tool-" + callID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseStart,
		Status:        agentengine.UIEventStatusRunning,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          time.Now().UnixMilli(),
	})
}

// emitRemoteToolUIEventEnd emits a best-effort tool end UI event for Codex exec commands.
func (e *Engine) emitRemoteToolUIEventEnd(callID string, event map[string]interface{}) {
	command := coerceString(event, "command", "cmd", "codex_command")
	if strings.TrimSpace(command) == "" {
		command = "exec"
	}
	status := agentengine.UIEventStatusOK
	if exit := coerceInt(event, "exit_code", "exitCode", "code"); exit != 0 {
		status = agentengine.UIEventStatusError
	}
	briefPrefix := "✅"
	if status == agentengine.UIEventStatusError {
		briefPrefix = "❌"
	}
	brief := briefPrefix + " bash: " + strings.TrimSpace(command)
	full := "### Tool: bash (" + string(status) + ")\n\n```sh\n" + strings.TrimSpace(command) + "\n```"

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       "tool-" + callID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseEnd,
		Status:        status,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          time.Now().UnixMilli(),
	})
}

// setRemoteThinking updates the cached remote thinking state and emits a
// best-effort EvThinking when the value changes.
func (e *Engine) setRemoteThinking(thinking bool, atMs int64) {
	if e == nil {
		return
	}
	if atMs == 0 {
		atMs = time.Now().UnixMilli()
	}

	e.mu.Lock()
	if e.remoteThinking == thinking {
		e.mu.Unlock()
		return
	}
	e.remoteThinking = thinking
	e.mu.Unlock()

	e.tryEmit(agentengine.EvThinking{
		Mode:     agentengine.ModeRemote,
		Thinking: thinking,
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
	if !thinking {
		phase = agentengine.UIEventPhaseEnd
		status = agentengine.UIEventStatusOK
	}
	brief := "Thinking…"
	if !thinking {
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

// emitIdentifiers emits session id + rollout path if available.
func (e *Engine) emitIdentifiers() {
	e.mu.Lock()
	client := e.remoteClient
	e.mu.Unlock()
	if client == nil {
		return
	}

	resumeToken := client.ResumeToken()
	if strings.TrimSpace(resumeToken) != "" {
		e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeRemote, ResumeToken: resumeToken})
	}
	rolloutPath := client.RolloutPath()
	if strings.TrimSpace(rolloutPath) != "" {
		e.tryEmit(agentengine.EvRolloutPath{Mode: agentengine.ModeRemote, Path: rolloutPath})
	}
}

// handlePermission turns an MCP elicitation request into a PermissionDecision.
func (e *Engine) handlePermission(requestID string, toolName string, input map[string]interface{}, requester agentengine.PermissionRequester) (*codex.PermissionDecision, error) {
	if requester == nil {
		return &codex.PermissionDecision{Decision: "denied", Message: "permission requester not configured"}, nil
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	decision, err := requester.AwaitPermission(ctx, requestID, toolName, payload, time.Now().UnixMilli())
	if err != nil {
		return &codex.PermissionDecision{Decision: "denied", Message: err.Error()}, nil
	}

	approved := codexDecisionDenied
	if decision.Allow {
		approved = codexDecisionApproved
	}
	return &codex.PermissionDecision{
		Decision: approved,
		Message:  decision.Message,
	}, nil
}

// tryEmit enqueues an engine event without blocking.
func (e *Engine) tryEmit(ev agentengine.Event) {
	if e == nil {
		return
	}
	select {
	case e.events <- ev:
	default:
	}
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
func resolveMode(currentPermissionMode string, currentModel string, meta map[string]any) (string, string, bool) {
	permissionMode := currentPermissionMode
	model := currentModel
	changed := false

	if permissionMode == "" {
		permissionMode = permissionModeDefault
	}

	if meta == nil {
		return permissionMode, model, changed
	}

	if raw, ok := meta[configKeyPermissionMode]; ok {
		if value, ok := raw.(string); ok && value != "" && value != permissionMode {
			permissionMode = value
			changed = true
		}
	}

	if raw, ok := meta[configKeyModel]; ok {
		if raw == nil {
			if model != "" {
				model = ""
				changed = true
			}
		} else if value, ok := raw.(string); ok && value != model {
			model = value
			changed = true
		}
	}

	return permissionMode, model, changed
}

// approvalPolicy maps Delight's permissionMode value to a Codex approval-policy.
func approvalPolicy(permissionMode string) string {
	switch permissionMode {
	case "read-only":
		return "never"
	case "safe-yolo", "yolo":
		return "on-failure"
	default:
		return "untrusted"
	}
}

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

// coerceString returns the first non-empty string from the given keys.
func coerceString(event map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if raw, ok := event[key]; ok {
			if s, ok := raw.(string); ok && s != "" {
				return s
			}
			if raw != nil {
				if s := fmt.Sprint(raw); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// coerceInt returns the first key that can be interpreted as an integer.
func coerceInt(event map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		raw, ok := event[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case int:
			return int64(v)
		case int64:
			return v
		case float64:
			return int64(v)
		case string:
			s := strings.TrimSpace(v)
			if s == "" {
				continue
			}
			if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
				return parsed
			}
		default:
			s := strings.TrimSpace(fmt.Sprint(v))
			if s == "" {
				continue
			}
			if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

// normalizeUserText normalizes user input text for echo suppression.
func normalizeUserText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// discoverLatestRolloutPath scans ~/.codex/sessions for the most recently updated rollout JSONL.
func discoverLatestRolloutPath() (string, error) {
	return discoverLatestRolloutPathImpl()
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
