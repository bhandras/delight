// Package claudeengine adapts Claude Code (local PTY + session scanner and
// remote stream-json bridge) to the agentengine.AgentEngine interface.
//
// This allows the SessionActor runtime to manage Claude and Codex uniformly.
package claudeengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// localSessionFileWaitTimeout bounds how long we wait for Claude to create
	// the session JSONL after the process emits the session id.
	localSessionFileWaitTimeout = 5 * time.Second
)

const (
	// localShutdownTimeout bounds how long we wait for Claude local mode to exit
	// after requesting a stop (best-effort).
	localShutdownTimeout = 2 * time.Second

	// remoteShutdownTimeout bounds how long we wait for Claude remote mode to exit
	// after requesting a stop (best-effort).
	remoteShutdownTimeout = 2 * time.Second
)

const (
	// claudePermissionAllow indicates an approval response to a permission request.
	claudePermissionAllow = "allow"
	// claudePermissionDeny indicates a rejection response to a permission request.
	claudePermissionDeny = "deny"
)

const (
	// claudeMetaKeyModel is the stream-json metadata key used to select a model.
	claudeMetaKeyModel = "model"
	// claudeMetaKeyPermissionMode is the stream-json metadata key used to select
	// a permission mode preset.
	claudeMetaKeyPermissionMode = "permissionMode"
)

const (
	// claudePermissionModeDefault is Claude Code's default permission mode.
	claudePermissionModeDefault = "default"
	// claudePermissionModePlan is Claude Code's plan-only permission mode.
	claudePermissionModePlan = "plan"
	// claudePermissionModeAcceptEdits is Claude Code's "accept edits" permission mode.
	claudePermissionModeAcceptEdits = "acceptEdits"
	// claudePermissionModeBypassPermissions is Claude Code's "bypass permissions" mode.
	claudePermissionModeBypassPermissions = "bypassPermissions"
)

// normalizeClaudeConfig returns a stable Claude config snapshot.
//
// Claude ignores reasoning effort; permission mode defaults to "default".
func normalizeClaudeConfig(cfg agentengine.AgentConfig) agentengine.AgentConfig {
	permissionMode := strings.TrimSpace(cfg.PermissionMode)
	switch strings.ToLower(permissionMode) {
	case "":
		permissionMode = claudePermissionModeDefault
	case "default":
		permissionMode = claudePermissionModeDefault
	case "plan":
		permissionMode = claudePermissionModePlan
	case "acceptedits":
		permissionMode = claudePermissionModeAcceptEdits
	case "bypasspermissions":
		permissionMode = claudePermissionModeBypassPermissions
	}

	out := agentengine.AgentConfig{
		Model:          strings.TrimSpace(cfg.Model),
		PermissionMode: permissionMode,
	}
	// "default" means "no explicit override" (do not pass --model to upstream).
	if strings.EqualFold(out.Model, "default") {
		out.Model = ""
	}
	return out
}

// mergeClaudeMessageMeta returns a meta object that includes stable engine
// settings while preserving any caller-provided metadata keys.
func mergeClaudeMessageMeta(meta map[string]any, cfg agentengine.AgentConfig) map[string]any {
	cfg = normalizeClaudeConfig(cfg)

	if meta == nil {
		meta = make(map[string]any, 2)
	} else {
		// Copy to avoid mutating caller-provided maps (which may be reused).
		copyMeta := make(map[string]any, len(meta)+2)
		for k, v := range meta {
			copyMeta[k] = v
		}
		meta = copyMeta
	}

	// Always set permissionMode so switching back to "default" takes effect.
	meta[claudeMetaKeyPermissionMode] = cfg.PermissionMode
	if cfg.Model != "" {
		meta[claudeMetaKeyModel] = cfg.Model
	}
	return meta
}

// Engine adapts Claude local/remote runners to agentengine.AgentEngine.
type Engine struct {
	mu sync.Mutex

	workDir string
	debug   bool

	requester agentengine.PermissionRequester
	config    agentengine.AgentConfig

	events chan agentengine.Event
	closed chan struct{}
	once   sync.Once

	localProc    *claude.Process
	localScanner *claude.Scanner
	localCtx     context.Context
	localCancel  context.CancelFunc
	localExited  chan struct{}
	localExitErr *error

	remoteBridge    *claude.RemoteBridge
	remoteCtx       context.Context
	remoteCancel    context.CancelFunc
	remoteExited    chan struct{}
	remoteExitErr   *error
	remoteSessionID string
	remoteTurnID    string
	remoteThinking  bool
	remoteToolNames map[string]string

	waitOnce sync.Once
	waitErr  error
	waitCh   chan struct{}
}

// New returns a new Claude engine instance.
func New(workDir string, requester agentengine.PermissionRequester, debug bool) *Engine {
	return &Engine{
		workDir:         workDir,
		debug:           debug,
		requester:       requester,
		config:          agentengine.AgentConfig{},
		events:          make(chan agentengine.Event, 128),
		closed:          make(chan struct{}),
		waitCh:          make(chan struct{}),
		localCtx:        context.Background(),
		remoteCtx:       context.Background(),
		localCancel:     func() {},
		remoteCancel:    func() {},
		remoteToolNames: make(map[string]string),
	}
}

// Events implements agentengine.AgentEngine.
func (e *Engine) Events() <-chan agentengine.Event {
	return e.events
}

// Start implements agentengine.AgentEngine.
func (e *Engine) Start(ctx context.Context, spec agentengine.EngineStartSpec) error {
	if e == nil {
		return fmt.Errorf("claude engine is nil")
	}

	e.mu.Lock()
	e.config = normalizeClaudeConfig(spec.Config)
	e.mu.Unlock()

	switch spec.Mode {
	case agentengine.ModeLocal:
		return e.startLocal(ctx, spec)
	case agentengine.ModeRemote:
		return e.startRemote(ctx, spec)
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

	e.once.Do(func() {
		close(e.closed)
		close(e.events)
	})

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
	_ = ctx
	if e == nil {
		return fmt.Errorf("claude engine is nil")
	}

	e.mu.Lock()
	bridge := e.remoteBridge
	cfg := e.config
	e.mu.Unlock()
	if bridge == nil {
		return fmt.Errorf("claude remote bridge not running")
	}

	e.startRemoteTurn()
	nowMs := time.Now().UnixMilli()
	e.setRemoteThinking(true, nowMs)

	meta := mergeClaudeMessageMeta(msg.Meta, cfg)
	return bridge.SendUserMessage(msg.Text, meta)
}

// Abort implements agentengine.AgentEngine.
func (e *Engine) Abort(ctx context.Context) error {
	_ = ctx
	if e == nil {
		return nil
	}
	e.mu.Lock()
	bridge := e.remoteBridge
	e.mu.Unlock()
	if bridge == nil {
		return nil
	}
	return bridge.Abort()
}

// Capabilities implements agentengine.AgentEngine.
func (e *Engine) Capabilities() agentengine.AgentCapabilities {
	// Claude supports session-scoped model selection, but the upstream CLI only
	// applies it at process start. Delight provides best-effort application by
	// passing the selected model as stream-json metadata; the bridge respawns
	// the underlying Claude process when the model changes.
	return agentengine.AgentCapabilities{
		Models: []string{
			"default",
			"sonnet",
			"opus",
			"haiku",
		},
		PermissionModes: []string{
			claudePermissionModeDefault,
			claudePermissionModePlan,
			claudePermissionModeAcceptEdits,
			claudePermissionModeBypassPermissions,
		},
	}
}

// CurrentConfig implements agentengine.AgentEngine.
func (e *Engine) CurrentConfig() agentengine.AgentConfig {
	if e == nil {
		return agentengine.AgentConfig{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.config
}

// ApplyConfig implements agentengine.AgentEngine.
func (e *Engine) ApplyConfig(ctx context.Context, cfg agentengine.AgentConfig) error {
	_ = ctx
	e.mu.Lock()
	e.config = normalizeClaudeConfig(cfg)
	e.mu.Unlock()

	// Model/permission mode are applied to the upstream Claude process by the
	// Node bridge when the next user message is sent.
	return nil
}

// Wait implements agentengine.AgentEngine.
func (e *Engine) Wait() error {
	if e == nil {
		return nil
	}

	e.waitOnce.Do(func() {
		defer close(e.waitCh)

		e.mu.Lock()
		localExited := e.localExited
		localExitErr := e.localExitErr
		remoteExited := e.remoteExited
		remoteExitErr := e.remoteExitErr
		e.mu.Unlock()

		if localExited != nil && localExitErr != nil {
			<-localExited
			e.waitErr = *localExitErr
			return
		}
		if remoteExited != nil && remoteExitErr != nil {
			<-remoteExited
			e.waitErr = *remoteExitErr
			return
		}
		e.waitErr = nil
	})

	<-e.waitCh
	return e.waitErr
}

// InjectLine injects a single line into the local interactive Claude runner.
//
// This is a best-effort escape hatch used by the session FSM when remote mode
// fails to start but inbound phone messages need to be delivered somewhere.
func (e *Engine) InjectLine(text string) error {
	e.mu.Lock()
	proc := e.localProc
	e.mu.Unlock()
	if proc == nil {
		return fmt.Errorf("local runner not active")
	}
	return proc.SendLine(text)
}

func (e *Engine) startLocal(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx

	workDir := strings.TrimSpace(spec.WorkDir)
	if workDir == "" {
		workDir = e.workDir
	}
	if workDir == "" {
		return fmt.Errorf("missing workDir")
	}

	resumeToken := strings.TrimSpace(spec.ResumeToken)
	cfg := normalizeClaudeConfig(spec.Config)
	proc, err := claude.NewProcess(claude.ProcessOptions{
		WorkDir:        workDir,
		ResumeToken:    resumeToken,
		Model:          cfg.Model,
		PermissionMode: cfg.PermissionMode,
		Debug:          e.debug,
	})
	if err != nil {
		return err
	}
	if err := proc.Start(); err != nil {
		return err
	}

	localCtx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	exitErr := new(error)

	e.mu.Lock()
	old := e.detachLocalLocked()
	e.localProc = proc
	e.localScanner = nil
	e.localCtx = localCtx
	e.localCancel = cancel
	e.localExited = exited
	e.localExitErr = exitErr
	e.mu.Unlock()

	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeLocal})

	old.stop(context.Background())

	go func(p *claude.Process, done chan struct{}, errPtr *error) {
		err := p.Wait()
		*errPtr = err
		close(done)
		e.tryEmit(agentengine.EvExited{Mode: agentengine.ModeLocal, Err: err})
	}(proc, exited, exitErr)

	go e.watchLocalSession(localCtx, workDir, proc)

	return nil
}

type localStopHandle struct {
	cancel  context.CancelFunc
	proc    *claude.Process
	scanner *claude.Scanner
	exited  chan struct{}
	exitErr *error
}

func (h localStopHandle) stop(ctx context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}
	if h.scanner != nil {
		h.scanner.Stop()
	}
	if h.proc != nil {
		_ = h.proc.Kill()
	}
	if h.exited == nil || h.exitErr == nil {
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
	case <-h.exited:
		return *h.exitErr
	}
}

func (e *Engine) stopLocalAndWait(ctx context.Context) error {
	e.mu.Lock()
	handle := e.detachLocalLocked()
	e.mu.Unlock()
	return handle.stop(ctx)
}

func (e *Engine) detachLocalLocked() localStopHandle {
	handle := localStopHandle{
		cancel:  e.localCancel,
		proc:    e.localProc,
		scanner: e.localScanner,
		exited:  e.localExited,
		exitErr: e.localExitErr,
	}
	e.localCancel = nil
	e.localProc = nil
	e.localScanner = nil
	e.localCtx = context.Background()
	e.localExited = nil
	e.localExitErr = nil
	return handle
}

func (e *Engine) startRemote(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx

	workDir := strings.TrimSpace(spec.WorkDir)
	if workDir == "" {
		workDir = e.workDir
	}
	if workDir == "" {
		return fmt.Errorf("missing workDir")
	}
	resumeToken := strings.TrimSpace(spec.ResumeToken)

	bridge, err := claude.NewRemoteBridge(workDir, resumeToken, e.debug)
	if err != nil {
		return err
	}

	// Install a synchronous permission handler so Claude's control_request flow
	// blocks until a mobile user decides.
	bridge.SetPermissionHandler(func(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
		return e.handleRemotePermissionRequest(requestID, toolName, input)
	})

	bridge.SetMessageHandler(func(msg *claude.RemoteMessage) error {
		return e.handleRemoteBridgeMessage(msg)
	})

	if err := bridge.Start(); err != nil {
		return err
	}

	remoteCtx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	exitErr := new(error)

	e.mu.Lock()
	old := e.detachRemoteLocked()
	e.remoteBridge = bridge
	e.remoteCtx = remoteCtx
	e.remoteCancel = cancel
	e.remoteExited = exited
	e.remoteExitErr = exitErr
	e.mu.Unlock()

	if resumeToken != "" {
		e.mu.Lock()
		e.remoteSessionID = resumeToken
		e.mu.Unlock()
		e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeRemote, ResumeToken: resumeToken})
	}
	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeRemote})

	old.stop(context.Background())

	go func(b *claude.RemoteBridge, done chan struct{}, errPtr *error) {
		err := b.Wait()
		*errPtr = err
		close(done)
		e.tryEmit(agentengine.EvExited{Mode: agentengine.ModeRemote, Err: err})
	}(bridge, exited, exitErr)

	return nil
}

type remoteStopHandle struct {
	cancel  context.CancelFunc
	bridge  *claude.RemoteBridge
	exited  chan struct{}
	exitErr *error
}

func (h remoteStopHandle) stop(ctx context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}
	if h.bridge != nil {
		_ = h.bridge.Kill()
	}
	if h.exited == nil || h.exitErr == nil {
		return nil
	}

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > remoteShutdownTimeout {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(waitCtx, remoteShutdownTimeout)
		defer cancel()
	}

	select {
	case <-waitCtx.Done():
		return waitCtx.Err()
	case <-h.exited:
		return *h.exitErr
	}
}

func (e *Engine) stopRemoteAndWait(ctx context.Context) error {
	e.mu.Lock()
	handle := e.detachRemoteLocked()
	e.mu.Unlock()
	return handle.stop(ctx)
}

func (e *Engine) detachRemoteLocked() remoteStopHandle {
	handle := remoteStopHandle{
		cancel:  e.remoteCancel,
		bridge:  e.remoteBridge,
		exited:  e.remoteExited,
		exitErr: e.remoteExitErr,
	}
	e.remoteCancel = nil
	e.remoteBridge = nil
	e.remoteCtx = context.Background()
	e.remoteExited = nil
	e.remoteExitErr = nil
	e.remoteSessionID = ""
	return handle
}

// handleRemoteBridgeMessage processes a single message from the Claude remote
// bridge and emits any derived engine events.
func (e *Engine) handleRemoteBridgeMessage(msg *claude.RemoteMessage) error {
	if msg == nil {
		return nil
	}

	// Capture a stable resume token for remote mode. The bridge emits a "system"
	// init message with a session_id once Claude has started (or resumed).
	if msg.Type == "system" && msg.Subtype == "init" {
		sessionID := strings.TrimSpace(msg.SessionID)
		if sessionID != "" {
			shouldEmit := false
			e.mu.Lock()
			if e.remoteSessionID != sessionID {
				e.remoteSessionID = sessionID
				shouldEmit = true
			}
			e.mu.Unlock()
			if shouldEmit {
				e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeRemote, ResumeToken: sessionID})
			}
		}
	}

	raw, ok := buildRawRecordBytesFromRemote(msg)
	if !ok {
		return nil
	}

	nowMs := time.Now().UnixMilli()
	e.emitRemoteUIEventsFromRaw(raw, nowMs)
	e.tryEmit(agentengine.EvOutboundRecord{
		Mode:    agentengine.ModeRemote,
		LocalID: types.NewCUID(),
		Payload: raw,
		AtMs:    nowMs,
	})
	return nil
}

func (e *Engine) watchLocalSession(ctx context.Context, workDir string, proc *claude.Process) {
	if proc == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case sessionID, ok := <-proc.SessionID():
			if !ok || strings.TrimSpace(sessionID) == "" {
				return
			}
			if !claude.WaitForSessionFile(workDir, sessionID, localSessionFileWaitTimeout) {
				continue
			}

			e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeLocal, ResumeToken: sessionID})

			scanner := claude.NewScanner(workDir, sessionID, e.debug)
			scanner.Start()

			e.mu.Lock()
			// If this local runner is no longer current, stop immediately.
			if e.localProc != proc {
				e.mu.Unlock()
				scanner.Stop()
				return
			}
			if e.localScanner != nil {
				e.localScanner.Stop()
			}
			e.localScanner = scanner
			e.mu.Unlock()

			go e.forwardScannerMessages(ctx, scanner)
			return
		}
	}
}

func (e *Engine) forwardScannerMessages(ctx context.Context, scanner *claude.Scanner) {
	if scanner == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-scanner.Messages():
			if !ok || msg == nil {
				return
			}

			// Deep copy the message to detach from scanner buffers.
			raw, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			var copyMsg claude.SessionMessage
			if err := json.Unmarshal(raw, &copyMsg); err != nil {
				continue
			}

			nowMs := time.Now().UnixMilli()
			plaintext, err := json.Marshal(copyMsg)
			if err != nil {
				continue
			}

			userText := ""
			if copyMsg.Type == "user" {
				userText = extractClaudeUserText(copyMsg.Message)
			}

			e.tryEmit(agentengine.EvOutboundRecord{
				Mode:               agentengine.ModeLocal,
				LocalID:            copyMsg.UUID,
				Payload:            plaintext,
				UserTextNormalized: userText,
				AtMs:               nowMs,
			})
		}
	}
}

func (e *Engine) handleRemotePermissionRequest(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(toolName) == "" {
		return &claude.PermissionResponse{Behavior: claudePermissionDeny, Message: "invalid permission request"}, nil
	}

	e.mu.Lock()
	requester := e.requester
	ctx := e.remoteCtx
	e.mu.Unlock()
	if requester == nil {
		return &claude.PermissionResponse{Behavior: claudePermissionDeny, Message: "permission requester not configured"}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Bound by a timeout so upstream Claude doesn't stall forever if the phone
	// becomes unreachable.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	decision, err := requester.AwaitPermission(waitCtx, requestID, toolName, input, time.Now().UnixMilli())
	if err != nil {
		return &claude.PermissionResponse{Behavior: claudePermissionDeny, Message: err.Error()}, nil
	}

	resp := &claude.PermissionResponse{
		Behavior: claudePermissionDeny,
		Message:  decision.Message,
	}
	if decision.Allow {
		resp.Behavior = claudePermissionAllow
		// Claude Code expects allow responses to include updatedInput (even when
		// unmodified). Mirror legacy behavior by echoing the input if valid JSON.
		var probe any
		if json.Unmarshal(input, &probe) == nil {
			resp.UpdatedInput = append(json.RawMessage(nil), input...)
		}
		// Match legacy behavior: omit message field for allow responses.
		resp.Message = ""
	}
	return resp, nil
}

// startRemoteTurn resets per-turn state used for UI events.
func (e *Engine) startRemoteTurn() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.remoteTurnID = types.NewCUID()
	e.mu.Unlock()
}

// setRemoteThinking updates the cached remote thinking state and emits
// best-effort EvThinking and EvUIEvent updates.
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
	turnID := e.remoteTurnID
	e.mu.Unlock()

	e.tryEmit(agentengine.EvThinking{Mode: agentengine.ModeRemote, Thinking: thinking, AtMs: atMs})

	if strings.TrimSpace(turnID) == "" {
		return
	}

	phase := agentengine.UIEventPhaseUpdate
	status := agentengine.UIEventStatusRunning
	brief := "Thinking…"
	if !thinking {
		phase = agentengine.UIEventPhaseEnd
		status = agentengine.UIEventStatusOK
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

// emitRemoteUIEventsFromRaw parses a Claude output record and emits UI events
// (thinking/tool lifecycle) as rendered Markdown.
func (e *Engine) emitRemoteUIEventsFromRaw(raw []byte, nowMs int64) {
	if len(raw) == 0 {
		return
	}
	rec, ok, err := wire.TryParseAgentOutputRecord(raw)
	if err != nil || !ok || rec == nil {
		return
	}

	msg := rec.Content.Data.Message
	blocks := msg.Content
	if len(blocks) == 0 {
		return
	}

	for _, block := range blocks {
		blockType := normalizeClaudeBlockType(block.Type)
		switch blockType {
		case "text":
			if strings.TrimSpace(block.Text) != "" && msg.Role == "assistant" {
				e.setRemoteThinking(false, nowMs)
			}
		case "thinking":
			// Do not forward thinking content; treat it as a generic busy signal.
			e.setRemoteThinking(true, nowMs)
		case "tool-use":
			e.emitClaudeToolUIStart(block, nowMs)
		case "tool-result":
			e.emitClaudeToolUIEnd(block, nowMs)
		default:
			continue
		}
	}
}

func normalizeClaudeBlockType(t string) string {
	t = strings.TrimSpace(strings.ToLower(t))
	t = strings.ReplaceAll(t, "_", "-")
	return t
}

func (e *Engine) emitClaudeToolUIStart(block wire.ContentBlock, nowMs int64) {
	toolID := ""
	if block.Fields != nil {
		if raw, ok := block.Fields["id"]; ok {
			toolID, _ = raw.(string)
		}
	}
	if strings.TrimSpace(toolID) == "" {
		toolID = types.NewCUID()
	}

	name := "tool"
	var input any
	if block.Fields != nil {
		if raw, ok := block.Fields["name"]; ok {
			if s, ok := raw.(string); ok && s != "" {
				name = s
			}
		}
		input = block.Fields["input"]
	}

	e.mu.Lock()
	if e.remoteToolNames == nil {
		e.remoteToolNames = make(map[string]string)
	}
	e.remoteToolNames[toolID] = name
	e.mu.Unlock()

	brief, full := renderToolMarkdown(name, input, nil, agentengine.UIEventStatusRunning)
	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       "tool-" + toolID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseStart,
		Status:        agentengine.UIEventStatusRunning,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          nowMs,
	})
}

func (e *Engine) emitClaudeToolUIEnd(block wire.ContentBlock, nowMs int64) {
	toolID := ""
	status := agentengine.UIEventStatusOK
	var output any

	if block.Fields != nil {
		if raw, ok := block.Fields["tool_use_id"]; ok {
			toolID, _ = raw.(string)
		}
		if raw, ok := block.Fields["is_error"]; ok {
			if v, ok := raw.(bool); ok && v {
				status = agentengine.UIEventStatusError
			}
		}
		output = block.Fields["content"]
	}
	if strings.TrimSpace(toolID) == "" {
		toolID = types.NewCUID()
	}

	e.mu.Lock()
	name := e.remoteToolNames[toolID]
	delete(e.remoteToolNames, toolID)
	e.mu.Unlock()
	if strings.TrimSpace(name) == "" {
		name = "tool"
	}

	brief, full := renderToolMarkdown(name, nil, output, status)
	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       "tool-" + toolID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseEnd,
		Status:        status,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          nowMs,
	})
}

func renderToolMarkdown(name string, input any, output any, status agentengine.UIEventStatus) (brief string, full string) {
	normalized := strings.TrimSpace(strings.ToLower(name))
	if normalized == "" {
		normalized = "tool"
	}

	prefix := "🔧"
	switch status {
	case agentengine.UIEventStatusOK:
		prefix = "✅"
	case agentengine.UIEventStatusError:
		prefix = "❌"
	}
	brief = prefix + " " + normalized

	heading := "### Tool: " + normalized
	if status != "" && status != agentengine.UIEventStatusRunning {
		heading += " (" + string(status) + ")"
	}
	fullLines := []string{heading}

	if normalized == "bash" {
		if cmd := extractBashCommand(input); cmd != "" {
			brief = prefix + " bash: " + cmd
			fullLines = append(fullLines, "", "```sh", cmd, "```")
		}
	} else if input != nil {
		if pretty := prettyJSON(input, 2_000); pretty != "" {
			fullLines = append(fullLines, "", "```json", pretty, "```")
		}
	}

	if status != agentengine.UIEventStatusRunning && output != nil {
		if pretty := prettyJSON(output, 2_000); pretty != "" {
			fullLines = append(fullLines, "", "```", pretty, "```")
		}
	}

	full = strings.TrimSpace(strings.Join(fullLines, "\n"))
	return brief, full
}

func extractBashCommand(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	if raw, ok := m["command"]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func prettyJSON(value any, maxLen int) string {
	if value == nil {
		return ""
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	out := string(raw)
	if maxLen > 0 && len(out) > maxLen {
		out = out[:maxLen] + "…"
	}
	return out
}

func (e *Engine) tryEmit(ev agentengine.Event) {
	if ev == nil {
		return
	}

	select {
	case <-e.closed:
		return
	default:
	}

	defer func() { _ = recover() }()
	select {
	case e.events <- ev:
	default:
		// Best-effort: drop if downstream is slow.
	}
}

func buildRawRecordBytesFromRemote(msg *claude.RemoteMessage) ([]byte, bool) {
	if msg == nil {
		return nil, false
	}

	// Prefer already-structured payloads.
	if len(msg.Message) > 0 && msg.Type != "raw" {
		raw := json.RawMessage(msg.Message)
		// If it already looks like a wire record, forward as-is.
		var probe struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && probe.Role != "" && probe.Content.Type != "" {
			return []byte(raw), true
		}
	}

	switch msg.Type {
	case "raw":
		if len(msg.Message) == 0 {
			return nil, false
		}
		return []byte(msg.Message), true
	case "message":
		role := msg.Role
		contentBlocks, err := wire.DecodeContentBlocks(msg.Content)
		if err != nil {
			return nil, false
		}
		if role == "" || len(contentBlocks) == 0 {
			return nil, false
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}
		outType := role
		rec := wire.AgentOutputRecord{
			Role: "agent",
			Content: wire.AgentOutputContent{
				Type: "output",
				Data: wire.AgentOutputData{
					Type:             outType,
					IsSidechain:      false,
					IsCompactSummary: false,
					IsMeta:           false,
					UUID:             types.NewCUID(),
					ParentUUID:       nil,
					Message: wire.AgentMessage{
						Role:    role,
						Model:   model,
						Content: contentBlocks,
					},
				},
			},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, false
		}
		return data, true
	case "assistant":
		text := ""
		switch v := msg.Content.(type) {
		case string:
			text = v
		default:
			blocks, err := wire.DecodeContentBlocks(v)
			if err == nil {
				for _, block := range blocks {
					if block.Type == "text" && block.Text != "" {
						text = block.Text
						break
					}
				}
			}
		}
		if text == "" && msg.Result != "" {
			text = msg.Result
		}
		if text == "" {
			return nil, false
		}
		model := msg.Model
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
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, false
		}
		return data, true
	default:
		return nil, false
	}
}

func extractClaudeUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	var walk func(any) string
	walk = func(v any) string {
		switch t := v.(type) {
		case string:
			return t
		case map[string]any:
			if content, ok := t["content"]; ok {
				if s := walk(content); s != "" {
					return s
				}
			}
			if text, ok := t["text"]; ok {
				if s := walk(text); s != "" {
					return s
				}
			}
			if message, ok := t["message"]; ok {
				if s := walk(message); s != "" {
					return s
				}
			}
			if data, ok := t["data"]; ok {
				if s := walk(data); s != "" {
					return s
				}
			}
			return ""
		case []any:
			for _, part := range t {
				if s := walk(part); s != "" {
					return s
				}
			}
			return ""
		default:
			return ""
		}
	}

	return normalizeUserText(walk(value))
}

func normalizeUserText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimSpace(text)
}
