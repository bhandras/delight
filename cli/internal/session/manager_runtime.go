package session

import (
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
