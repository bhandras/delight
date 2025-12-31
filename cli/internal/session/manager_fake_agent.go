package session

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
)

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

	_ = m.wsClient.EmitMessage(wire.OutboundMessagePayload{
		SID:     m.sessionID,
		Message: encrypted,
	})
}
