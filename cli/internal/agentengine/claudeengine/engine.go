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

// Engine adapts Claude local/remote runners to agentengine.AgentEngine.
type Engine struct {
	mu sync.Mutex

	workDir string
	debug   bool

	requester agentengine.PermissionRequester

	events chan agentengine.Event
	closed chan struct{}
	once   sync.Once

	localProc    *claude.Process
	localScanner *claude.Scanner
	localCtx     context.Context
	localCancel  context.CancelFunc
	localExited  chan struct{}
	localExitErr *error

	remoteBridge  *claude.RemoteBridge
	remoteCtx     context.Context
	remoteCancel  context.CancelFunc
	remoteExited  chan struct{}
	remoteExitErr *error

	waitOnce sync.Once
	waitErr  error
	waitCh   chan struct{}
}

// New returns a new Claude engine instance.
func New(workDir string, requester agentengine.PermissionRequester, debug bool) *Engine {
	return &Engine{
		workDir:      workDir,
		debug:        debug,
		requester:    requester,
		events:       make(chan agentengine.Event, 128),
		closed:       make(chan struct{}),
		waitCh:       make(chan struct{}),
		localCtx:     context.Background(),
		remoteCtx:    context.Background(),
		localCancel:  func() {},
		remoteCancel: func() {},
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
	e.mu.Unlock()
	if bridge == nil {
		return fmt.Errorf("claude remote bridge not running")
	}
	return bridge.SendUserMessage(msg.Text, msg.Meta)
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

	proc, err := claude.NewProcess(workDir, e.debug)
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
		raw, ok := buildRawRecordBytesFromRemote(msg)
		if !ok {
			return nil
		}

		nowMs := time.Now().UnixMilli()
		e.tryEmit(agentengine.EvOutboundRecord{
			Mode:    agentengine.ModeRemote,
			LocalID: types.NewCUID(),
			Payload: raw,
			AtMs:    nowMs,
		})
		return nil
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
	return handle
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
