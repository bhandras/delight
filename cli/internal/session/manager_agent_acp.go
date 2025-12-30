package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/bhandras/delight/cli/internal/acp"
	"github.com/bhandras/delight/cli/internal/protocol/wire"
	"github.com/bhandras/delight/cli/pkg/types"
)

func (m *Manager) startACP() error {
	if m.cfg == nil {
		return fmt.Errorf("missing config")
	}
	if m.cfg.ACPURL == "" || m.acpAgent == "" {
		return fmt.Errorf("acp is not configured (missing url or agent)")
	}
	if m.sessionID == "" {
		return fmt.Errorf("missing happy session id")
	}

	if err := m.ensureACPSessionID(); err != nil {
		return err
	}

	m.acpClient = acp.NewClient(m.cfg.ACPURL, m.acpAgent, m.debug)

	m.modeMu.Lock()
	m.mode = ModeRemote
	m.modeMu.Unlock()

	m.state.ControlledByUser = false

	if m.debug {
		log.Printf("ACP ready (agent=%s session=%s)", m.acpAgent, m.acpSessionID)
	}

	return nil
}

func (m *Manager) ensureACPSessionID() error {
	if m.acpSessionID != "" {
		return nil
	}
	if m.sessionID == "" {
		return fmt.Errorf("missing happy session id")
	}

	path := filepath.Join(m.cfg.DelightHome, "acp.sessions.json")
	store := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		var payload struct {
			Sessions map[string]string `json:"sessions"`
		}
		if err := json.Unmarshal(data, &payload); err == nil && payload.Sessions != nil {
			store = payload.Sessions
		}
	}

	if existing, ok := store[m.sessionID]; ok && existing != "" {
		m.acpSessionID = existing
		return nil
	}

	sessionID, err := acp.NewUUID()
	if err != nil {
		return err
	}
	store[m.sessionID] = sessionID

	payload := struct {
		Sessions map[string]string `json:"sessions"`
	}{
		Sessions: store,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		return err
	}

	m.acpSessionID = sessionID
	return nil
}

func (m *Manager) handleACPMessage(content string) {
	if m.acpClient == nil {
		return
	}

	m.setThinking(true)

	ctx := context.Background()
	result, err := m.acpClient.Run(ctx, m.acpSessionID, content)
	if err != nil {
		if m.debug {
			log.Printf("ACP run error: %v", err)
		}
		m.setThinking(false)
		return
	}
	if m.debug && result != nil {
		log.Printf("ACP run status: %s runId=%s awaiting=%v", result.Status, result.RunID, result.AwaitRequest != nil)
	}

	for result != nil && result.Status == "awaiting" && result.AwaitRequest != nil {
		if m.debug {
			log.Printf("ACP awaiting permission: runId=%s", result.RunID)
		}
		m.setThinking(false)

		resumePayload, ok := m.requestACPAwaitResume(result.AwaitRequest)
		if !ok {
			if m.debug {
				log.Printf("ACP await resume aborted")
			}
			return
		}
		if m.debug {
			log.Printf("ACP resuming run: %s", result.RunID)
		}
		result, err = m.acpClient.Resume(ctx, result.RunID, resumePayload)
		if err != nil {
			if m.debug {
				log.Printf("ACP resume error: %v", err)
			}
			return
		}
		if m.debug {
			log.Printf("ACP resume status: %s", result.Status)
		}

		m.setThinking(true)
	}

	if result != nil && result.OutputText != "" {
		if m.debug {
			log.Printf("ACP output: %q", result.OutputText)
		}
		m.sendAgentOutput(result.OutputText)
	}

	m.setThinking(false)
}

func (m *Manager) requestACPAwaitResume(awaitRequest map[string]interface{}) (wire.ACPAwaitResume, bool) {
	requestID, err := acp.NewUUID()
	if err != nil {
		if m.debug {
			log.Printf("ACP await request id error: %v", err)
		}
		return wire.ACPAwaitResume{}, false
	}

	payload, err := json.Marshal(wire.ACPAwaitInput{
		Await: awaitRequest,
	})
	if err != nil {
		return wire.ACPAwaitResume{}, false
	}

	response, err := m.handlePermissionRequest(requestID, "acp.await", payload)
	if err != nil {
		if m.debug {
			log.Printf("ACP permission error: %v", err)
		}
		return wire.ACPAwaitResume{}, false
	}
	if m.debug {
		log.Printf("ACP permission response received: requestId=%s", requestID)
	}

	allow := response != nil && response.Behavior == "allow"
	message := ""
	if response != nil {
		message = response.Message
	}

	return wire.ACPAwaitResume{
		Allow:   allow,
		Message: message,
	}, true
}

func (m *Manager) sendAgentOutput(text string) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

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
					Model: "acp",
					Content: []wire.ContentBlock{
						{Type: "text", Text: text},
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("ACP output marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		if m.debug {
			log.Printf("ACP output encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(wire.OutboundMessagePayload{
		SID:     m.sessionID,
		Message: encrypted,
	})
}
