package session

import (
	"fmt"
	"time"

	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
)

const (
	// agentConfigPersistTimeout bounds how long we wait for the session actor to
	// persist agent config updates to the server (so listSessions observes them).
	agentConfigPersistTimeout = 2 * time.Second
)

// SetAgentConfig updates durable session agent configuration.
//
// This is a remote-only operation: callers must ensure the session is in
// phone-controlled mode (remote) before calling this method.
func (m *Manager) SetAgentConfig(model string, permissionMode string, reasoningEffort string) error {
	if m.sessionActor == nil {
		return fmt.Errorf("session actor not initialized")
	}

	reply := make(chan error, 1)
	if !m.sessionActor.Enqueue(sessionactor.SetAgentConfig(model, permissionMode, reasoningEffort, reply)) {
		return fmt.Errorf("failed to schedule agent config update")
	}

	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-reply:
		if err != nil {
			return err
		}
	}

	// Ensure the change is persisted before returning, so callers can reliably
	// observe the updated values via listSessions.
	persistReply := make(chan error, 1)
	if !m.sessionActor.Enqueue(sessionactor.WaitForAgentStatePersist(persistReply)) {
		return fmt.Errorf("failed to schedule agent config persistence")
	}
	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-persistReply:
		return err
	case <-time.After(agentConfigPersistTimeout):
		return fmt.Errorf("timed out waiting for agent config persistence")
	}
}
