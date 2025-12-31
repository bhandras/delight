package session

import (
	"encoding/json"
	"fmt"

	"github.com/bhandras/delight/protocol/wire"
)

// registerRPCHandlers sets up RPC handlers for mobile app commands
func (m *Manager) registerRPCHandlers() {
	if m.rpcManager == nil {
		return
	}

	// Scoped method prefix
	prefix := m.sessionID + ":"

	// Abort handler - abort current remote query
	m.rpcManager.RegisterHandler(prefix+"abort", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_session_abort", params)
		if err := m.AbortRemote(); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"success": true})
	})

	// Switch handler - switch between local/remote modes
	m.rpcManager.RegisterHandler(prefix+"switch", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_session_switch", params)
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

	// Permission handler - respond to permission requests
	m.rpcManager.RegisterHandler(prefix+"permission", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_session_permission", params)
		var req wire.PermissionResponseRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}

		m.HandlePermissionResponse(req.RequestID, req.Allow, req.Message)
		return json.Marshal(wire.SuccessResponse{Success: true})
	})
}
