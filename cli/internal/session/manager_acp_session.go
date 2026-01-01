package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bhandras/delight/cli/internal/acp"
)

// ensureACPSessionID ensures m.acpSessionID is set for the current Delight session.
//
// ACP expects a stable session identifier to preserve conversation state across runs.
// We persist a mapping of Delight session id -> ACP session id under DelightHome.
func (m *Manager) ensureACPSessionID() error {
	if m == nil {
		return fmt.Errorf("manager is nil")
	}
	if m.acpSessionID != "" {
		return nil
	}
	if m.sessionID == "" {
		return fmt.Errorf("missing delight session id")
	}
	if m.cfg == nil {
		return fmt.Errorf("missing config")
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
