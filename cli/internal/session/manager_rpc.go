package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bhandras/delight/cli/internal/agentengine/codexengine"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/shared/wire"
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

	// Agent-config handler - update model/effort/permission mode (remote-only).
	m.rpcManager.RegisterHandler(prefix+"agent-config", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_session_agent_config", params)
		var req wire.SetAgentConfigRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}

		// Remote-only: require phone control before allowing changes.
		if m.GetMode() != ModeRemote {
			return nil, fmt.Errorf("cannot update agent config: not in remote mode")
		}
		if m.sessionActor != nil && m.sessionActor.State().AgentState.ControlledByUser {
			return nil, fmt.Errorf("cannot update agent config: session not phone-controlled")
		}

		if err := m.SetAgentConfig(req.Model, req.PermissionMode, req.ReasoningEffort); err != nil {
			// Return a typed response instead of relying on implicit RPC errors.
			// Some SDK clients treat errors as regular payloads.
			return json.Marshal(wire.SetAgentConfigResponse{Success: false, Error: err.Error()})
		}

		agentStateJSON := ""
		agentStateVersion := int64(0)
		if m.sessionActor != nil {
			st := m.sessionActor.State()
			agentStateJSON = st.AgentStateJSON
			agentStateVersion = st.AgentStateVersion
		}
		return json.Marshal(wire.SetAgentConfigResponse{
			Success:           true,
			AgentState:        agentStateJSON,
			AgentStateVersion: agentStateVersion,
		})
	})

	// Agent-capabilities handler - query supported settings + current config.
	m.rpcManager.RegisterHandler(prefix+"agent-capabilities", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_session_agent_capabilities", params)

		var req wire.AgentCapabilitiesRequest
		if len(params) > 0 {
			// Treat malformed payloads as a request for the current snapshot; this
			// handler is frequently invoked from UI refresh flows and should be
			// resilient to version skew.
			_ = json.Unmarshal(params, &req)
		}

		if m.sessionActor == nil {
			return json.Marshal(wire.AgentCapabilitiesResponse{Success: false, Error: "session actor not initialized"})
		}

		reply := make(chan sessionactor.AgentEngineSettingsSnapshot, 1)
		if !m.sessionActor.Enqueue(sessionactor.GetAgentEngineSettings(reply)) {
			return json.Marshal(wire.AgentCapabilitiesResponse{Success: false, Error: "failed to schedule agent capabilities query"})
		}

		select {
		case <-m.stopCh:
			return json.Marshal(wire.AgentCapabilitiesResponse{Success: false, Error: "session closed"})
		case snapshot := <-reply:
			resp := wire.AgentCapabilitiesResponse{
				Success:   snapshot.Error == "",
				AgentType: string(snapshot.AgentType),
				Error:     snapshot.Error,
				Capabilities: wire.AgentCapabilities{
					Models:           snapshot.Capabilities.Models,
					PermissionModes:  snapshot.Capabilities.PermissionModes,
					ReasoningEfforts: snapshot.Capabilities.ReasoningEfforts,
				},
				DesiredConfig: wire.AgentConfig{
					Model:           snapshot.DesiredConfig.Model,
					ReasoningEffort: snapshot.DesiredConfig.ReasoningEffort,
					PermissionMode:  snapshot.DesiredConfig.PermissionMode,
				},
				EffectiveConfig: wire.AgentConfig{
					Model:           snapshot.EffectiveConfig.Model,
					ReasoningEffort: snapshot.EffectiveConfig.ReasoningEffort,
					PermissionMode:  snapshot.EffectiveConfig.PermissionMode,
				},
			}
			if strings.TrimSpace(req.Model) != "" && resp.AgentType == "codex" {
				resp.Capabilities.ReasoningEfforts = codexengine.ReasoningEffortsForModel(req.Model)
			}
			if !resp.Success && resp.Error == "" {
				resp.Error = "failed to fetch agent capabilities"
			}
			return json.Marshal(resp)
		}
	})
}
