package session

import (
	"encoding/json"
	"fmt"

	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/internal/protocol/wire"
)

func (m *Manager) runInboundRPC(fn func() (json.RawMessage, error)) (json.RawMessage, error) {
	type rpcResult struct {
		value json.RawMessage
		err   error
	}
	done := make(chan rpcResult, 1)
	if !m.enqueueInbound(func() {
		val, err := fn()
		done <- rpcResult{value: val, err: err}
	}) {
		return nil, fmt.Errorf("failed to schedule request (busy or shutting down)")
	}

	select {
	case <-m.stopCh:
		return nil, fmt.Errorf("session closed")
	case res := <-done:
		return res.value, res.err
	}
}

func (m *Manager) runInboundErr(fn func() error) error {
	done := make(chan error, 1)
	if !m.enqueueInbound(func() {
		done <- fn()
	}) {
		return fmt.Errorf("failed to schedule request (busy or shutting down)")
	}

	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case err := <-done:
		return err
	}
}

func (m *Manager) runInboundPermissionDecision(fn func() (*codex.PermissionDecision, error)) (*codex.PermissionDecision, error) {
	type result struct {
		decision *codex.PermissionDecision
		err      error
	}
	done := make(chan result, 1)
	if !m.enqueueInbound(func() {
		decision, err := fn()
		done <- result{decision: decision, err: err}
	}) {
		return nil, fmt.Errorf("failed to schedule request (busy or shutting down)")
	}

	select {
	case <-m.stopCh:
		return nil, fmt.Errorf("session closed")
	case res := <-done:
		return res.decision, res.err
	}
}

// registerRPCHandlers sets up RPC handlers for mobile app commands
func (m *Manager) registerRPCHandlers() {
	if m.rpcManager == nil {
		return
	}

	// Scoped method prefix
	prefix := m.sessionID + ":"

	// Abort handler - abort current remote query
	m.rpcManager.RegisterHandler(prefix+"abort", func(params json.RawMessage) (json.RawMessage, error) {
		return m.runInboundRPC(func() (json.RawMessage, error) {
			wire.DumpToTestdata("rpc_session_abort", params)
			if m.agent == "claude" {
				return nil, fmt.Errorf("remote abort not supported in Claude TUI mode")
			}
			if err := m.AbortRemote(); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]bool{"success": true})
		})
	})

	// Switch handler - switch between local/remote modes
	m.rpcManager.RegisterHandler(prefix+"switch", func(params json.RawMessage) (json.RawMessage, error) {
		return m.runInboundRPC(func() (json.RawMessage, error) {
			wire.DumpToTestdata("rpc_session_switch", params)
			if m.agent == "claude" {
				return nil, fmt.Errorf("mode switching disabled in Claude TUI mode")
			}
			var req wire.SwitchModeRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}

			switch Mode(req.Mode) {
			case ModeLocal:
				if err := m.SwitchToLocal(); err != nil {
					return nil, err
				}
			case ModeRemote:
				if err := m.SwitchToRemote(); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unknown mode: %s", req.Mode)
			}

			return json.Marshal(map[string]string{"mode": string(m.GetMode())})
		})
	})

	// Permission handler - respond to permission requests
	m.rpcManager.RegisterHandler(prefix+"permission", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_session_permission", params)
		var req wire.PermissionResponseRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}

		// This handler must not go through the inbound queue, since permission
		// requests are awaited synchronously in the inbound event loop.
		m.HandlePermissionResponse(req.RequestID, req.Allow, req.Message)
		return json.Marshal(wire.SuccessResponse{Success: true})
	})
}
