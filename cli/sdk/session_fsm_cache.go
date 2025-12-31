package sdk

import "time"

// refreshSessionsForFSM refreshes session summaries from the server so the SDK
// has a current view of per-session control state.
func (c *Client) refreshSessionsForFSM() error {
	_, err := c.listSessions()
	return err
}

// ensureSessionFSM returns a recent FSM snapshot for a session.
//
// If the cached snapshot is missing or stale, it triggers a ListSessions
// refresh. This keeps send/switch gating deterministic even when callers do
// not poll the session list frequently.
func (c *Client) ensureSessionFSM(sessionID string) (sessionFSMState, bool, error) {
	now := time.Now().UnixMilli()

	c.mu.Lock()
	state, ok := c.sessionFSM[sessionID]
	c.mu.Unlock()

	if !ok || state.fetchedAt == 0 || time.Duration(now-state.fetchedAt)*time.Millisecond > sessionFSMStaleAfter {
		if err := c.refreshSessionsForFSM(); err != nil {
			return sessionFSMState{}, false, err
		}
		c.mu.Lock()
		state, ok = c.sessionFSM[sessionID]
		c.mu.Unlock()
	}
	return state, ok, nil
}
