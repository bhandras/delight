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

// terminalAckClient is the subset of websocket.Client used by terminal update
// helpers. It exists to keep tests deterministic.
type terminalAckClient interface {
	IsConnected() bool
	EmitWithAck(event string, payload any, timeout time.Duration) (map[string]any, error)
}

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

	newVer, err := emitTerminalUpdateState(m.terminalClient, m.terminalID, m.terminalState, m.masterSecret, m.terminalStateVer)
	if err != nil {
		return err
	}
	m.terminalStateVer = newVer
	return nil
}

// emitTerminalUpdateState encrypts and sends the terminal daemon state update,
// returning the version reported by the server.
func emitTerminalUpdateState(client terminalAckClient, terminalID string, state any, masterSecret []byte, expectedVersion int64) (int64, error) {
	if len(masterSecret) != 32 {
		return 0, fmt.Errorf("master secret must be 32 bytes, got %d", len(masterSecret))
	}
	encryptedState, err := crypto.EncryptWithDataKey(state, masterSecret)
	if err != nil {
		return 0, fmt.Errorf("failed to encrypt daemon state: %w", err)
	}

	resp, err := client.EmitWithAck(
		"terminal-update-state",
		wire.TerminalUpdateStatePayload{
			TerminalID:      terminalID,
			DaemonState:     base64.StdEncoding.EncodeToString(encryptedState),
			ExpectedVersion: expectedVersion,
		},
		5*time.Second,
	)
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, fmt.Errorf("missing ack")
	}

	result, _ := resp["result"].(string)
	switch result {
	case "success":
		return getInt64(resp["version"]), nil
	case "version-mismatch":
		return getInt64(resp["version"]), nil
	default:
		return 0, fmt.Errorf("terminal-update-state failed: %v", result)
	}
}

// updateTerminalMetadata emits the current terminal metadata to the server.
func (m *Manager) updateTerminalMetadata() error {
	if m.terminalClient == nil || !m.terminalClient.IsConnected() {
		return nil
	}

	newVer, err := emitTerminalUpdateMetadata(m.terminalClient, m.terminalID, m.terminalMetadata, m.masterSecret, m.terminalMetaVer)
	if err != nil {
		return err
	}
	m.terminalMetaVer = newVer
	return nil
}

// emitTerminalUpdateMetadata encrypts and sends the terminal metadata update,
// returning the version reported by the server.
func emitTerminalUpdateMetadata(client terminalAckClient, terminalID string, metadata any, masterSecret []byte, expectedVersion int64) (int64, error) {
	if len(masterSecret) != 32 {
		return 0, fmt.Errorf("master secret must be 32 bytes, got %d", len(masterSecret))
	}
	encryptedMeta, err := crypto.EncryptWithDataKey(metadata, masterSecret)
	if err != nil {
		return 0, fmt.Errorf("failed to encrypt terminal metadata: %w", err)
	}

	resp, err := client.EmitWithAck(
		"terminal-update-metadata",
		wire.TerminalUpdateMetadataPayload{
			TerminalID:      terminalID,
			Metadata:        base64.StdEncoding.EncodeToString(encryptedMeta),
			ExpectedVersion: expectedVersion,
		},
		5*time.Second,
	)
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, fmt.Errorf("missing ack")
	}

	result, _ := resp["result"].(string)
	switch result {
	case "success":
		return getInt64(resp["version"]), nil
	case "version-mismatch":
		return getInt64(resp["version"]), nil
	default:
		return 0, fmt.Errorf("terminal-update-metadata failed: %v", result)
	}
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
