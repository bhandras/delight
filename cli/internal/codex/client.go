package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bhandras/delight/cli/internal/version"
	"github.com/bhandras/delight/shared/logger"
)

const (
	defaultProtocolVersion = "2024-11-05"
)

const (
	// serverMethodElicitationRequest is the MCP method used by some older Codex builds for approvals.
	serverMethodElicitationRequest = "elicitation/request"
	// serverMethodElicitationCreate is the MCP method used by newer Codex builds for approvals.
	serverMethodElicitationCreate = "elicitation/create"
	// serverMethodExecCommandApproval is the Codex server request method for exec approvals.
	serverMethodExecCommandApproval = "execCommandApproval"
	// serverMethodApplyPatchApproval is the Codex server request method for patch approvals.
	serverMethodApplyPatchApproval = "applyPatchApproval"
)

const (
	// codexElicitationExecApproval identifies an exec approval prompt.
	codexElicitationExecApproval = "exec-approval"
	// codexElicitationPatchApproval identifies an apply-patch approval prompt.
	codexElicitationPatchApproval = "patch-approval"
)

const (
	// codexToolBash is the logical tool name used for exec approvals.
	codexToolBash = "CodexBash"
	// codexToolApplyPatch is the logical tool name used for patch approvals.
	codexToolApplyPatch = "CodexApplyPatch"
	// codexToolUnknownApproval is used when we cannot classify an approval prompt.
	codexToolUnknownApproval = "CodexApproval"
)

const (
	// codexDecisionApproved indicates the approval was granted.
	codexDecisionApproved = "approved"
	// codexDecisionDenied indicates the approval was denied.
	codexDecisionDenied = "denied"
	// codexDecisionAllow is a compatibility decision string used by some clients.
	codexDecisionAllow = "allow"
	// codexDecisionDeny is a compatibility decision string used by some clients.
	codexDecisionDeny = "deny"
)

type PermissionDecision struct {
	Decision string
	Message  string
}

type PermissionHandler func(requestID string, toolName string, input map[string]interface{}) (*PermissionDecision, error)
type EventHandler func(event map[string]interface{})

type SessionConfig struct {
	Prompt           string
	ApprovalPolicy   string
	BaseInstructions string
	Config           map[string]interface{}
	Cwd              string
	IncludePlanTool  *bool
	Model            string
	Profile          string
	Sandbox          string
}

type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	debug   bool
	workDir string

	mu          sync.Mutex
	nextID      int64
	pending     map[int64]chan rpcResponse
	inFlight    int64
	stopCh      chan struct{}
	closed      bool
	started     bool
	event       EventHandler
	perm        PermissionHandler
	session     string
	convo       string
	forcedConvo string
	rollout     string
	shutdown    bool
	waitOnce    sync.Once
	waitErr     error
	waitCh      chan struct{}
}

type rpcResponse struct {
	result map[string]interface{}
	err    error
}

var (
	// ErrClientClosed is returned when an RPC cannot complete because the Codex
	// MCP process or client has been closed.
	ErrClientClosed = errors.New("codex client closed")
)

type rpcMessage struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id,omitempty"`
	Method  string                 `json:"method,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Result  map[string]interface{} `json:"result,omitempty"`
	Error   *rpcError              `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func NewClient(workDir string, debug bool) *Client {
	return &Client{
		debug:   debug,
		workDir: workDir,
		nextID:  1,
		pending: make(map[int64]chan rpcResponse),
		stopCh:  make(chan struct{}),
		waitCh:  make(chan struct{}),
	}
}

func (c *Client) SetEventHandler(handler EventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.event = handler
}

func (c *Client) SetPermissionHandler(handler PermissionHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.perm = handler
}

func (c *Client) Start() error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()

	mcpCommand := getCodexMcpCommand(c.debug)
	c.cmd = exec.Command("codex", mcpCommand)
	if c.workDir != "" {
		c.cmd.Dir = c.workDir
	}
	configureCodexMCPProcess(c.cmd)

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return err
	}
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr

	if err := c.cmd.Start(); err != nil {
		return err
	}

	go c.readLoop()
	go c.readStderr()

	if err := c.initialize(); err != nil {
		return err
	}

	return nil
}

func (c *Client) initialize() error {
	params := map[string]interface{}{
		"protocolVersion": defaultProtocolVersion,
		"capabilities": map[string]interface{}{
			"tools":       map[string]interface{}{},
			"elicitation": map[string]interface{}{},
		},
		"clientInfo": map[string]interface{}{
			"name":    "delight-codex-client",
			"version": version.Version(),
		},
	}

	_, err := c.call(context.Background(), "initialize", params, 10*time.Second)
	if err != nil {
		return err
	}

	return c.notify("initialized", map[string]interface{}{})
}

func (c *Client) StartSession(ctx context.Context, config SessionConfig) (map[string]interface{}, error) {
	c.mu.Lock()
	c.forcedConvo = ""
	c.mu.Unlock()

	args := map[string]interface{}{
		"prompt": config.Prompt,
	}
	if config.ApprovalPolicy != "" {
		args["approval-policy"] = config.ApprovalPolicy
	}
	if config.BaseInstructions != "" {
		args["base-instructions"] = config.BaseInstructions
	}
	if config.Config != nil {
		args["config"] = config.Config
	}
	if config.Cwd != "" {
		args["cwd"] = config.Cwd
	}
	if config.IncludePlanTool != nil {
		args["include-plan-tool"] = *config.IncludePlanTool
	}
	if config.Model != "" {
		args["model"] = config.Model
	}
	if config.Profile != "" {
		args["profile"] = config.Profile
	}
	if config.Sandbox != "" {
		args["sandbox"] = config.Sandbox
	}

	resp, err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      "codex",
		"arguments": args,
	}, 0)
	if err != nil {
		return nil, err
	}
	if err := toolCallError(resp); err != nil {
		return nil, err
	}
	c.extractIdentifiers(resp)
	return resp, nil
}

// ResumeConversation loads a persisted Codex conversation into the MCP server.
//
// The `codex-reply` tool-call can only continue conversations that are already
// present in the server's in-memory thread manager. For stored sessions (e.g.
// those resumed via `codex resume <id>`), callers must invoke resumeConversation
// first so the thread is available for subsequent codex-reply turns.
func (c *Client) ResumeConversation(ctx context.Context, conversationID string) (map[string]interface{}, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, errors.New("conversation id is required")
	}

	resp, err := c.call(ctx, "resumeConversation", map[string]interface{}{
		"conversationId": conversationID,
	}, 0)
	if err != nil {
		return nil, err
	}
	c.extractIdentifiers(resp)

	c.mu.Lock()
	if c.forcedConvo != "" && c.convo == c.forcedConvo {
		c.forcedConvo = ""
	}
	c.mu.Unlock()

	return resp, nil
}

func (c *Client) ContinueSession(ctx context.Context, prompt string) (map[string]interface{}, error) {
	c.mu.Lock()
	sessionID := c.session
	conversation := c.convo
	forced := c.forcedConvo
	if forced != "" {
		conversation = forced
	}
	c.mu.Unlock()
	if conversation == "" {
		return nil, errors.New("codex conversation not initialized")
	}

	args := map[string]interface{}{
		"conversationId": conversation,
		"prompt":         prompt,
	}
	// Some Codex MCP builds require sessionId, others accept conversationId only.
	// Include it when available but do not force it to match the conversation id.
	if sessionID != "" {
		args["sessionId"] = sessionID
	}

	resp, err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      "codex-reply",
		"arguments": args,
	}, 0)
	if err != nil {
		return nil, err
	}
	if err := toolCallError(resp); err != nil {
		return nil, err
	}
	c.extractIdentifiers(resp)
	c.mu.Lock()
	if c.forcedConvo != "" && c.convo == conversation {
		c.forcedConvo = ""
	}
	c.mu.Unlock()
	return resp, nil
}

// toolCallError returns an error when the Codex MCP server returns an isError
// tool response, surfacing the human-readable text message when available.
func toolCallError(resp map[string]interface{}) error {
	if resp == nil {
		return nil
	}
	isErr, ok := resp["isError"]
	if !ok {
		isErr = resp["is_error"]
	}
	if flag, ok := isErr.(bool); !ok || !flag {
		return nil
	}

	// Best effort: join any text blocks.
	var parts []string
	if raw, ok := resp["content"]; ok {
		if blocks, ok := raw.([]interface{}); ok {
			for _, block := range blocks {
				m, ok := block.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
		}
	}

	msg := strings.Join(parts, "\n")
	if strings.TrimSpace(msg) == "" {
		msg = "codex tool call failed"
	}
	return errors.New(msg)
}

// CancelInFlight requests that Codex interrupt the currently running tool call.
//
// Codex MCP supports the `notifications/cancelled` notification, keyed by the
// JSON-RPC request id. This is the preferred way to stop an in-flight turn
// without tearing down the MCP process (which helps preserve resume tokens and
// session continuity).
//
// This is best-effort: older Codex builds may ignore the notification.
func (c *Client) CancelInFlight(ctx context.Context, reason string) error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	requestID := c.inFlight
	closed := c.closed
	c.mu.Unlock()
	if closed || requestID == 0 {
		return nil
	}

	params := map[string]interface{}{
		"requestId": requestID,
	}
	if strings.TrimSpace(reason) != "" {
		params["reason"] = reason
	}

	cancelCtx := ctx
	if cancelCtx == nil {
		cancelCtx = context.Background()
	}

	// This is a notification: there is no response to wait for.
	// We still apply a short timeout so CancelInFlight does not block shutdown.
	cancelCtx, cancel := context.WithTimeout(cancelCtx, 500*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.notify("notifications/cancelled", params) }()

	select {
	case <-cancelCtx.Done():
		return cancelCtx.Err()
	case err := <-done:
		return err
	}
}

func (c *Client) ClearSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = ""
	c.convo = ""
	c.forcedConvo = ""
	c.rollout = ""
}

func (c *Client) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *Client) ConversationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.convo
}

// ResumeToken returns the best identifier for resuming a Codex session.
//
// Codex uses both "sessionId" and "conversationId" in different contexts:
//   - The MCP tool protocol typically returns a session id plus (sometimes) a
//     conversation id for follow-up turns.
//   - The `codex resume <id>` CLI expects a stable conversation/session id to
//     restore the local TUI.
//
// Prefer conversation id when available because it is stable across turns.
func (c *Client) ResumeToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.convo != "" {
		return c.convo
	}
	return c.session
}

// RolloutPath returns the current rollout JSONL path recorded for the active Codex session.
//
// When Codex is running as an MCP server, responses and events may include a `rolloutPath`
// field pointing at the append-only JSONL event log for the conversation. This path can be
// tailed to mirror activity in other UIs (e.g. a mobile viewer) without scraping the TUI.
func (c *Client) RolloutPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rollout
}

// SetResumeToken forces the client to treat resumeToken as the active Codex
// conversation identifier.
//
// This is used when switching into remote mode with a known resume token so the
// next prompt uses the `codex-reply` tool instead of starting a fresh session.
func (c *Client) SetResumeToken(resumeToken string) {
	if c == nil {
		return
	}
	resumeToken = strings.TrimSpace(resumeToken)
	if resumeToken == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Codex uses separate identifiers for:
	// - the MCP session (sessionId)
	// - the upstream conversation (conversationId)
	//
	// The value returned by `codex resume <id>` maps to the conversation id.
	// Do not overwrite the MCP session id, otherwise Codex may resume the wrong
	// conversation (often the last active one).
	c.forcedConvo = resumeToken
	c.convo = resumeToken
}

// Shutdown requests that the Codex MCP server process exit gracefully.
//
// This is best-effort because Codex builds vary in their MCP method support.
// When successful, it allows Codex to flush state and exit without leaving
// background processes running.
func (c *Client) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	if c.shutdown {
		c.mu.Unlock()
		return nil
	}
	c.shutdown = true
	c.mu.Unlock()

	shutdownCtx := ctx
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	shutdownCtx, cancel := context.WithTimeout(shutdownCtx, 2*time.Second)
	defer cancel()

	_, _ = c.call(shutdownCtx, "shutdown", map[string]interface{}{}, 0)
	_ = c.notify("exit", map[string]interface{}{})

	c.mu.Lock()
	stdin := c.stdin
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	pending := c.pending
	c.pending = make(map[int64]chan rpcResponse)
	close(c.stopCh)
	c.mu.Unlock()

	for _, ch := range pending {
		select {
		case ch <- rpcResponse{err: ErrClientClosed}:
		default:
		}
	}

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		stopCodexMCPProcess(c.cmd)
	}
	return nil
}

func (c *Client) Wait() error {
	c.waitOnce.Do(func() {
		if c.cmd == nil {
			c.waitErr = nil
			close(c.waitCh)
			return
		}
		c.waitErr = c.cmd.Wait()
		close(c.waitCh)
	})
	<-c.waitCh
	return c.waitErr
}

func (c *Client) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			if c.debug {
				logger.Debugf("codex: read error: %v", err)
			}
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			if c.debug {
				logger.Debugf("codex: invalid json: %s", string(line))
			}
			continue
		}

		if msg.Method != "" && msg.ID != nil {
			c.handleRequest(&msg)
			continue
		}
		if msg.Method != "" && msg.ID == nil {
			c.handleNotification(&msg)
			continue
		}
		if msg.ID != nil {
			c.handleResponse(&msg)
		}
	}
}

func (c *Client) readStderr() {
	if c.stderr == nil {
		return
	}
	reader := bufio.NewReader(c.stderr)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if c.debug {
			logger.Debugf("[codex] %s", strings.TrimSpace(line))
		}
	}
}

func (c *Client) handleNotification(msg *rpcMessage) {
	if msg.Method != "codex/event" {
		return
	}
	params := msg.Params
	raw, ok := params["msg"]
	if !ok {
		return
	}

	event, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	c.updateIdentifiersFromEvent(event)

	c.mu.Lock()
	handler := c.event
	c.mu.Unlock()
	if handler != nil {
		handler(event)
	}
}

func (c *Client) handleRequest(msg *rpcMessage) {
	method := msg.Method
	c.mu.Lock()
	debug := c.debug
	c.mu.Unlock()

	if debug {
		var keys []string
		for key := range msg.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		logger.Tracef("codex: request: id=%v method=%s paramsKeys=%v", msg.ID, method, keys)
	}
	switch method {
	case serverMethodElicitationRequest:
		c.handleElicitationApproval(msg)
		return
	case serverMethodElicitationCreate:
		c.handleElicitationApproval(msg)
		return
	case serverMethodExecCommandApproval:
		c.handleExecCommandApproval(msg)
		return
	case serverMethodApplyPatchApproval:
		c.handleApplyPatchApproval(msg)
		return
	default:
		if debug {
			logger.Debugf("codex: unsupported request method: %s", method)
		}
		_ = c.sendError(msg.ID, -32601, "method not supported")
		return
	}
}

// handleElicitationApproval handles the legacy MCP elicitation/request approval flow.
func (c *Client) handleElicitationApproval(msg *rpcMessage) {
	params := msg.Params
	if params == nil {
		params = map[string]interface{}{}
	}

	requestID := getStringParam(params, "codex_call_id", "codex_mcp_tool_call_id", "codex_event_id")
	if requestID == "" {
		requestID = fmt.Sprintf("codex-%v", msg.ID)
	}

	elicitation, _ := params["codex_elicitation"].(string)
	toolName := codexToolUnknownApproval
	input := map[string]interface{}{
		"elicitation": elicitation,
		"callId":      params["codex_call_id"],
		"eventId":     params["codex_event_id"],
		"toolCallId":  params["codex_mcp_tool_call_id"],
	}

	switch elicitation {
	case codexElicitationExecApproval:
		toolName = codexToolBash
		input["command"] = params["codex_command"]
		input["cwd"] = params["codex_cwd"]
		input["parsedCommand"] = params["codex_parsed_cmd"]
	case codexElicitationPatchApproval:
		toolName = codexToolApplyPatch
		input["changes"] = params["codex_changes"]
		input["reason"] = params["codex_reason"]
		input["grantRoot"] = params["codex_grant_root"]
	default:
		input["raw"] = params
	}

	c.mu.Lock()
	handler := c.perm
	debug := c.debug
	c.mu.Unlock()

	if debug {
		logger.Debugf("codex: approval request: id=%s method=%s tool=%s type=%s", requestID, msg.Method, toolName, elicitation)
	}

	decision := codexDecisionDenied
	message := ""
	if handler != nil {
		resp, err := handler(requestID, toolName, input)
		if err == nil && resp != nil {
			if resp.Decision != "" {
				switch resp.Decision {
				case codexDecisionAllow:
					decision = codexDecisionApproved
				case codexDecisionDeny:
					decision = codexDecisionDenied
				default:
					decision = resp.Decision
				}
			}
			message = resp.Message
		}
	}

	result := map[string]interface{}{
		"decision": decision,
	}
	if message != "" {
		result["reason"] = message
	}
	if debug {
		logger.Debugf("codex: approval decision: id=%s decision=%s", requestID, decision)
	}
	_ = c.sendResult(msg.ID, result)
}

// handleExecCommandApproval handles the modern Codex execCommandApproval request.
func (c *Client) handleExecCommandApproval(msg *rpcMessage) {
	params := msg.Params
	if params == nil {
		params = map[string]interface{}{}
	}

	callID := getStringParam(params, "callId", "call_id")
	if callID == "" {
		callID = fmt.Sprintf("codex-%v", msg.ID)
	}

	input := map[string]interface{}{
		"callId":         callID,
		"command":        params["command"],
		"cwd":            params["cwd"],
		"reason":         params["reason"],
		"parsedCmd":      params["parsedCmd"],
		"conversationId": params["conversationId"],
	}

	c.mu.Lock()
	handler := c.perm
	debug := c.debug
	c.mu.Unlock()

	if debug {
		logger.Debugf("codex: approval request: id=%s method=%s tool=%s", callID, serverMethodExecCommandApproval, codexToolBash)
	}

	decision := codexDecisionDeny
	message := ""
	if handler != nil {
		resp, err := handler(callID, codexToolBash, input)
		if err == nil && resp != nil {
			message = resp.Message
			switch resp.Decision {
			case codexDecisionApproved, codexDecisionAllow:
				decision = codexDecisionAllow
			case codexDecisionDenied, codexDecisionDeny, "":
				decision = codexDecisionDeny
			default:
				decision = resp.Decision
			}
		}
	}

	result := map[string]interface{}{
		"decision": decision,
	}
	if message != "" {
		result["reason"] = message
	}
	if debug {
		logger.Debugf("codex: approval decision: id=%s decision=%s", callID, decision)
	}
	_ = c.sendResult(msg.ID, result)
}

// handleApplyPatchApproval handles the modern Codex applyPatchApproval request.
func (c *Client) handleApplyPatchApproval(msg *rpcMessage) {
	params := msg.Params
	if params == nil {
		params = map[string]interface{}{}
	}

	callID := getStringParam(params, "callId", "call_id")
	if callID == "" {
		callID = fmt.Sprintf("codex-%v", msg.ID)
	}

	input := map[string]interface{}{
		"callId":         callID,
		"fileChanges":    params["fileChanges"],
		"reason":         params["reason"],
		"grantRoot":      params["grantRoot"],
		"conversationId": params["conversationId"],
	}

	c.mu.Lock()
	handler := c.perm
	debug := c.debug
	c.mu.Unlock()

	if debug {
		logger.Debugf("codex: approval request: id=%s method=%s tool=%s", callID, serverMethodApplyPatchApproval, codexToolApplyPatch)
	}

	decision := codexDecisionDeny
	message := ""
	if handler != nil {
		resp, err := handler(callID, codexToolApplyPatch, input)
		if err == nil && resp != nil {
			message = resp.Message
			switch resp.Decision {
			case codexDecisionApproved, codexDecisionAllow:
				decision = codexDecisionAllow
			case codexDecisionDenied, codexDecisionDeny, "":
				decision = codexDecisionDeny
			default:
				decision = resp.Decision
			}
		}
	}

	result := map[string]interface{}{
		"decision": decision,
	}
	if message != "" {
		result["reason"] = message
	}
	if debug {
		logger.Debugf("codex: approval decision: id=%s decision=%s", callID, decision)
	}
	_ = c.sendResult(msg.ID, result)
}

func (c *Client) handleResponse(msg *rpcMessage) {
	id, ok := coerceID(msg.ID)
	if !ok {
		return
	}

	c.mu.Lock()
	ch := c.pending[id]
	if ch != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ch == nil {
		return
	}

	if msg.Error != nil {
		ch <- rpcResponse{err: errors.New(msg.Error.Message)}
		return
	}
	ch <- rpcResponse{result: msg.Result}
}

func (c *Client) call(ctx context.Context, method string, params map[string]interface{}, timeout time.Duration) (map[string]interface{}, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	respCh := make(chan rpcResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	c.pending[id] = respCh
	c.inFlight = id
	c.mu.Unlock()

	msg := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.send(msg); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		if c.inFlight == id {
			c.inFlight = 0
		}
		c.mu.Unlock()
		return nil, err
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		// Remove the pending entry to avoid leaking response channels for
		// canceled requests.
		if ch := c.pending[id]; ch == respCh {
			delete(c.pending, id)
		}
		if c.inFlight == id {
			c.inFlight = 0
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-respCh:
		c.mu.Lock()
		if c.inFlight == id {
			c.inFlight = 0
		}
		c.mu.Unlock()
		return resp.result, resp.err
	}
}

func (c *Client) notify(method string, params map[string]interface{}) error {
	msg := rpcMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.send(msg)
}

func (c *Client) sendResult(id interface{}, result map[string]interface{}) error {
	msg := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	return c.send(msg)
}

func (c *Client) sendError(id interface{}, code int, message string) error {
	msg := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
	return c.send(msg)
}

func (c *Client) send(msg rpcMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		if c.closed {
			return ErrClientClosed
		}
		return errors.New("codex stdin not available")
	}
	enc, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if c.debug {
		logger.Tracef("codex -> %s", string(enc))
	}
	_, err = c.stdin.Write(append(enc, '\n'))
	return err
}

func (c *Client) extractIdentifiers(resp map[string]interface{}) {
	if resp == nil {
		return
	}
	if metaRaw, ok := resp["meta"]; ok {
		if meta, ok := metaRaw.(map[string]interface{}); ok {
			c.updateIdentifiersFromMap(meta)
		}
	}
	c.updateIdentifiersFromMap(resp)
}

func (c *Client) updateIdentifiersFromEvent(event map[string]interface{}) {
	c.updateIdentifiersFromMap(event)
	if dataRaw, ok := event["data"]; ok {
		if data, ok := dataRaw.(map[string]interface{}); ok {
			c.updateIdentifiersFromMap(data)
		}
	}
}

func (c *Client) updateIdentifiersFromMap(values map[string]interface{}) {
	sessionID := getStringParam(values, "sessionId", "session_id")
	conversationID := getStringParam(values, "conversationId", "conversation_id")
	rolloutPath := getStringParam(values, "rolloutPath", "rollout_path")

	if sessionID != "" {
		c.mu.Lock()
		c.session = sessionID
		c.mu.Unlock()
	}
	if conversationID != "" {
		c.mu.Lock()
		// If the client was explicitly primed with a resume token, ignore unrelated
		// background events that reference a different conversation (Codex can emit
		// metadata for the last active session on startup).
		if c.forcedConvo == "" || c.forcedConvo == conversationID {
			c.convo = conversationID
		}
		c.mu.Unlock()
	}
	if rolloutPath != "" {
		c.mu.Lock()
		c.rollout = rolloutPath
		c.mu.Unlock()
	}
}

func getStringParam(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := values[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}

func coerceID(id interface{}) (int64, bool) {
	switch val := id.(type) {
	case int64:
		return val, true
	case int:
		return int64(val), true
	case float64:
		return int64(val), true
	case json.Number:
		if v, err := val.Int64(); err == nil {
			return v, true
		}
	case string:
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

func getCodexMcpCommand(debug bool) string {
	out, err := exec.Command("codex", "--version").Output()
	if err != nil {
		if debug {
			logger.Debugf("codex: failed to detect version, defaulting to mcp-server: %v", err)
		}
		return "mcp-server"
	}
	version := strings.TrimSpace(string(out))
	re := regexp.MustCompile(`codex-cli\s+(\d+\.\d+\.\d+(?:-alpha\.\d+)?)`)
	match := re.FindStringSubmatch(version)
	if len(match) < 2 {
		return "mcp-server"
	}
	versionStr := match[1]
	parts := strings.FieldsFunc(versionStr, func(r rune) bool {
		return r == '.' || r == '-'
	})
	if len(parts) < 3 {
		return "mcp-server"
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])

	if major > 0 || minor > 43 {
		return "mcp-server"
	}
	if minor == 43 && patch == 0 {
		if strings.Contains(versionStr, "-alpha.") {
			alphaParts := strings.Split(versionStr, "-alpha.")
			if len(alphaParts) == 2 {
				alphaNum, _ := strconv.Atoi(alphaParts[1])
				if alphaNum >= 5 {
					return "mcp-server"
				}
				return "mcp"
			}
		}
		return "mcp-server"
	}
	return "mcp"
}
