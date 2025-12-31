package session

import (
	"fmt"

	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
)

func (m *Manager) GetMode() Mode {
	if m.sessionActor == nil {
		return ModeLocal
	}
	// For non-Claude agents, treat "control" as a UI signal derived from the
	// agent state rather than an actual local/remote runner lifecycle.
	if m.agent != "claude" {
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
	if m.agent != "claude" {
		return fmt.Errorf("switch mode is only supported for Claude sessions")
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
	if m.agent != "claude" {
		return fmt.Errorf("switch mode is only supported for Claude sessions")
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

// SendUserMessage sends a user message to Claude (remote mode only).
func (m *Manager) SendUserMessage(content string, meta map[string]interface{}) error {
	if m.agent != "claude" {
		return fmt.Errorf("remote send is only supported for Claude sessions")
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
	if m.agent != "claude" {
		return fmt.Errorf("abort is only supported for Claude sessions")
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
