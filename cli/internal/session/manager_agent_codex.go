package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
)

func (m *Manager) startCodex() error {
	client := codex.NewClient(m.workDir, m.debug)
	client.SetEventHandler(m.handleCodexEvent)
	client.SetPermissionHandler(m.handleCodexPermission)
	if err := client.Start(); err != nil {
		return fmt.Errorf("failed to start codex: %w", err)
	}
	m.codexClient = client

	m.modeMu.Lock()
	m.mode = ModeRemote
	m.modeMu.Unlock()
	m.stateMu.Lock()
	m.state.ControlledByUser = false
	m.stateMu.Unlock()
	m.requestPersistAgentState()

	go m.runCodexLoop()

	log.Println("Codex MCP session ready")
	return nil
}

func (m *Manager) runCodexLoop() {
	for {
		select {
		case <-m.stopCh:
			return
		case <-m.codexStop:
			return
		case msg := <-m.codexQueue:
			if m.codexClient == nil {
				continue
			}
			permissionMode, model, modeChanged := m.resolveCodexMode(msg.meta)
			if modeChanged {
				m.codexClient.ClearSession()
				m.codexSessionActive = false
			}

			cfg := codex.SessionConfig{
				Prompt:         msg.text,
				ApprovalPolicy: codexApprovalPolicy(permissionMode),
				Sandbox:        codexSandbox(permissionMode),
				Cwd:            m.workDir,
				Model:          model,
			}

			ctx := context.Background()
			if !m.codexSessionActive {
				if _, err := m.codexClient.StartSession(ctx, cfg); err != nil {
					if m.debug {
						log.Printf("Codex start error: %v", err)
					}
					m.codexSessionActive = false
					continue
				}
				m.codexSessionActive = true
				m.codexPermissionMode = permissionMode
				m.codexModel = model
				continue
			}

			if _, err := m.codexClient.ContinueSession(ctx, msg.text); err != nil {
				if m.debug {
					log.Printf("Codex continue error: %v", err)
				}
				m.codexSessionActive = false
			}
		}
	}
}

func (m *Manager) resolveCodexMode(meta map[string]interface{}) (string, string, bool) {
	permissionMode := m.codexPermissionMode
	model := m.codexModel
	changed := false

	if permissionMode == "" {
		permissionMode = "default"
	}

	if meta != nil {
		if raw, ok := meta["permissionMode"]; ok {
			if value, ok := raw.(string); ok && value != "" {
				if value != permissionMode {
					permissionMode = value
					changed = true
				}
			}
		}

		if raw, ok := meta["model"]; ok {
			if raw == nil {
				if model != "" {
					model = ""
					changed = true
				}
			} else if value, ok := raw.(string); ok {
				if value != model {
					model = value
					changed = true
				}
			}
		}
	}

	return permissionMode, model, changed
}

func codexApprovalPolicy(permissionMode string) string {
	switch permissionMode {
	case "read-only":
		return "never"
	case "safe-yolo", "yolo":
		return "on-failure"
	default:
		return "untrusted"
	}
}

func codexSandbox(permissionMode string) string {
	switch permissionMode {
	case "read-only":
		return "read-only"
	case "yolo":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

func (m *Manager) handleCodexEvent(event map[string]interface{}) {
	if event == nil {
		return
	}

	// Detach from caller's map to avoid races and make inbound processing deterministic.
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	var copyEvent map[string]interface{}
	if err := json.Unmarshal(payload, &copyEvent); err != nil {
		return
	}

	_ = m.enqueueInbound(func() { m.handleCodexEventInbound(copyEvent) })
}

func (m *Manager) handleCodexEventInbound(event map[string]interface{}) {
	if event == nil {
		return
	}

	evtType, _ := event["type"].(string)
	switch evtType {
	case "task_started":
		m.setThinking(true)
	case "task_complete", "turn_aborted":
		m.setThinking(false)
	}

	switch evtType {
	case "agent_message":
		message, _ := event["message"].(string)
		if message == "" {
			if raw, ok := event["message"]; ok {
				message = fmt.Sprint(raw)
			}
		}
		if message == "" {
			return
		}
		m.sendCodexRecord(wire.CodexRecord{
			Type:    "message",
			Message: message,
		})
	case "agent_reasoning":
		text, _ := event["text"].(string)
		if text == "" {
			text, _ = event["message"].(string)
		}
		if text == "" {
			if raw, ok := event["text"]; ok {
				text = fmt.Sprint(raw)
			}
		}
		if text == "" {
			return
		}
		m.sendCodexRecord(wire.CodexRecord{
			Type:    "reasoning",
			Message: text,
		})
	case "exec_command_begin", "exec_approval_request":
		callID := extractString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		input := filterMap(event, "type", "call_id", "callId")
		m.sendCodexRecord(wire.CodexRecord{
			Type:   "tool-call",
			CallID: callID,
			Name:   "CodexBash",
			Input:  input,
			ID:     types.NewCUID(),
		})
	case "exec_command_end":
		callID := extractString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		output := filterMap(event, "type", "call_id", "callId")
		m.sendCodexRecord(wire.CodexRecord{
			Type:   "tool-call-result",
			CallID: callID,
			Output: output,
			ID:     types.NewCUID(),
		})
	}
}

func (m *Manager) handleCodexPermission(requestID string, toolName string, input map[string]interface{}) (*codex.PermissionDecision, error) {
	// IMPORTANT: do not run this on the inbound queue.
	//
	// Permission requests block waiting for a mobile response. If we block the inbound
	// queue, we can deadlock because permission responses are delivered via queued
	// inbound RPC handlers.
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	response, err := m.handlePermissionRequest(requestID, toolName, payload)
	if err != nil {
		return &codex.PermissionDecision{
			Decision: "denied",
			Message:  err.Error(),
		}, nil
	}

	decision := "denied"
	if response != nil && response.Behavior == "allow" {
		decision = "approved"
	}
	message := ""
	if response != nil {
		message = response.Message
	}
	return &codex.PermissionDecision{
		Decision: decision,
		Message:  message,
	}, nil
}

func (m *Manager) sendCodexRecord(data any) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	payload := wire.AgentCodexRecord{
		Role: "agent",
		Content: wire.AgentCodexContent{
			Type: "codex",
			Data: data,
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("Codex marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(raw)
	if err != nil {
		if m.debug {
			log.Printf("Codex encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(wire.OutboundMessagePayload{
		SID:     m.sessionID,
		Message: encrypted,
	})
}

func extractString(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := data[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}

func filterMap(data map[string]interface{}, excludeKeys ...string) map[string]interface{} {
	if data == nil {
		return nil
	}
	exclude := map[string]struct{}{}
	for _, key := range excludeKeys {
		exclude[key] = struct{}{}
	}
	out := make(map[string]interface{}, len(data))
	for key, val := range data {
		if _, ok := exclude[key]; ok {
			continue
		}
		out[key] = val
	}
	return out
}
