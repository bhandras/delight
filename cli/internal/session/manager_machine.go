package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/protocol/wire"
)

func (m *Manager) registerMachineRPCHandlers() {
	if m.machineRPC == nil {
		return
	}

	prefix := m.machineID + ":"

	m.machineRPC.RegisterHandler(prefix+"spawn-happy-session", func(params json.RawMessage) (json.RawMessage, error) {
		return m.runInboundRPC(func() (json.RawMessage, error) {
			wire.DumpToTestdata("rpc_machine_spawn_happy_session", params)
			var req wire.SpawnHappySessionRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			if req.Directory == "" {
				return nil, fmt.Errorf("directory is required")
			}

			if _, err := os.Stat(req.Directory); err != nil {
				if os.IsNotExist(err) {
					if !req.ApprovedNewDirectoryCreation {
						return json.Marshal(wire.SpawnHappySessionResponse{
							Type:      "requestToApproveDirectoryCreation",
							Directory: req.Directory,
						})
					}
					if err := os.MkdirAll(req.Directory, 0700); err != nil {
						return nil, fmt.Errorf("failed to create directory: %w", err)
					}
				} else {
					return nil, fmt.Errorf("failed to stat directory: %w", err)
				}
			}

			child, err := NewManager(m.cfg, m.token, m.debug)
			if err != nil {
				return nil, err
			}
			if req.Agent == "acp" || req.Agent == "claude" || req.Agent == "codex" {
				child.agent = req.Agent
			}
			child.disableMachineSocket = true

			if err := child.Start(req.Directory); err != nil {
				return nil, err
			}
			if child.sessionID == "" {
				return nil, fmt.Errorf("session id not assigned")
			}

			m.spawnMu.Lock()
			m.spawnedSessions[child.sessionID] = child
			m.spawnMu.Unlock()

			m.registerSpawnedSession(child.sessionID, req.Directory)

			go func(sessionID string, mgr *Manager) {
				_ = mgr.Wait()
				m.spawnMu.Lock()
				delete(m.spawnedSessions, sessionID)
				m.spawnMu.Unlock()
			}(child.sessionID, child)

			return json.Marshal(wire.SpawnHappySessionResponse{
				Type:      "success",
				SessionID: child.sessionID,
			})
		})
	})

	m.machineRPC.RegisterHandler(prefix+"stop-session", func(params json.RawMessage) (json.RawMessage, error) {
		return m.runInboundRPC(func() (json.RawMessage, error) {
			wire.DumpToTestdata("rpc_machine_stop_session", params)
			var req wire.StopSessionRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			if req.SessionID == "" {
				return nil, fmt.Errorf("sessionId is required")
			}

			m.spawnMu.Lock()
			child, ok := m.spawnedSessions[req.SessionID]
			if ok {
				delete(m.spawnedSessions, req.SessionID)
			}
			m.spawnMu.Unlock()

			if !ok {
				return nil, fmt.Errorf("session not found")
			}
			_ = child.Close()
			m.removeSpawnedSession(req.SessionID)
			return json.Marshal(wire.StopSessionResponse{Message: "Session stopped"})
		})
	})

	m.machineRPC.RegisterHandler(prefix+"stop-daemon", func(params json.RawMessage) (json.RawMessage, error) {
		return m.runInboundRPC(func() (json.RawMessage, error) {
			wire.DumpToTestdata("rpc_machine_stop_daemon", params)
			log.Printf("Stop-daemon requested")
			m.scheduleShutdown()
			m.forceExitAfter(2 * time.Second)
			return json.Marshal(wire.StopDaemonResponse{
				Message: "Daemon stop request acknowledged, starting shutdown sequence...",
			})
		})
	})

	m.machineRPC.RegisterHandler(prefix+"ping", func(params json.RawMessage) (json.RawMessage, error) {
		return m.runInboundRPC(func() (json.RawMessage, error) {
			wire.DumpToTestdata("rpc_machine_ping", params)
			return json.Marshal(wire.PingResponse{Success: true})
		})
	})
}

func (m *Manager) updateMachineState() error {
	if m.machineClient == nil || !m.machineClient.IsConnected() {
		return nil
	}

	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)

	encryptedState, err := crypto.EncryptLegacy(m.machineState, &secretKey)
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

	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)

	encryptedMeta, err := crypto.EncryptLegacy(m.machineMetadata, &secretKey)
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
