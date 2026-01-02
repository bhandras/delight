package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
)

func (m *Manager) registerMachineRPCHandlers() {
	if m.machineRPC == nil {
		return
	}

	prefix := m.machineID + ":"

	m.machineRPC.RegisterHandler(prefix+"spawn-session", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_machine_spawn_session", params)
		var req wire.SpawnSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		if req.Directory == "" {
			return nil, fmt.Errorf("directory is required")
		}

		if _, err := os.Stat(req.Directory); err != nil {
			if os.IsNotExist(err) {
				if !req.ApprovedNewDirectoryCreation {
					return json.Marshal(wire.SpawnSessionResponse{
						Type:      "requestToApproveDirectoryCreation",
						Directory: req.Directory,
					})
				}
				if err := os.MkdirAll(req.Directory, 0o700); err != nil {
					return nil, fmt.Errorf("failed to create directory: %w", err)
				}
			} else {
				return nil, fmt.Errorf("failed to stat directory: %w", err)
			}
		}

		sessionID, err := m.spawnChildSession(req.Directory, req.Agent)
		if err != nil {
			return nil, err
		}
		if sessionID == "" {
			return nil, fmt.Errorf("session id not assigned")
		}

		return json.Marshal(wire.SpawnSessionResponse{
			Type:      "success",
			SessionID: sessionID,
		})
	})

	m.machineRPC.RegisterHandler(prefix+"stop-session", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_machine_stop_session", params)
		var req wire.StopSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		if req.SessionID == "" {
			return nil, fmt.Errorf("sessionId is required")
		}

		if err := m.stopChildSession(req.SessionID); err != nil {
			return nil, err
		}
		return json.Marshal(wire.StopSessionResponse{Message: "Session stopped"})
	})

	m.machineRPC.RegisterHandler(prefix+"stop-daemon", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_machine_stop_daemon", params)
		logger.Infof("Stop-daemon requested")
		m.scheduleShutdown()
		m.forceExitAfter(2 * time.Second)
		return json.Marshal(wire.StopDaemonResponse{
			Message: "Daemon stop request acknowledged, starting shutdown sequence...",
		})
	})

	m.machineRPC.RegisterHandler(prefix+"ping", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_machine_ping", params)
		return json.Marshal(wire.PingResponse{Success: true})
	})
}

func (m *Manager) updateMachineState() error {
	if m.machineClient == nil || !m.machineClient.IsConnected() {
		return nil
	}

	if len(m.masterSecret) != 32 {
		return fmt.Errorf("master secret must be 32 bytes, got %d", len(m.masterSecret))
	}
	encryptedState, err := crypto.EncryptWithDataKey(m.machineState, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt daemon state: %w", err)
	}

	resp, err := m.machineClient.EmitWithAck(
		"machine-update-state",
		wire.MachineUpdateStatePayload{
			MachineID:       m.machineID,
			DaemonState:     base64.StdEncoding.EncodeToString(encryptedState),
			ExpectedVersion: m.machineStateVer,
		},
		5*time.Second,
	)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("missing ack")
	}

	result, _ := resp["result"].(string)
	switch result {
	case "success":
		m.machineStateVer = getInt64(resp["version"])
	case "version-mismatch":
		m.machineStateVer = getInt64(resp["version"])
	default:
		return fmt.Errorf("machine-update-state failed: %v", result)
	}
	return nil
}

func (m *Manager) updateMachineMetadata() error {
	if m.machineClient == nil || !m.machineClient.IsConnected() {
		return nil
	}

	if len(m.masterSecret) != 32 {
		return fmt.Errorf("master secret must be 32 bytes, got %d", len(m.masterSecret))
	}
	encryptedMeta, err := crypto.EncryptWithDataKey(m.machineMetadata, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt machine metadata: %w", err)
	}

	resp, err := m.machineClient.EmitWithAck(
		"machine-update-metadata",
		wire.MachineUpdateMetadataPayload{
			MachineID:       m.machineID,
			Metadata:        base64.StdEncoding.EncodeToString(encryptedMeta),
			ExpectedVersion: m.machineMetaVer,
		},
		5*time.Second,
	)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("missing ack")
	}

	result, _ := resp["result"].(string)
	switch result {
	case "success":
		m.machineMetaVer = getInt64(resp["version"])
	case "version-mismatch":
		m.machineMetaVer = getInt64(resp["version"])
	default:
		return fmt.Errorf("machine-update-metadata failed: %v", result)
	}
	return nil
}

func getInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	default:
		return 0
	}
}
