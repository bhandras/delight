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
	"strings"
	"sync"
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

	remoteClient         *codex.Client
	remoteSessionActive  bool
	remoteEnabled        bool
	remotePermissionMode string
	remoteModel          string
	remoteWaitStarted    bool

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
	e.mu.Lock()
	client := e.remoteClient
	active := e.remoteSessionActive
	enabled := e.remoteEnabled
	e.mu.Unlock()
	if client == nil {
		return fmt.Errorf("codex remote client not started")
	}
	if !enabled {
		return fmt.Errorf("codex remote mode not active")
	}

	permissionMode, model, modeChanged := resolveMode(e.remotePermissionMode, e.remoteModel, msg.Meta)
	if modeChanged {
		client.ClearSession()
		e.mu.Lock()
		e.remoteSessionActive = false
		e.remotePermissionMode = permissionMode
		e.remoteModel = model
		active = false
		e.mu.Unlock()
	}

	cfg := codex.SessionConfig{
		Prompt:         msg.Text,
		ApprovalPolicy: approvalPolicy(permissionMode),
		Sandbox:        sandboxPolicy(permissionMode),
		Cwd:            e.workDir,
		Model:          model,
	}

	if !active {
		if _, err := client.StartSession(ctx, cfg); err != nil {
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

	if _, err := client.ContinueSession(ctx, msg.Text); err != nil {
		e.mu.Lock()
		e.remoteSessionActive = false
		e.mu.Unlock()
		return err
	}
	e.emitIdentifiers()
	return nil
}

// Abort implements agentengine.AgentEngine.
func (e *Engine) Abort(ctx context.Context) error {
	_ = ctx
	// Codex MCP does not currently provide a standardized "abort turn" request.
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
		e.remotePermissionMode = permissionModeDefault
		e.remoteEnabled = true
		e.remoteWaitStarted = false
		e.mu.Unlock()
	} else {
		e.mu.Lock()
		e.remoteEnabled = true
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
	if h.tailer != nil {
		// Tailer exits on ctx cancellation.
	}
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
	e.remoteClient = nil
	e.remoteEnabled = false
	e.remoteSessionActive = false
	e.remoteWaitStarted = false
	e.mu.Unlock()

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
		raw, err := marshalCodexRecord(wire.CodexRecord{
			Type:    "message",
			Message: v.Text,
		})
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
		message := coerceString(event, "message")
		if strings.TrimSpace(message) == "" {
			return
		}
		e.emitCodexRecord(wire.CodexRecord{Type: "message", Message: message})
	case "agent_reasoning":
		text := coerceString(event, "text", "message")
		if strings.TrimSpace(text) == "" {
			return
		}
		e.emitCodexRecord(wire.CodexRecord{Type: "reasoning", Message: text})
	case "exec_command_begin", "exec_approval_request":
		callID := coerceString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		input := filterMap(event, "type", "call_id", "callId")
		e.emitCodexRecord(wire.CodexRecord{
			Type:   "tool-call",
			CallID: callID,
			Name:   "CodexBash",
			Input:  input,
			ID:     types.NewCUID(),
		})
	case "exec_command_end":
		callID := coerceString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		output := filterMap(event, "type", "call_id", "callId")
		e.emitCodexRecord(wire.CodexRecord{
			Type:   "tool-call-result",
			CallID: callID,
			Output: output,
			ID:     types.NewCUID(),
		})
	}
}

// emitCodexRecord encodes a CodexRecord and emits it as an outbound record event.
func (e *Engine) emitCodexRecord(record wire.CodexRecord) {
	raw, err := marshalCodexRecord(record)
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

// marshalCodexRecord encodes a CodexRecord into an AgentCodexRecord JSON payload.
func marshalCodexRecord(data any) ([]byte, error) {
	payload := wire.AgentCodexRecord{
		Role: "agent",
		Content: wire.AgentCodexContent{
			Type: "codex",
			Data: data,
		},
	}
	return json.Marshal(payload)
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

// filterMap copies event into a new map excluding the given keys.
func filterMap(event map[string]interface{}, excludeKeys ...string) map[string]interface{} {
	if event == nil {
		return nil
	}
	exclude := map[string]struct{}{}
	for _, key := range excludeKeys {
		exclude[key] = struct{}{}
	}
	out := make(map[string]interface{}, len(event))
	for key, val := range event {
		if _, ok := exclude[key]; ok {
			continue
		}
		out[key] = val
	}
	return out
}

// normalizeUserText normalizes user input text for echo suppression.
func normalizeUserText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// discoverLatestRolloutPath scans ~/.codex/sessions for the most recently updated rollout JSONL.
func discoverLatestRolloutPath() (string, error) {
	path, _, err := discoverLatestRolloutPathWithMod()
	return path, err
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
			path, _, err := discoverLatestRolloutPathWithMod()
			if err != nil {
				continue
			}
			if strings.TrimSpace(path) != "" {
				return path, nil
			}
		}
	}
}

// discoverLatestRolloutPathWithMod finds the latest rollout JSONL and returns its modtime.
func discoverLatestRolloutPathWithMod() (string, time.Time, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", time.Time{}, err
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
			return "", time.Time{}, fmt.Errorf("codex rollout directory not found: %s", root)
		}
		return "", time.Time{}, walkErr
	}

	if bestPath == "" {
		return "", time.Time{}, fmt.Errorf("no codex rollout jsonl found under %s", root)
	}
	return bestPath, bestMod, nil
}
