package sdk

import (
	"encoding/json"
	"time"
)

const (
	// sessionUIUpdateType identifies the synthetic update payload emitted by
	// the SDK when the derived UI state changes.
	sessionUIUpdateType = "session-ui"
)

// sessionUIJSON encodes the derived UI map into a stable JSON string for
// change detection.
func sessionUIJSON(ui map[string]any) string {
	if ui == nil {
		return ""
	}
	encoded, err := json.Marshal(ui)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// buildSessionUIUpdate constructs a synthetic update payload that mirrors the
// server "update" event envelope.
func buildSessionUIUpdate(nowMs int64, sessionID string, ui map[string]any) map[string]any {
	return map[string]any{
		"createdAt": nowMs,
		"body": map[string]any{
			"t":   sessionUIUpdateType,
			"sid": sessionID,
			"ui":  ui,
		},
	}
}

// clearSessionSwitching clears the switching/transition flags for a session and
// emits a fresh session-ui update so clients can re-render immediately.
func (c *Client) clearSessionSwitching(sessionID string) {
	if sessionID == "" {
		return
	}

	now := time.Now().UnixMilli()

	c.mu.Lock()
	prev := c.sessionFSM[sessionID]
	prev.switching = false
	prev.transition = ""
	prev.switchingAt = 0
	_, ui := deriveSessionUI(now, prev.connected, prev.online, prev.working, "", &prev)
	prev.uiJSON = sessionUIJSON(ui)
	c.sessionFSM[sessionID] = prev
	c.mu.Unlock()

	c.emitUpdate(sessionID, buildSessionUIUpdate(now, sessionID, ui))
}
