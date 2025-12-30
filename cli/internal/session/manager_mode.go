package session

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
)

type remoteInputRecord struct {
	text string
	at   time.Time
}

func normalizeRemoteInputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	return text
}

func (m *Manager) rememberRemoteInput(text string) {
	text = normalizeRemoteInputText(text)
	if text == "" {
		return
	}

	now := time.Now()

	m.recentRemoteInputsMu.Lock()
	defer m.recentRemoteInputsMu.Unlock()

	// Keep a small, time-bounded list; most sessions only need a handful of recent inputs.
	const maxItems = 64
	const ttl = 20 * time.Second

	// Drop expired entries.
	cutoff := now.Add(-ttl)
	dst := m.recentRemoteInputs[:0]
	for _, rec := range m.recentRemoteInputs {
		if rec.at.After(cutoff) {
			dst = append(dst, rec)
		}
	}
	m.recentRemoteInputs = dst

	m.recentRemoteInputs = append(m.recentRemoteInputs, remoteInputRecord{text: text, at: now})
	if len(m.recentRemoteInputs) > maxItems {
		m.recentRemoteInputs = m.recentRemoteInputs[len(m.recentRemoteInputs)-maxItems:]
	}
}

func (m *Manager) isRecentlyInjectedRemoteInput(text string) bool {
	text = normalizeRemoteInputText(text)
	if text == "" {
		return false
	}

	now := time.Now()
	const ttl = 20 * time.Second
	cutoff := now.Add(-ttl)

	m.recentRemoteInputsMu.Lock()
	defer m.recentRemoteInputsMu.Unlock()

	// Iterate newest-first.
	for i := len(m.recentRemoteInputs) - 1; i >= 0; i-- {
		rec := m.recentRemoteInputs[i]
		if rec.at.Before(cutoff) {
			break
		}
		if rec.text == text {
			return true
		}
	}
	return false
}

// GetMode returns the current operation mode
func (m *Manager) GetMode() Mode {
	m.modeMu.RLock()
	defer m.modeMu.RUnlock()
	return m.mode
}

// SwitchToRemote switches to remote mode (mobile app control)
func (m *Manager) SwitchToRemote() error {
	m.modeMu.Lock()
	if m.mode == ModeRemote {
		m.modeMu.Unlock()
		return nil
	}
	m.mode = ModeRemote
	m.modeMu.Unlock()

	log.Println("Switching to remote mode...")

	// Kill the interactive Claude process
	if m.claudeProcess != nil {
		m.claudeProcess.Kill()
		m.claudeProcess = nil
	}

	// Stop session scanner
	if m.sessionScanner != nil {
		m.sessionScanner.Stop()
		m.sessionScanner = nil
	}

	// Start remote bridge
	bridge, err := claude.NewRemoteBridge(m.workDir, m.claudeSessionID, m.debug)
	if err != nil {
		m.modeMu.Lock()
		m.mode = ModeLocal
		m.modeMu.Unlock()
		return fmt.Errorf("failed to create remote bridge: %w", err)
	}

	// Set up message handler
	bridge.SetMessageHandler(m.handleRemoteMessage)

	// Set up permission handler
	bridge.SetPermissionHandler(m.handleRemotePermission)

	if err := bridge.Start(); err != nil {
		m.modeMu.Lock()
		m.mode = ModeLocal
		m.modeMu.Unlock()
		return fmt.Errorf("failed to start remote bridge: %w", err)
	}

	m.remoteBridge = bridge

	// Update state to show we're in remote mode
	m.state.ControlledByUser = false
	m.updateState()

	log.Println("Remote mode active")
	return nil
}

func (m *Manager) handleRemotePermission(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	// IMPORTANT: do not run this on the inbound queue.
	//
	// Permission requests block waiting for a mobile response. If we block the inbound
	// queue, we can deadlock because permission responses are delivered via queued
	// inbound RPC handlers.
	if input == nil {
		input = json.RawMessage("null")
	} else {
		// Detach from caller-owned bytes to avoid concurrent mutation.
		input = append(json.RawMessage(nil), input...)
	}
	return m.handlePermissionRequest(requestID, toolName, input)
}

// SwitchToLocal switches to local mode (terminal control)
func (m *Manager) SwitchToLocal() error {
	m.modeMu.Lock()
	if m.mode == ModeLocal {
		m.modeMu.Unlock()
		return nil
	}
	m.mode = ModeLocal
	m.modeMu.Unlock()

	log.Println("Switching to local mode...")

	// Kill the remote bridge
	if m.remoteBridge != nil {
		m.remoteBridge.Kill()
		m.remoteBridge = nil
	}

	// Start Claude process with fd 3 tracking
	claudeProc, err := claude.NewProcess(m.workDir, m.debug)
	if err != nil {
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		return fmt.Errorf("failed to create claude process: %w", err)
	}

	m.claudeProcess = claudeProc

	if err := claudeProc.Start(); err != nil {
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		return fmt.Errorf("failed to start claude: %w", err)
	}

	// Start session ID detection handler
	go m.handleSessionIDDetection()

	// Start thinking state handler
	go m.handleThinkingState()

	// Update state to show we're in local mode
	m.state.ControlledByUser = true
	m.updateState()

	log.Println("Local mode active")
	return nil
}

// SendUserMessage sends a user message to Claude (remote mode only)
func (m *Manager) SendUserMessage(content string, meta map[string]interface{}) error {
	m.modeMu.RLock()
	mode := m.mode
	bridge := m.remoteBridge
	m.modeMu.RUnlock()

	if mode != ModeRemote {
		return fmt.Errorf("not in remote mode")
	}

	if bridge == nil {
		return fmt.Errorf("remote bridge not running")
	}

	return bridge.SendUserMessage(content, meta)
}

// AbortRemote aborts the current remote query
func (m *Manager) AbortRemote() error {
	m.modeMu.RLock()
	bridge := m.remoteBridge
	m.modeMu.RUnlock()

	if bridge == nil {
		return fmt.Errorf("remote bridge not running")
	}

	return bridge.Abort()
}

// handleRemoteMessage processes messages from the remote bridge
func (m *Manager) handleRemoteMessage(msg *claude.RemoteMessage) error {
	if msg == nil {
		return nil
	}

	// Detach from caller-owned struct/slices to avoid concurrent mutation issues.
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	var copyMsg claude.RemoteMessage
	if err := json.Unmarshal(payload, &copyMsg); err != nil {
		return err
	}

	return m.runInboundErr(func() error { return m.handleRemoteMessageInbound(&copyMsg) })
}

func (m *Manager) handleRemoteMessageInbound(msg *claude.RemoteMessage) error {
	if msg == nil {
		return nil
	}

	if m.debug {
		log.Printf("Remote message: type=%s", msg.Type)
	}

	// Track thinking state from assistant messages
	if msg.Type == "assistant" {
		m.setThinking(true)
	} else if msg.Type == "result" {
		m.setThinking(false)
	}

	// Track session ID from system init
	if msg.Type == "system" && msg.SessionID != "" {
		m.claudeSessionID = msg.SessionID
	}

	m.renderRemoteMessage(msg)

	// Forward to server
	if m.wsClient != nil && m.wsClient.IsConnected() {
		payload := m.buildRawRecordFromRemote(msg)
		if payload == nil {
			return nil
		}

		data, err := json.Marshal(payload)
		if err != nil {
			if m.debug {
				log.Printf("Failed to marshal remote payload: %v", err)
			}
			return nil
		}

		encrypted, err := m.encrypt(data)
		if err != nil {
			if m.debug {
				log.Printf("Failed to encrypt remote message: %v", err)
			}
			return nil
		}

		m.wsClient.EmitMessage(wire.OutboundMessagePayload{
			SID:     m.sessionID,
			Message: encrypted,
		})
	}

	// Send usage report if present
	if len(msg.Usage) > 0 && m.wsClient != nil && m.wsClient.IsConnected() {
		var usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		}
		if err := json.Unmarshal(msg.Usage, &usage); err == nil {
			total := usage.InputTokens + usage.OutputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
			_ = m.wsClient.EmitRaw("usage-report", wire.UsageReportPayload{
				Key:       "claude-session",
				SessionID: m.sessionID,
				Tokens: wire.UsageReportTokens{
					Total:         total,
					Input:         usage.InputTokens,
					Output:        usage.OutputTokens,
					CacheCreation: usage.CacheCreationInputTokens,
					CacheRead:     usage.CacheReadInputTokens,
				},
				Cost: wire.UsageReportCost{
					Total:  0,
					Input:  0,
					Output: 0,
				},
			})
		}
	}

	return nil
}

func (m *Manager) renderRemoteMessage(msg *claude.RemoteMessage) {
	if m.GetMode() != ModeRemote {
		return
	}

	switch msg.Type {
	case "raw":
		if len(msg.Message) == 0 {
			return
		}
		printRawRecord(msg.Message)
	case "error":
		if msg.Error != "" {
			fmt.Fprintf(os.Stdout, "claude error: %s\n", msg.Error)
		}
	}
}

func printRawRecord(raw json.RawMessage) {
	rec, ok, err := wire.TryParseAgentOutputRecord([]byte(raw))
	if err != nil || !ok || rec == nil {
		return
	}
	msgRole := rec.Content.Data.Message.Role
	blocks := rec.Content.Data.Message.Content
	if msgRole == "" || len(blocks) == 0 {
		return
	}

	prefix := "you> "
	if msgRole == "assistant" {
		prefix = "claude> "
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				fmt.Fprintln(os.Stdout, prefix+block.Text)
			}
		case "tool_use":
			name, _ := block.Fields["name"].(string)
			id, _ := block.Fields["id"].(string)
			summary := formatToolInput(block.Fields["input"])
			line := prefix + "[tool] " + name
			if id != "" {
				line += " " + id
			}
			if summary != "" {
				line += " - " + summary
			}
			fmt.Fprintln(os.Stdout, line)
		case "tool_result":
			summary := formatToolResult(block.Fields["content"])
			line := prefix + "[tool_result]"
			if summary != "" {
				line += " " + summary
			}
			fmt.Fprintln(os.Stdout, line)
		}
	}
}

func formatToolInput(input any) string {
	if input == nil {
		return ""
	}
	if inputMap, ok := input.(map[string]any); ok {
		if cmd, _ := inputMap["command"].(string); cmd != "" {
			return cmd
		}
		if query, _ := inputMap["query"].(string); query != "" {
			return query
		}
		if url, _ := inputMap["url"].(string); url != "" {
			return url
		}
	}
	blob, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	s := string(blob)
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func formatToolResult(content any) string {
	switch v := content.(type) {
	case string:
		if len(v) > 160 {
			return v[:160] + "..."
		}
		return v
	case []any:
		if len(v) == 0 {
			return ""
		}
		if text, ok := v[0].(map[string]any); ok {
			if t, _ := text["type"].(string); t == "text" {
				if val, _ := text["text"].(string); val != "" {
					if len(val) > 160 {
						return val[:160] + "..."
					}
					return val
				}
			}
		}
	}
	return ""
}

// buildRawRecordFromRemote converts bridge RemoteMessage into the raw record
// format expected by the mobile app (matches RawRecordSchema on client).
func (m *Manager) buildRawRecordFromRemote(msg *claude.RemoteMessage) any {
	type rawRecordProbe struct {
		Role    string `json:"role"`
		Content struct {
			Type string `json:"type"`
		} `json:"content"`
	}

	type legacySDKMessage struct {
		Type    string              `json:"type"`
		Role    string              `json:"role"`
		Model   string              `json:"model,omitempty"`
		Content []wire.ContentBlock `json:"content"`
		Usage   any                 `json:"usage,omitempty"`
		ID      string              `json:"id,omitempty"`
	}

	// If bridge already sent structured payload, prefer that
	if len(msg.Message) > 0 {
		raw := json.RawMessage(msg.Message)

		var legacy legacySDKMessage
		if err := json.Unmarshal(raw, &legacy); err == nil &&
			legacy.Type == "message" &&
			(legacy.Role == "assistant" || legacy.Role == "user") &&
			len(legacy.Content) > 0 {
			model := legacy.Model
			if legacy.Role == "assistant" && model == "" {
				model = "unknown"
			}

			outType := legacy.Role
			uuid := legacy.ID
			if uuid == "" {
				uuid = types.NewCUID()
			}

			return wire.AgentOutputRecord{
				Role: "agent",
				Content: wire.AgentOutputContent{
					Type: "output",
					Data: wire.AgentOutputData{
						Type:             outType,
						IsSidechain:      false,
						IsCompactSummary: false,
						IsMeta:           false,
						UUID:             uuid,
						ParentUUID:       nil,
						Message: wire.AgentMessage{
							Role:    legacy.Role,
							Model:   model,
							Content: legacy.Content,
							Usage:   legacy.Usage,
						},
					},
				},
			}
		}

		var probe rawRecordProbe
		if err := json.Unmarshal(raw, &probe); err == nil &&
			(probe.Role == "agent" || probe.Role == "user") &&
			probe.Content.Type != "" {
			return raw
		}

		if msg.Type == "raw" {
			return nil
		}
	}

	switch msg.Type {
	case "message":
		role := msg.Role
		contentBlocks, err := wire.DecodeContentBlocks(msg.Content)
		if err != nil {
			return nil
		}
		if role == "" || len(contentBlocks) == 0 {
			return nil
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}

		if role == "assistant" {
			message := wire.AgentMessage{
				Role:    "assistant",
				Model:   model,
				Content: contentBlocks,
			}
			if len(msg.Usage) > 0 {
				var usage interface{}
				if err := json.Unmarshal(msg.Usage, &usage); err == nil && usage != nil {
					message.Usage = usage
				}
			}

			data := wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             types.NewCUID(),
				ParentUUID:       nil,
				Message:          message,
			}
			if msg.ParentToolUseID != "" {
				data.ParentToolUseID = msg.ParentToolUseID
			}

			return wire.AgentOutputRecord{
				Role:    "agent",
				Content: wire.AgentOutputContent{Type: "output", Data: data},
			}
		}

		if role == "user" {
			return wire.AgentOutputRecord{
				Role: "agent",
				Content: wire.AgentOutputContent{
					Type: "output",
					Data: wire.AgentOutputData{
						Type:             "user",
						IsSidechain:      false,
						IsCompactSummary: false,
						IsMeta:           false,
						UUID:             types.NewCUID(),
						ParentUUID:       nil,
						Message: wire.AgentMessage{
							Role:    "user",
							Content: contentBlocks,
						},
					},
				},
			}
		}

		return nil
	case "result":
		// Ignore result events to avoid duplicate assistant messages.
		return nil
	case "assistant":
		// Build minimal assistant message with a single text chunk
		uuid := types.NewCUID()

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
		if text == "" {
			if msg.Result != "" {
				text = msg.Result
			} else {
				return nil
			}
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}

		message := wire.AgentMessage{
			Role:  "assistant",
			Model: model,
			Content: []wire.ContentBlock{
				{Type: "text", Text: text},
			},
		}
		if len(msg.Usage) > 0 {
			var usage interface{}
			if err := json.Unmarshal(msg.Usage, &usage); err == nil && usage != nil {
				message.Usage = usage
			}
		}

		return wire.AgentOutputRecord{
			Role: "agent",
			Content: wire.AgentOutputContent{
				Type: "output",
				Data: wire.AgentOutputData{
					Type:             "assistant",
					IsSidechain:      false,
					IsCompactSummary: false,
					IsMeta:           false,
					UUID:             uuid,
					ParentUUID:       nil,
					Message:          message,
				},
			},
		}
	case "user":
		switch v := msg.Content.(type) {
		case nil:
			return nil
		case string:
			if v == "" {
				return nil
			}
		default:
			if fmt.Sprint(v) == "" {
				return nil
			}
		}
		return wire.UserTextRecord{
			Role: "user",
			Content: struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
				Type: "text",
				Text: fmt.Sprint(msg.Content),
			},
		}
	default:
		// Ignore unsupported types (system, control, etc.)
		return nil
	}
}

func (m *Manager) sendFakeAgentResponse(userText string) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	reply := fmt.Sprintf("fake-agent: %s", userText)
	uuid := types.NewCUID()

	payload := wire.AgentOutputRecord{
		Role: "agent",
		Content: wire.AgentOutputContent{
			Type: "output",
			Data: wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             uuid,
				ParentUUID:       nil,
				Message: wire.AgentMessage{
					Role:  "assistant",
					Model: "fake-agent",
					Content: []wire.ContentBlock{
						{Type: "text", Text: reply},
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("Fake agent marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		if m.debug {
			log.Printf("Fake agent encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(wire.OutboundMessagePayload{
		SID:     m.sessionID,
		Message: encrypted,
	})
}
