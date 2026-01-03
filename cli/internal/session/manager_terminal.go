package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
)

// registerTerminalRPCHandlers registers RPC methods available on the
// terminal-scoped websocket.
func (m *Manager) registerTerminalRPCHandlers() {
	if m.terminalRPC == nil {
		return
	}

	prefix := m.terminalID + ":"

	m.terminalRPC.RegisterHandler(prefix+"stop-daemon", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_terminal_stop_daemon", params)
		logger.Infof("Stop-daemon requested")
		m.scheduleShutdown()
		m.forceExitAfter(2 * time.Second)
		return json.Marshal(wire.StopDaemonResponse{
			Message: "Daemon stop request acknowledged, starting shutdown sequence...",
		})
	})

	m.terminalRPC.RegisterHandler(prefix+"ping", func(params json.RawMessage) (json.RawMessage, error) {
		wire.DumpToTestdata("rpc_terminal_ping", params)
		return json.Marshal(wire.PingResponse{Success: true})
	})
}

// updateTerminalState emits the current daemon state to the server.
func (m *Manager) updateTerminalState() error {
	if m.terminalClient == nil || !m.terminalClient.IsConnected() {
		return nil
	}

	if len(m.masterSecret) != 32 {
		return fmt.Errorf("master secret must be 32 bytes, got %d", len(m.masterSecret))
	}
	encryptedState, err := crypto.EncryptWithDataKey(m.terminalState, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt daemon state: %w", err)
	}

	resp, err := m.terminalClient.EmitWithAck(
		"terminal-update-state",
		wire.TerminalUpdateStatePayload{
			TerminalID:      m.terminalID,
			DaemonState:     base64.StdEncoding.EncodeToString(encryptedState),
			ExpectedVersion: m.terminalStateVer,
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
		m.terminalStateVer = getInt64(resp["version"])
	case "version-mismatch":
		m.terminalStateVer = getInt64(resp["version"])
	default:
		return fmt.Errorf("terminal-update-state failed: %v", result)
	}
	return nil
}

// updateTerminalMetadata emits the current terminal metadata to the server.
func (m *Manager) updateTerminalMetadata() error {
	if m.terminalClient == nil || !m.terminalClient.IsConnected() {
		return nil
	}

	if len(m.masterSecret) != 32 {
		return fmt.Errorf("master secret must be 32 bytes, got %d", len(m.masterSecret))
	}
	encryptedMeta, err := crypto.EncryptWithDataKey(m.terminalMetadata, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt terminal metadata: %w", err)
	}

	resp, err := m.terminalClient.EmitWithAck(
		"terminal-update-metadata",
		wire.TerminalUpdateMetadataPayload{
			TerminalID:      m.terminalID,
			Metadata:        base64.StdEncoding.EncodeToString(encryptedMeta),
			ExpectedVersion: m.terminalMetaVer,
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
		m.terminalMetaVer = getInt64(resp["version"])
	case "version-mismatch":
		m.terminalMetaVer = getInt64(resp["version"])
	default:
		return fmt.Errorf("terminal-update-metadata failed: %v", result)
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
