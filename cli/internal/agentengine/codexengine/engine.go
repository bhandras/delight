package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/internal/codex/rollout"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
)

const (
	// codexBinary is the executable name used to start Codex.
	codexBinary = "codex"
	// codexResumeSubcommand is the Codex CLI subcommand to resume a session.
	codexResumeSubcommand = "resume"
)

const (
	// permissionModeDefault mirrors Codex's default permission behavior.
	permissionModeDefault = "default"
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

	localCmd    *exec.Cmd
	localCancel context.CancelFunc
	localTailer *rollout.Tailer

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
		e.stopLocal()
	case agentengine.ModeRemote:
		e.mu.Lock()
		e.remoteEnabled = false
		e.mu.Unlock()
	default:
		return fmt.Errorf("unsupported stop mode: %q", mode)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Close implements agentengine.AgentEngine.
func (e *Engine) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}

	e.stopLocal()

	e.mu.Lock()
	client := e.remoteClient
	e.remoteClient = nil
	e.remoteEnabled = false
	e.mu.Unlock()
	if client != nil {
		_ = client.Close()
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
		localCmd := e.localCmd
		client := e.remoteClient
		e.mu.Unlock()

		if localCmd != nil {
			e.waitErr = localCmd.Wait()
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
		e.mu.Unlock()
	} else {
		e.mu.Lock()
		e.remoteEnabled = true
		e.mu.Unlock()
	}

	e.tryEmit(agentengine.EvReady{})
	return nil
}

// startLocal starts a Codex local TUI process and a rollout tailer.
func (e *Engine) startLocal(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx
	if strings.TrimSpace(spec.ResumeToken) == "" {
		return fmt.Errorf("missing codex resume token")
	}

	rolloutPath := spec.RolloutPath
	if strings.TrimSpace(rolloutPath) == "" {
		discovered, err := discoverLatestRolloutPath()
		if err != nil {
			return err
		}
		rolloutPath = discovered
	}

	localCtx, cancel := context.WithCancel(context.Background())

	cmd := exec.Command(codexBinary, codexResumeSubcommand, spec.ResumeToken)
	workDir := e.workDir
	if spec.WorkDir != "" {
		workDir = spec.WorkDir
	}
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	tailer := rollout.NewTailer(rolloutPath, rollout.TailerOptions{StartAtEnd: true})
	if err := tailer.Start(localCtx); err != nil {
		_ = cmd.Process.Kill()
		cancel()
		return err
	}

	e.mu.Lock()
	e.stopLocalLocked()
	e.localCmd = cmd
	e.localCancel = cancel
	e.localTailer = tailer
	e.mu.Unlock()

	e.tryEmit(agentengine.EvReady{})

	go e.forwardRollout(localCtx, tailer)
	go func() {
		err := cmd.Wait()
		cancel()
		e.tryEmit(agentengine.EvExited{Err: err})
	}()

	return nil
}

// stopLocal stops the local process and rollout tailer, if present.
func (e *Engine) stopLocal() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocalLocked()
}

// stopLocalLocked is stopLocal under Engine.mu.
func (e *Engine) stopLocalLocked() {
	cancel := e.localCancel
	cmd := e.localCmd
	tailer := e.localTailer

	e.localCancel = nil
	e.localCmd = nil
	e.localTailer = nil

	if cancel != nil {
		cancel()
	}
	if tailer != nil {
		// Tailer exits on ctx cancellation.
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
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
		e.tryEmit(agentengine.EvSessionIdentified{ResumeToken: v.SessionID})
	case rollout.EvUserMessage:
		raw, err := marshalUserTextRecord(v.Text, nil)
		if err != nil {
			return
		}
		e.tryEmit(agentengine.EvOutboundRecord{
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

	sessionID := client.SessionID()
	if strings.TrimSpace(sessionID) != "" {
		e.tryEmit(agentengine.EvSessionIdentified{ResumeToken: sessionID})
	}
	rolloutPath := client.RolloutPath()
	if strings.TrimSpace(rolloutPath) != "" {
		e.tryEmit(agentengine.EvRolloutPath{Path: rolloutPath})
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

	approved := "denied"
	if decision.Allow {
		approved = "approved"
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
