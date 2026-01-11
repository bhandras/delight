package codexengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/codex/appserver"
	"github.com/bhandras/delight/cli/internal/version"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
)

const (
	// appServerApprovalPolicyNever disables interactive approvals in the app-server.
	appServerApprovalPolicyNever = "never"

	// appServerSummaryDetailed requests an assistant summary style supported by
	// codex-cli (notably, gpt-5.2-codex rejects "concise" and only supports
	// "detailed").
	appServerSummaryDetailed = "detailed"
)

// startRemoteAppServer starts Codex remote mode using `codex app-server`.
func (e *Engine) startRemoteAppServer(ctx context.Context, spec agentengine.EngineStartSpec) error {
	if e == nil {
		return fmt.Errorf("codex engine is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	resumeToken := strings.TrimSpace(spec.ResumeToken)

	model := strings.TrimSpace(spec.Config.Model)
	if model == "" {
		model = defaultRemoteModel
	}
	effort := strings.TrimSpace(spec.Config.ReasoningEffort)
	if effort == "" {
		effort = defaultRemoteReasoningEffort
	}
	permissionMode := normalizePermissionMode(spec.Config.PermissionMode)

	e.mu.Lock()
	e.remoteEnabled = true
	e.remotePermissionMode = permissionMode
	e.remoteModel = model
	e.remoteReasoningEffort = effort
	e.mu.Unlock()

	client := e.ensureAppServerClient(ctx)
	if client == nil {
		return fmt.Errorf("failed to start app-server client")
	}

	threadID, err := e.openAppServerThread(ctx, client, resumeToken, model, permissionMode)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.remoteThreadID = threadID
	e.remoteResumeToken = threadID
	e.remoteSessionActive = threadID != ""
	e.mu.Unlock()

	if strings.TrimSpace(threadID) != "" {
		e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeRemote, ResumeToken: threadID})
	}
	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeRemote})
	return nil
}

// ensureAppServerClient ensures the Engine has a running app-server client.
func (e *Engine) ensureAppServerClient(ctx context.Context) *appserver.Client {
	e.mu.Lock()
	existing := e.remoteAppServer
	debug := e.debug
	e.mu.Unlock()
	if existing != nil {
		return existing
	}

	client := appserver.NewClient(debug)
	client.SetNotificationHandler(func(method string, params json.RawMessage) {
		e.handleAppServerNotification(method, params)
	})
	client.SetRequestHandler(func(method string, params json.RawMessage) (json.RawMessage, *appserver.RPCError) {
		return e.handleAppServerRequest(ctx, method, params)
	})

	if err := client.Start(ctx, "delight", version.RichVersion()); err != nil {
		return nil
	}

	e.mu.Lock()
	if e.remoteAppServer == nil {
		e.remoteAppServer = client
	}
	existing = e.remoteAppServer
	e.mu.Unlock()
	if existing != client {
		_ = client.Close()
	}
	return existing
}

// openAppServerThread creates or resumes a thread and returns its id.
func (e *Engine) openAppServerThread(
	ctx context.Context,
	client *appserver.Client,
	resumeToken string,
	model string,
	permissionMode string,
) (string, error) {
	if client == nil {
		return "", fmt.Errorf("app-server client not initialized")
	}

	if strings.TrimSpace(resumeToken) != "" {
		raw, err := client.Call(ctx, appserver.MethodThreadResume, map[string]any{
			"threadId": resumeToken,
		})
		if err != nil {
			return "", err
		}
		threadID := parseThreadIDFromResult(raw)
		if threadID == "" {
			threadID = resumeToken
		}
		return threadID, nil
	}

	raw, err := client.Call(ctx, appserver.MethodThreadStart, map[string]any{
		"model":          model,
		"cwd":            e.workDir,
		"approvalPolicy": appServerApprovalPolicyNever,
		// thread/start expects kebab-case sandbox mode strings in codex-cli 0.80.0.
		"sandbox": sandboxNameKebabCase(permissionMode),
	})
	if err != nil && strings.Contains(err.Error(), "unknown variant") &&
		(strings.Contains(err.Error(), "workspace-write") || strings.Contains(err.Error(), "read-only") || strings.Contains(err.Error(), "danger-full-access")) {
		// Fall back to camelCase if the app-server schema expects it.
		raw, err = client.Call(ctx, appserver.MethodThreadStart, map[string]any{
			"model":          model,
			"cwd":            e.workDir,
			"approvalPolicy": appServerApprovalPolicyNever,
			"sandbox": sandboxName(permissionMode),
		})
	}
	if err != nil {
		return "", err
	}
	threadID := parseThreadIDFromResult(raw)
	if threadID == "" {
		return "", fmt.Errorf("thread/start returned empty thread id")
	}
	return threadID, nil
}

// startRemoteTurnViaAppServer starts a new turn via turn/start and returns immediately.
func (e *Engine) startRemoteTurnViaAppServer(ctx context.Context, msg agentengine.UserMessage) error {
	if e == nil {
		return fmt.Errorf("codex engine is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	e.mu.Lock()
	client := e.remoteAppServer
	threadID := strings.TrimSpace(e.remoteThreadID)
	enabled := e.remoteEnabled
	model := strings.TrimSpace(e.remoteModel)
	effort := strings.TrimSpace(e.remoteReasoningEffort)
	permissionMode := normalizePermissionMode(e.remotePermissionMode)
	busy := strings.TrimSpace(e.remoteActiveTurnID) != ""
	debug := e.debug
	if enabled && !busy {
		// Reserve the slot so concurrent sends fail fast even if the app-server is
		// slow to respond.
		e.remoteActiveTurnID = "pending"
	}
	e.mu.Unlock()

	if !enabled {
		return fmt.Errorf("codex remote mode not active")
	}
	if busy {
		return nil
	}
	if client == nil || threadID == "" {
		e.mu.Lock()
		if e.remoteActiveTurnID == "pending" {
			e.remoteActiveTurnID = ""
		}
		e.mu.Unlock()
		return fmt.Errorf("codex app-server not initialized")
	}

	if model == "" {
		model = defaultRemoteModel
	}
	if effort == "" {
		effort = defaultRemoteReasoningEffort
	}

	if debug {
		logger.Debugf("codex: turn/start thread=%s model=%s effort=%s", threadID, model, effort)
	}
	req := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{
				"type": "text",
				"text": msg.Text,
			},
		},
		"cwd":            e.workDir,
		"approvalPolicy": appServerApprovalPolicyNever,
		"sandboxPolicy":  sandboxPolicyObject(permissionMode, e.workDir),
		"model":          model,
		"effort":         effort,
		"summary":        appServerSummaryDetailed,
	}

	raw, err := client.Call(ctx, appserver.MethodTurnStart, req)
	if err != nil && strings.Contains(err.Error(), "unknown variant") &&
		(strings.Contains(err.Error(), "workspaceWrite") || strings.Contains(err.Error(), "readOnly") || strings.Contains(err.Error(), "dangerFullAccess")) {
		// The server likely expects kebab-case discriminants.
		req["sandboxPolicy"] = sandboxPolicyObjectKebabCase(permissionMode, e.workDir)
		raw, err = client.Call(ctx, appserver.MethodTurnStart, req)
	} else if err != nil && strings.Contains(err.Error(), "unknown variant") &&
		(strings.Contains(err.Error(), "workspace-write") || strings.Contains(err.Error(), "read-only") || strings.Contains(err.Error(), "danger-full-access")) {
		// The server likely expects camelCase discriminants (codex-cli 0.80.0).
		req["sandboxPolicy"] = sandboxPolicyObject(permissionMode, e.workDir)
		raw, err = client.Call(ctx, appserver.MethodTurnStart, req)
	}
	if err != nil {
		if debug {
			logger.Debugf("codex: turn/start failed: %v", err)
		}
		e.mu.Lock()
		if e.remoteActiveTurnID == "pending" {
			e.remoteActiveTurnID = ""
		}
		e.mu.Unlock()
		return err
	}
	if debug {
		logger.Debugf("codex: turn/start ok (bytes=%d)", len(raw))
	}

	turnID := parseTurnIDFromResult(raw)
	if turnID == "" {
		e.mu.Lock()
		if e.remoteActiveTurnID == "pending" {
			e.remoteActiveTurnID = ""
		}
		e.mu.Unlock()
		return fmt.Errorf("turn/start returned empty turn id")
	}

	nowMs := time.Now().UnixMilli()
	e.mu.Lock()
	e.remoteActiveTurnID = turnID
	e.startRemoteTurnLocked(turnID)
	e.mu.Unlock()
	e.setRemoteThinking(true, nowMs)
	return nil
}

// interruptRemoteTurn requests canceling the active turn via turn/interrupt.
func (e *Engine) interruptRemoteTurn(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	e.mu.Lock()
	client := e.remoteAppServer
	threadID := strings.TrimSpace(e.remoteThreadID)
	turnID := strings.TrimSpace(e.remoteActiveTurnID)
	e.mu.Unlock()

	if client == nil || threadID == "" || turnID == "" || turnID == "pending" {
		return nil
	}

	_, err := client.Call(ctx, appserver.MethodTurnInterrupt, map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	})
	return err
}

// handleAppServerNotification handles a server notification and emits engine events.
func (e *Engine) handleAppServerNotification(method string, params json.RawMessage) {
	e.mu.Lock()
	debug := e.debug
	e.mu.Unlock()
	if debug {
		logger.Debugf("codex: app-server notification %s", method)
	}
	switch method {
	case appserver.NotifyThreadStarted:
		e.handleThreadStarted(params)
	case appserver.NotifyTurnStarted:
		e.handleTurnStarted(params)
	case appserver.NotifyTurnCompleted:
		e.handleTurnCompleted(params)
	case appserver.NotifyItemStarted:
		e.handleItemStarted(params)
	case appserver.NotifyItemCompleted:
		e.handleItemCompleted(params)
	case appserver.NotifyItemAgentMessageDelta:
		e.handleAgentMessageDelta(params)
	case appserver.NotifyError:
		e.handleTurnError(params)
	default:
		if debug {
			logger.Debugf("codex: app-server ignored notification %s", method)
		}
	}
}

// handleAppServerRequest fails safe for unhandled server requests.
func (e *Engine) handleAppServerRequest(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *appserver.RPCError) {
	_ = ctx
	_ = params
	return nil, &appserver.RPCError{Code: -32601, Message: fmt.Sprintf("unsupported request: %s", method)}
}

// handleThreadStarted updates the engine's thread id from a thread/started notification.
func (e *Engine) handleThreadStarted(params json.RawMessage) {
	type payload struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	threadID := strings.TrimSpace(p.Thread.ID)
	if threadID == "" {
		return
	}

	e.mu.Lock()
	e.remoteThreadID = threadID
	e.remoteResumeToken = threadID
	e.remoteSessionActive = true
	e.mu.Unlock()
	e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeRemote, ResumeToken: threadID})
}

// handleTurnStarted sets busy/thinking state from turn/started.
func (e *Engine) handleTurnStarted(params json.RawMessage) {
	type payload struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	turnID := strings.TrimSpace(p.Turn.ID)
	if turnID == "" {
		return
	}

	nowMs := time.Now().UnixMilli()
	e.mu.Lock()
	e.remoteActiveTurnID = turnID
	e.startRemoteTurnLocked(turnID)
	e.mu.Unlock()
	e.setRemoteThinking(true, nowMs)
}

// handleTurnCompleted clears busy/thinking state from turn/completed.
func (e *Engine) handleTurnCompleted(params json.RawMessage) {
	type payload struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	turnID := strings.TrimSpace(p.Turn.ID)
	nowMs := time.Now().UnixMilli()

	e.mu.Lock()
	if turnID != "" && e.remoteActiveTurnID == turnID {
		e.remoteActiveTurnID = ""
	} else if turnID == "" {
		e.remoteActiveTurnID = ""
	}
	e.mu.Unlock()

	// Always clear thinking when the turn completes; clients rely on this as the
	// authoritative idle signal.
	e.setRemoteThinking(false, nowMs)
}

// handleItemStarted emits a best-effort UI event for tool-like items.
func (e *Engine) handleItemStarted(params json.RawMessage) {
	item := parseItemFromParams(params)
	if item == nil {
		return
	}

	itemID := strings.TrimSpace(stringValue(item["id"]))
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemID == "" || itemType == "" {
		return
	}

	if itemType == appserver.ItemTypeAgentMessage {
		e.mu.Lock()
		if e.remoteAgentMessageItems == nil {
			e.remoteAgentMessageItems = make(map[string]string)
		}
		e.remoteAgentMessageItems[itemID] = ""
		e.mu.Unlock()
		return
	}

	brief, full := renderToolItem(item)
	if brief == "" && full == "" {
		return
	}

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       itemID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseStart,
		Status:        agentengine.UIEventStatusRunning,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          time.Now().UnixMilli(),
	})
}

// handleAgentMessageDelta appends streamed text to an in-flight agentMessage item.
func (e *Engine) handleAgentMessageDelta(params json.RawMessage) {
	type payload struct {
		ItemID string `json:"itemId"`
		Delta  string `json:"delta"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	itemID := strings.TrimSpace(p.ItemID)
	if itemID == "" || p.Delta == "" {
		return
	}

	e.mu.Lock()
	if e.remoteAgentMessageItems == nil {
		e.remoteAgentMessageItems = make(map[string]string)
	}
	e.remoteAgentMessageItems[itemID] = e.remoteAgentMessageItems[itemID] + p.Delta
	e.mu.Unlock()
}

// handleItemCompleted emits final transcript/tool events for completed items.
func (e *Engine) handleItemCompleted(params json.RawMessage) {
	item := parseItemFromParams(params)
	if item == nil {
		return
	}

	itemID := strings.TrimSpace(stringValue(item["id"]))
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemID == "" || itemType == "" {
		return
	}

	if itemType == appserver.ItemTypeAgentMessage {
		text := ""
		e.mu.Lock()
		if e.remoteAgentMessageItems != nil {
			text = e.remoteAgentMessageItems[itemID]
			delete(e.remoteAgentMessageItems, itemID)
		}
		e.mu.Unlock()

		if strings.TrimSpace(text) == "" {
			text = stringValue(item["text"])
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		e.emitRemoteAssistantText(text)
		return
	}

	if itemType == appserver.ItemTypeReasoning {
		brief, full := renderReasoningItem(item)
		if brief == "" && full == "" {
			return
		}
		e.tryEmit(agentengine.EvUIEvent{
			Mode:          agentengine.ModeRemote,
			EventID:       itemID,
			Kind:          agentengine.UIEventReasoning,
			Phase:         agentengine.UIEventPhaseEnd,
			Status:        agentengine.UIEventStatusOK,
			BriefMarkdown: brief,
			FullMarkdown:  full,
			AtMs:          time.Now().UnixMilli(),
		})
		return
	}

	brief, full := renderToolItem(item)
	if brief == "" && full == "" {
		return
	}

	status := agentengine.UIEventStatusOK
	if s := strings.ToLower(strings.TrimSpace(stringValue(item["status"]))); s != "" {
		switch s {
		case "failed":
			status = agentengine.UIEventStatusError
		case "declined":
			status = agentengine.UIEventStatusCanceled
		}
	}

	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       itemID,
		Kind:          agentengine.UIEventTool,
		Phase:         agentengine.UIEventPhaseEnd,
		Status:        status,
		BriefMarkdown: brief,
		FullMarkdown:  full,
		AtMs:          time.Now().UnixMilli(),
	})
}

// handleTurnError emits an assistant-visible error message for error notifications.
func (e *Engine) handleTurnError(params json.RawMessage) {
	type payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	msg := strings.TrimSpace(p.Error.Message)
	if msg == "" {
		msg = "Codex app-server error"
	}
	e.emitRemoteAssistantError(msg)
}

// parseThreadIDFromResult extracts a thread.id value from a request result payload.
func parseThreadIDFromResult(raw json.RawMessage) string {
	type payload struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	return strings.TrimSpace(p.Thread.ID)
}

// parseTurnIDFromResult extracts a turn.id value from a request result payload.
func parseTurnIDFromResult(raw json.RawMessage) string {
	type payload struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	return strings.TrimSpace(p.Turn.ID)
}

// parseItemFromParams extracts the `item` object from an item/* notification payload.
func parseItemFromParams(params json.RawMessage) map[string]any {
	type payload struct {
		Item map[string]any `json:"item"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	return p.Item
}

// renderToolItem returns brief/full markdown strings for a tool-like item.
func renderToolItem(item map[string]any) (string, string) {
	itemType := strings.TrimSpace(stringValue(item["type"]))
	switch itemType {
	case appserver.ItemTypeCommandExecution:
		cmd := strings.TrimSpace(stringValue(item["command"]))
		if cmd == "" {
			return "", ""
		}
		out := strings.TrimSpace(stringValue(item["aggregatedOutput"]))
		cmdBlock := "    " + strings.ReplaceAll(cmd, "\n", "\n    ")
		brief := fmt.Sprintf("Tool: shell\n\n%s", cmdBlock)
		full := brief
		if out != "" {
			full = fmt.Sprintf("%s\n\nOutput:\n\n```\n%s\n```", full, truncateText(out, localToolOutputMaxChars))
		}
		return brief, full
	case appserver.ItemTypeMCPToolCall:
		server := strings.TrimSpace(stringValue(item["server"]))
		tool := strings.TrimSpace(stringValue(item["tool"]))
		brief := "Tool: mcp"
		if server != "" && tool != "" {
			brief = fmt.Sprintf("Tool: `%s/%s`", server, tool)
		}
		full := "Tool: mcp"
		if server != "" || tool != "" {
			full = fmt.Sprintf("Tool: mcp\n\nServer: `%s`\nTool: `%s`", server, tool)
		}
		return brief, full
	case appserver.ItemTypeFileChange:
		return renderFileChangeItem(item)
	default:
		// Ignore reasoning and unknown items for now to keep the mobile transcript readable.
		return "", ""
	}
}

// renderFileChangeItem renders a fileChange item into brief/full Markdown.
func renderFileChangeItem(item map[string]any) (string, string) {
	changes, _ := item["changes"].([]any)
	brief := "Patch"
	if len(changes) == 1 {
		brief = "Patch: 1 file"
	} else if len(changes) > 1 {
		brief = fmt.Sprintf("Patch: %d files", len(changes))
	}

	full := "Patch"
	if len(changes) == 0 {
		return brief, full
	}

	var b strings.Builder
	b.WriteString("Patch\n\n")
	for _, raw := range changes {
		change, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := strings.TrimSpace(stringValue(change["path"]))
		kindType := ""
		if kindObj, ok := change["kind"].(map[string]any); ok {
			kindType = strings.TrimSpace(stringValue(kindObj["type"]))
		}
		diff := stringValue(change["diff"])

		line := "- "
		if kindType != "" {
			line += kindType + " "
		}
		if path != "" {
			line += fmt.Sprintf("`%s`", path)
		} else {
			line += "file"
		}
		b.WriteString(line)
		b.WriteString("\n")

		diff = strings.TrimRight(diff, "\n")
		if diff == "" {
			continue
		}
		b.WriteString("\n```diff\n")
		b.WriteString(truncateText(diff, localToolOutputMaxChars))
		b.WriteString("\n```\n\n")
	}
	full = strings.TrimSpace(b.String())
	return brief, full
}

// renderReasoningItem renders a reasoning item into brief/full Markdown.
func renderReasoningItem(item map[string]any) (string, string) {
	var parts []string
	if summary, ok := item["summary"].([]any); ok {
		for _, entry := range summary {
			s, ok := entry.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "", ""
	}

	var content []string
	for _, part := range parts {
		head := strings.TrimLeft(part, "#")
		head = strings.TrimSpace(head)
		if head == "Reasoning" {
			continue
		}
		content = append(content, part)
	}
	if len(content) == 0 {
		return "", ""
	}

	brief := truncateOneLine(content[0], 160)
	full := "Reasoning\n\n" + strings.Join(content, "\n")
	return brief, strings.TrimSpace(full)
}

// sandboxName maps Delight permission mode strings to app-server sandbox modes.
//
// codex-cli 0.80.0 expects camelCase discriminants (workspaceWrite/readOnly/
// dangerFullAccess). Some newer app-server schemas use kebab-case; for that we
// fall back to sandboxNameKebabCase when we detect a schema mismatch.
func sandboxName(permissionMode string) string {
	switch normalizePermissionMode(permissionMode) {
	case "read-only":
		return "readOnly"
	case "yolo":
		return "dangerFullAccess"
	default:
		return "workspaceWrite"
	}
}

// sandboxNameKebabCase returns kebab-case sandbox discriminants expected by some
// older/newer app-server schema variants.
func sandboxNameKebabCase(permissionMode string) string {
	switch normalizePermissionMode(permissionMode) {
	case "read-only":
		return "read-only"
	case "yolo":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

// sandboxPolicyObject builds a sandboxPolicy payload for turn/start.
func sandboxPolicyObject(permissionMode string, workDir string) map[string]any {
	switch sandboxName(permissionMode) {
	case "readOnly":
		return map[string]any{"type": "readOnly"}
	case "dangerFullAccess":
		return map[string]any{"type": "dangerFullAccess"}
	default:
		// Default to workspaceWrite rooted at the current workDir.
		return map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": []string{workDir},
			"networkAccess": true,
		}
	}
}

// sandboxPolicyObjectKebabCase builds a sandboxPolicy payload that uses
// kebab-case discriminants.
func sandboxPolicyObjectKebabCase(permissionMode string, workDir string) map[string]any {
	switch sandboxNameKebabCase(permissionMode) {
	case "read-only":
		return map[string]any{"type": "read-only"}
	case "danger-full-access":
		return map[string]any{"type": "danger-full-access"}
	default:
		return map[string]any{
			"type":          "workspace-write",
			"writableRoots": []string{workDir},
			"networkAccess": true,
		}
	}
}

// stringValue extracts a string from an arbitrary decoded JSON value.
func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// emitRemoteAssistantText emits a single assistant message record for remote mode.
func (e *Engine) emitRemoteAssistantText(text string) {
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

// truncateOneLine truncates a string to a single line of maxRunes length.
func truncateOneLine(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes-1]) + "…"
}
