package session

import (
	"time"

	"github.com/bhandras/delight/cli/internal/session/runtime"
)

func (m *Manager) initRuntime() {
	if m.rt != nil || m.sessionID == "" {
		return
	}
	m.rt = runtime.New(runtime.Config{
		SessionID: m.sessionID,
	})
	m.rt.Start()

	go m.runRuntimeCommands()
}

func (m *Manager) runRuntimeCommands() {
	if m.rt == nil {
		return
	}
	for {
		select {
		case <-m.stopCh:
			return
		case cmd, ok := <-m.rt.Commands():
			if !ok {
				return
			}
			m.handleRuntimeCommand(cmd)
		}
	}
}

func (m *Manager) handleRuntimeCommand(cmd runtime.Command) {
	if cmd == nil {
		return
	}

	switch c := cmd.(type) {
	case runtime.EmitActivityCommand:
		m.broadcastThinking(c.Thinking)
	}
}

func (m *Manager) setThinking(thinking bool) {
	if m.thinking == thinking {
		return
	}
	m.thinking = thinking

	if m.rt == nil {
		m.broadcastThinking(thinking)
		return
	}

	if !m.rt.Post(runtime.SetThinkingEvent{
		Thinking: thinking,
		AtMs:     time.Now().UnixMilli(),
	}) {
		// Best-effort fallback if the runtime queue is full or stopping.
		m.broadcastThinking(thinking)
	}
}
