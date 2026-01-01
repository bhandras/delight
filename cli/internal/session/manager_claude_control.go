package session

import (
	"fmt"

	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
)

// GetMode returns the current session control mode derived from the SessionActor state.
func (m *Manager) GetMode() Mode {
	if m.sessionActor == nil {
		return ModeLocal
	}
	// Not all agents have a local/remote runner lifecycle yet. For legacy agents,
	// treat "mode" as an observability signal derived from AgentState.
	if m.agent != "claude" && m.agent != "codex" {
		if m.sessionActor.State().AgentState.ControlledByUser {
			return ModeLocal
		}
		return ModeRemote
	}
	switch m.sessionActor.State().Mode {
	case sessionactor.ModeRemote:
		return ModeRemote
	case sessionactor.ModeLocal:
		return ModeLocal
	default:
		return ModeLocal
	}
}

// SwitchToRemote switches to remote mode (phone control).
func (m *Manager) SwitchToRemote() error {
	if m.agent != "claude" && m.agent != "codex" {
		return fmt.Errorf("switch mode is not supported for agent=%s", m.agent)
	}
	if m.sessionActor == nil {
		return fmt.Errorf("session actor not initialized")
	}

	reply := make(chan error, 1)
	if !m.sessionActor.Enqueue(sessionactor.SwitchMode(sessionactor.ModeRemote, reply)) {
		return fmt.Errorf("failed to schedule switch")
	}

	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-reply:
		return err
	}
}

// SwitchToLocal switches to local mode (desktop control).
func (m *Manager) SwitchToLocal() error {
	if m.agent != "claude" && m.agent != "codex" {
		return fmt.Errorf("switch mode is not supported for agent=%s", m.agent)
	}
	if m.sessionActor == nil {
		return fmt.Errorf("session actor not initialized")
	}

	reply := make(chan error, 1)
	if !m.sessionActor.Enqueue(sessionactor.SwitchMode(sessionactor.ModeLocal, reply)) {
		return fmt.Errorf("failed to schedule switch")
	}

	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-reply:
		return err
	}
}

// SendUserMessage sends a user message to the remote runner.
func (m *Manager) SendUserMessage(content string, meta map[string]interface{}) error {
	if m.agent != "claude" && m.agent != "codex" {
		return fmt.Errorf("remote send is not supported for agent=%s", m.agent)
	}
	if m.sessionActor == nil {
		return fmt.Errorf("session actor not initialized")
	}
	reply := make(chan error, 1)
	if !m.sessionActor.Enqueue(sessionactor.RemoteSend(content, meta, "", reply)) {
		return fmt.Errorf("failed to schedule remote send")
	}
	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-reply:
		return err
	}
}

// AbortRemote aborts the current remote query.
func (m *Manager) AbortRemote() error {
	if m.agent != "claude" && m.agent != "codex" {
		return fmt.Errorf("abort is not supported for agent=%s", m.agent)
	}
	if m.sessionActor == nil {
		return fmt.Errorf("session actor not initialized")
	}
	reply := make(chan error, 1)
	if !m.sessionActor.Enqueue(sessionactor.AbortRemote(reply)) {
		return fmt.Errorf("failed to schedule abort")
	}
	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-reply:
		return err
	}
}
