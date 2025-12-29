package session

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bhandras/delight/cli/internal/claude"
)

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
			// Common shapes we have seen:
			// - {"content":"..."}
			// - {"content":[{"type":"text","text":"..."}]}
			// - {"text":"..."}
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

	return normalizeRemoteInputText(walk(value))
}

// handleSessionIDDetection uses dual verification to detect Claude session ID
func (m *Manager) handleSessionIDDetection() {
	// Wait for UUID from fd 3
	select {
	case <-m.stopCh:
		return
	case claudeSessionID := <-m.claudeProcess.SessionID():
		if m.debug {
			log.Printf("Received Claude session ID from fd3: %s", claudeSessionID)
		}

		// Dual verification: also check that the session file exists
		if claude.WaitForSessionFile(m.metadata.Path, claudeSessionID, 5*time.Second) {
			if !m.enqueueInbound(func() {
				m.claudeSessionID = claudeSessionID
				log.Printf("Claude session verified: %s", claudeSessionID)

				// Start session file scanner
				m.sessionScanner = claude.NewScanner(m.metadata.Path, claudeSessionID, m.debug)
				m.sessionScanner.Start()

				// Forward session messages to server
				go m.forwardSessionMessages()
			}) {
				return
			}
		} else {
			if m.debug {
				log.Printf("Session file not found for UUID: %s (may be a different UUID)", claudeSessionID)
			}
			// Continue listening for more UUIDs
			go m.handleSessionIDDetection()
		}
	}
}

// handleThinkingState broadcasts thinking state changes to the server
func (m *Manager) handleThinkingState() {
	for {
		select {
		case <-m.stopCh:
			return
		case thinking, ok := <-m.claudeProcess.Thinking():
			if !ok {
				return
			}
			if !m.enqueueInbound(func() {
				m.setThinking(thinking)
				if m.debug {
					log.Printf("Thinking state changed: %v", thinking)
				}
			}) {
				return
			}
		}
	}
}

// forwardSessionMessages sends Claude session messages to the server
func (m *Manager) forwardSessionMessages() {
	if m.sessionScanner == nil {
		return
	}

	for {
		select {
		case <-m.stopCh:
			return
		case msg, ok := <-m.sessionScanner.Messages():
			if !ok {
				return
			}

			// Detach from the scanner's internal buffers/slices so the inbound queue
			// processes an immutable snapshot.
			payload, err := json.Marshal(msg)
			if err != nil {
				if m.debug {
					log.Printf("Failed to marshal scanner message: %v", err)
				}
				continue
			}
			var copyMsg claude.SessionMessage
			if err := json.Unmarshal(payload, &copyMsg); err != nil {
				if m.debug {
					log.Printf("Failed to unmarshal scanner message: %v", err)
				}
				continue
			}

			if !m.enqueueInbound(func() { m.forwardSessionMessageInbound(&copyMsg) }) {
				if m.debug {
					log.Printf("Inbound queue full; dropping scanner message uuid=%s type=%s", copyMsg.UUID, copyMsg.Type)
				}
			}
		}
	}
}

func (m *Manager) forwardSessionMessageInbound(msg *claude.SessionMessage) {
	if msg == nil {
		return
	}

	if m.debug {
		log.Printf("Forwarding session message: type=%s uuid=%s", msg.Type, msg.UUID)
	}

	// Suppress forwarding "user" messages that came from the remote bridge.
	// The remote/mobile bridge already sent that user text to the server; Claude's
	// session file will contain a copy which we would otherwise forward back and
	// cause duplicates in the UI.
	if msg.Type == "user" {
		userText := extractClaudeUserText(msg.Message)
		if m.isRecentlyInjectedRemoteInput(userText) {
			if m.debug {
				log.Printf("Skipping echoed remote user input from scanner: %q", userText)
			}
			return
		}
	}

	encrypted, err := m.encryptMessage(msg)
	if err != nil {
		if m.debug {
			log.Printf("Failed to encrypt message: %v", err)
		}
		return
	}

	if m.wsClient != nil && m.wsClient.IsConnected() {
		m.wsClient.EmitMessage(map[string]interface{}{
			"sid":     m.sessionID,
			"localId": msg.UUID,
			"message": encrypted,
		})
	}
}

// encryptMessage encrypts a session message for transmission
func (m *Manager) encryptMessage(msg *claude.SessionMessage) (string, error) {
	// Marshal the message to JSON
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt message: %w", err)
	}

	return encrypted, nil
}
