package session

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/crypto"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// dataEncryptionKeyBytes is the byte length for session/machine data keys.
	dataEncryptionKeyBytes = 32
)

// Start starts a new Delight session.
func (m *Manager) Start(workDir string) error {
	// Get current working directory if not specified
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Store working directory for mode switching
	m.workDir = workDir

	// Get or create stable machine ID
	machineIDPath := filepath.Join(m.cfg.DelightHome, "machine.id")
	machineID, err := storage.GetOrCreateMachineID(machineIDPath)
	if err != nil {
		return fmt.Errorf("failed to get machine ID: %w", err)
	}
	m.machineID = machineID

	// Initialize metadata
	hostname, _ := os.Hostname()
	homeDir, _ := os.UserHomeDir()

	m.metadata = &types.Metadata{
		Path:           workDir,
		Host:           hostname,
		Version:        "1.0.0",
		OS:             "darwin", // TODO: detect OS
		MachineID:      machineID,
		HomeDir:        homeDir,
		DelightHomeDir: m.cfg.DelightHome,
		Flavor:         m.agent,
	}

	// Initialize machine metadata (best-effort)
	m.machineMetadata = &types.MachineMetadata{
		Host:              hostname,
		Platform:          "darwin", // TODO: detect platform
		DelightCliVersion: "1.0.0",
		HomeDir:           homeDir,
		DelightHomeDir:    m.cfg.DelightHome,
	}

	// Initialize daemon state
	m.machineState = &types.DaemonState{
		Status:    "running",
		PID:       os.Getpid(),
		StartedAt: time.Now().UnixMilli(),
	}

	// Create or update machine on server
	if err := m.createMachine(); err != nil {
		return fmt.Errorf("failed to create machine: %w", err)
	}

	logger.Infof("Machine registered: %s", m.machineID)

	// Create session on server
	if err := m.createSession(); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	logger.Infof("Session created: %s", m.sessionID)

	m.initRuntime()
	m.initSpawnActor()
	// Initialize the session actor early so runner lifecycle and agent-state
	// transitions are serialized through the FSM even if the websocket connect
	// is delayed or offline.
	m.initSessionActor()

	if m.agent == "acp" {
		if !m.cfg.ACPEnable {
			return fmt.Errorf("acp agent selected but ACP is not configured")
		}
		if err := m.ensureACPSessionID(); err != nil {
			return fmt.Errorf("failed to ensure acp session id: %w", err)
		}
	}

	// Connect WebSocket
	m.wsClient = websocket.NewClient(m.cfg.ServerURL, m.token, m.sessionID, m.cfg.SocketIOTransport, m.debug)
	// Re-wire the session actor runtime with the now-constructed websocket client.
	// initSessionActor is idempotent and updates runtime adapters when actor exists.
	m.initSessionActor()

	// Bridge socket lifecycle into the SessionActor so connection state and
	// persistence retries are deterministic.
	m.wsClient.OnConnect(func() {
		if m.sessionActor != nil {
			_ = m.sessionActor.Enqueue(sessionactor.WSConnected())
		}
	})
	m.wsClient.OnDisconnect(func(reason string) {
		if m.sessionActor != nil {
			_ = m.sessionActor.Enqueue(sessionactor.WSDisconnected(reason))
		}
	})

	// Register event handlers
	// Prefer structured updates; legacy message handler kept for backward compatibility
	m.wsClient.On(websocket.EventUpdate, m.handleUpdate)
	m.wsClient.On(websocket.EventSessionUpdate, m.handleSessionUpdate)

	if err := m.wsClient.Connect(); err != nil {
		// WebSocket is optional - log the error but don't fail
		logger.Warnf("WebSocket connection failed: %v", err)
		logger.Warnf("Continuing without WebSocket (real-time updates disabled)")
	} else {
		// Set up RPC manager for mobile app commands (registers on connect)
		m.rpcManager = websocket.NewRPCManager(m.wsClient, m.debug)
		m.rpcManager.SetEncryption(m.encrypt, m.decrypt)
		m.rpcManager.SetupSocketHandlers(m.wsClient.RawSocket())
		m.registerRPCHandlers()

		if m.wsClient.WaitForConnect(5 * time.Second) {
			logger.Infof("WebSocket connected")
			m.rpcManager.RegisterAll()
			_ = m.wsClient.KeepSessionAlive(m.sessionID, m.thinking)
			// Persist the initial agent state immediately so mobile can derive
			// control mode deterministically (and to migrate any legacy/invalid
			// agentState on the server).
			if m.sessionActor != nil {
				state := m.sessionActor.State()
				if state.AgentStateJSON != "" {
					_ = m.sessionActor.Enqueue(sessionactor.PersistAgentStateImmediate(state.AgentStateJSON))
				}
			}
		} else {
			logger.Warnf("WebSocket connection timeout")
		}
	}

	// Connect machine-scoped WebSocket (best-effort)
	if !m.disableMachineSocket {
		m.machineClient = websocket.NewMachineClient(m.cfg.ServerURL, m.token, m.machineID, m.cfg.SocketIOTransport, m.debug)
		m.machineClient.OnConnect(func() {
			if m.sessionActor != nil {
				_ = m.sessionActor.Enqueue(sessionactor.MachineConnected())
			}
		})
		m.machineClient.OnDisconnect(func(reason string) {
			if m.sessionActor != nil {
				_ = m.sessionActor.Enqueue(sessionactor.MachineDisconnected(reason))
			}
		})
		if err := m.machineClient.Connect(); err != nil {
			logger.Warnf("Machine WebSocket connection failed: %v", err)
			m.machineClient = nil
		} else {
			m.machineRPC = websocket.NewRPCManager(m.machineClient, m.debug)
			m.machineRPC.SetEncryption(m.encryptMachine, m.decryptMachine)
			m.machineRPC.SetupSocketHandlers(m.machineClient.RawSocket())
			m.registerMachineRPCHandlers()

			if m.machineClient.WaitForConnect(5 * time.Second) {
				if m.debug {
					logger.Infof("Machine WebSocket connected")
				}
				m.machineRPC.RegisterAll()
				_ = m.machineClient.EmitRaw("machine-alive", wire.MachineAlivePayload{
					MachineID: m.machineID,
					Time:      time.Now().UnixMilli(),
				})
				if err := m.updateMachineState(); err != nil && m.debug {
					logger.Warnf("Machine state update error: %v", err)
				}
				if err := m.updateMachineMetadata(); err != nil && m.debug {
					logger.Warnf("Machine metadata update error: %v", err)
				}
			} else if m.debug {
				logger.Warnf("Machine WebSocket connection timeout")
			}
		}
	}

	if err := m.restoreSpawnedSessions(); err != nil && m.debug {
		logger.Warnf("Failed to restore spawned sessions: %v", err)
	}

	if m.cfg.ACPEnable && m.agent != "acp" && m.debug {
		logger.Warnf("ACP configured but disabled (agent=%s)", m.agent)
	}

	if m.agent == "acp" {
		// ACP has no local runner; start in remote mode so phone input is accepted.
		if err := m.SwitchToRemote(); err != nil {
			return err
		}
		go m.keepAliveLoop()
		return nil
	}

	// Agent runner lifecycle is owned by the SessionActor FSM.
	//
	// Codex supports both:
	// - remote mode (MCP server)
	// - local mode (native TUI + rollout tail)
	//
	// Claude supports local PTY and remote stream-json.
	if m.agent == "claude" || m.agent == "codex" || m.agent == "fake" {
		if m.cfg != nil && m.cfg.StartingMode == "remote" {
			if err := m.SwitchToRemote(); err != nil {
				return err
			}
		} else {
			if err := m.SwitchToLocal(); err != nil {
				return err
			}
		}
		go m.keepAliveLoop()
		return nil
	}

	return nil
}

// initSessionActor initializes the SessionActor FSM and wires runtime adapters.
func (m *Manager) initSessionActor() {
	// Idempotent.
	if m.sessionActor != nil {
		if m.sessionActorRuntime != nil {
			m.sessionActorRuntime.WithSessionID(m.sessionID)
			if m.wsClient != nil {
				m.sessionActorRuntime.WithStateUpdater(m.wsClient)
				m.sessionActorRuntime.WithSocketEmitter(m.wsClient)
			}
			m.sessionActorRuntime.WithAgent(m.agent)
			if m.cfg != nil && m.cfg.ACPEnable {
				m.sessionActorRuntime.WithACPConfig(m.cfg.ACPURL, m.acpAgent, m.acpSessionID)
			}
			m.sessionActorRuntime.WithEncryptFn(m.encrypt)
		}
		return
	}

	rt := sessionactor.NewRuntime(m.workDir, m.debug).
		WithSessionID(m.sessionID).
		WithStateUpdater(m.wsClient).
		WithSocketEmitter(m.wsClient).
		WithAgent(m.agent).
		WithACPConfig(m.cfg.ACPURL, m.acpAgent, m.acpSessionID).
		WithEncryptFn(m.encrypt)

	hooks := framework.Hooks[sessionactor.State]{
		OnInput: func(input framework.Input) {
			logger.Tracef("session-actor input: %T", input)
		},
		OnTransition: func(prev sessionactor.State, next sessionactor.State, input framework.Input) {
			_ = input
			if next.FSM == sessionactor.StateClosed && prev.FSM != sessionactor.StateClosed {
				m.sessionActorClosedOnce.Do(func() {
					if m.sessionActorClosed != nil {
						close(m.sessionActorClosed)
					}
				})
			}
		},
	}

	// Initialize agent state in a server-compatible shape (plaintext JSON).
	agentState := types.AgentState{
		AgentType:         m.agent,
		ControlledByUser:  true,
		Requests:          make(map[string]types.AgentPendingRequest),
		CompletedRequests: make(map[string]types.AgentCompletedRequest),
	}
	stateData, _ := json.Marshal(agentState)
	initial := sessionactor.State{
		SessionID:             m.sessionID,
		FSM:                   sessionactor.StateClosed,
		Mode:                  sessionactor.ModeLocal,
		AgentState:            agentState,
		AgentStateJSON:        string(stateData),
		PersistRetryRemaining: 0,
		AgentStateVersion:     0,
	}

	m.sessionActorRuntime = rt
	// Codex can emit very chatty event streams (reasoning deltas, tool logs, etc.).
	// The SessionActor mailbox must be large enough to avoid dropping critical
	// command inputs like cmdPermissionAwait, which would otherwise deadlock the
	// remote engine waiting for an approval decision.
	// sessionActorMailboxSize is intentionally large to prevent deadlocks in
	// synchronous flows (e.g. Codex permission prompts) when the engine is
	// emitting a high volume of events.
	const sessionActorMailboxSize = 8192
	m.sessionActor = framework.New(
		initial,
		sessionactor.Reduce,
		rt,
		framework.WithHooks(hooks),
		framework.WithMailboxSize[sessionactor.State](sessionActorMailboxSize),
	)
	m.sessionActor.Start()
}

// createSession creates a new session on the server
func (m *Manager) createSession() error {
	// Generate session tag (stable by default).
	if m.cfg.ForceNewSession {
		m.sessionTag = fmt.Sprintf("session-%d", time.Now().Unix())
	} else {
		m.sessionTag = stableSessionTag(m.machineID, m.workDir)
	}

	// Ensure the per-session dataEncryptionKey is available.
	//
	// This key is used for AES-256-GCM encryption of session payloads (RPC + messages).
	if m.dataKey == nil {
		m.dataKey = make([]byte, dataEncryptionKeyBytes)
		if _, err := rand.Read(m.dataKey); err != nil {
			return fmt.Errorf("failed to generate dataEncryptionKey: %w", err)
		}
	}

	// Encode metadata as base64(JSON). (No app clients depend on encrypted metadata.)
	metaJSON, err := json.Marshal(m.metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	encodedMeta := base64.StdEncoding.EncodeToString(metaJSON)

	// Create session request (encode metadata as base64 string)
	dataKeyB64 := base64.StdEncoding.EncodeToString(m.dataKey)
	body, err := json.Marshal(wire.CreateSessionRequest{
		Tag:               m.sessionTag,
		Metadata:          encodedMeta,
		DataEncryptionKey: &dataKeyB64,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send request
	url := fmt.Sprintf("%s/v1/sessions", m.cfg.ServerURL)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.token))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create session failed: %s - %s", resp.Status, string(respBody))
	}

	var result wire.CreateSessionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if result.Session.ID == "" {
		return fmt.Errorf("invalid response: missing session id")
	}
	m.sessionID = result.Session.ID

	// Extract data key if present
	if result.Session.DataEncryptionKey != nil && *result.Session.DataEncryptionKey != "" {
		if err := m.setSessionDataEncryptionKey(*result.Session.DataEncryptionKey); err != nil && m.debug {
			logger.Warnf("Failed to load session dataEncryptionKey: %v", err)
		}
	}

	return nil
}

// setSessionDataEncryptionKey loads the session's dataEncryptionKey.
//
// The server stores and returns `dataEncryptionKey` as raw 32-byte base64.
func (m *Manager) setSessionDataEncryptionKey(encoded string) error {
	if encoded == "" {
		return nil
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}

	if len(raw) != dataEncryptionKeyBytes {
		return fmt.Errorf("invalid dataEncryptionKey length: %d", len(raw))
	}
	m.dataKey = raw
	if m.debug {
		logger.Debugf("Session dataEncryptionKey loaded")
	}
	return nil
}

// stableSessionTag derives a stable tag for a (machine, workdir) pair.
func stableSessionTag(machineID, workDir string) string {
	hash := sha256.Sum256([]byte(workDir))
	return fmt.Sprintf("m-%s-%s", machineID, hex.EncodeToString(hash[:6]))
}

// createMachine creates or updates a machine on the server
func (m *Manager) createMachine() error {
	// Encrypt machine metadata
	if len(m.masterSecret) != dataEncryptionKeyBytes {
		return fmt.Errorf("master secret must be %d bytes, got %d", dataEncryptionKeyBytes, len(m.masterSecret))
	}
	encryptedMeta, err := crypto.EncryptWithDataKey(m.machineMetadata, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt machine metadata: %w", err)
	}

	// Encrypt daemon state
	encryptedState, err := crypto.EncryptWithDataKey(m.machineState, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt daemon state: %w", err)
	}

	// Create machine request
	body, err := json.Marshal(wire.CreateMachineRequest{
		ID:          m.machineID,
		Metadata:    base64.StdEncoding.EncodeToString(encryptedMeta),
		DaemonState: base64.StdEncoding.EncodeToString(encryptedState),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send request
	url := fmt.Sprintf("%s/v1/machines", m.cfg.ServerURL)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.token))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create machine failed: %s - %s", resp.Status, string(respBody))
	}

	var response wire.CreateMachineResponse
	if err := json.Unmarshal(respBody, &response); err == nil {
		m.machineMetaVer = response.Machine.MetadataVersion
		m.machineStateVer = response.Machine.DaemonStateVersion
	}

	if m.debug {
		logger.Infof("Machine created/updated successfully")
	}

	return nil
}

// handleMessage handles incoming messages from the server (from mobile app)
// handleMessage routes inbound websocket messages to the appropriate handler.
func (m *Manager) handleMessage(data map[string]interface{}) {
	if m.debug {
		logger.Tracef("Received message from server: %+v", data)
	}

	wire.DumpToTestdata("session_message_event", data)

	cipher, localID, ok, err := wire.ExtractMessageCipher(data)
	if err != nil {
		if m.debug {
			logger.Debugf("Message parse error: %v", err)
		}
		return
	}
	if !ok || cipher == "" {
		if m.debug {
			logger.Debugf("Message has no usable ciphertext payload")
		}
		return
	}
	m.handleEncryptedUserMessage(cipher, localID)
}

// handleEncryptedUserMessage decrypts, parses, and routes an inbound user message.
func (m *Manager) handleEncryptedUserMessage(cipher string, localID string) {

	// Decrypt the message content
	decrypted, err := m.decrypt(cipher)
	if err != nil {
		if m.debug {
			logger.Debugf("Failed to decrypt message: %v", err)
		}
		return
	}

	text, meta, ok, err := wire.ParseUserTextRecord(decrypted)
	if err != nil {
		if m.debug {
			logger.Debugf("Failed to parse decrypted message: %v", err)
		}
		return
	}

	if m.debug {
		logger.Tracef("Decrypted message: localId=%s text=%s", localID, text)
	}

	if !ok || text == "" {
		if m.debug {
			logger.Debugf("Ignoring message with empty or unsupported content")
		}
		return
	}

	// Suppress local echoes: when the CLI forwards a user message to the server
	// (from the Claude session scanner), we can later receive that same message
	// back via the update stream. Re-injecting would duplicate input.
	// This suppression is handled inside the SessionActor reducer once it owns
	// the dedupe window. Leave this check here only for non-Claude agents.

	messageContent := text
	if messageContent == "" {
		if m.debug {
			logger.Debugf("Empty message content, ignoring")
		}
		return
	}
	messageContent = strings.TrimRight(messageContent, "\r\n")

	if m.sessionActor == nil {
		if m.debug {
			logger.Warnf("Session actor not initialized; dropping message")
		}
		return
	}
	_ = m.sessionActor.Enqueue(sessionactor.InboundUserMessage(messageContent, meta, localID, time.Now().UnixMilli()))
	return
}

// handleUpdate handles structured "update" events (new-message, etc.)
func (m *Manager) handleUpdate(data map[string]interface{}) {
	if m.debug {
		logger.Tracef("Received update from server: %+v", data)
	}

	wire.DumpToTestdata("session_update_event", data)

	// Best-effort: hydrate dataEncryptionKey if this update includes a session
	// record (e.g. new-session). This is required for decrypting AES-GCM
	// encrypted messages coming from newer clients.
	if m.dataKey == nil {
		m.hydrateSessionDataEncryptionKeyFromUpdate(data)
	}

	cipher, localID, ok, err := wire.ExtractNewMessageCipherAndLocalID(data)
	if err != nil {
		if m.debug {
			logger.Debugf("Update parse error: %v", err)
		}
		return
	}
	if !ok || cipher == "" {
		return
	}

	m.handleEncryptedUserMessage(cipher, localID)
}

// hydrateSessionDataEncryptionKeyFromUpdate checks for a `new-session` update
// containing a dataEncryptionKey and loads it into the manager.
func (m *Manager) hydrateSessionDataEncryptionKeyFromUpdate(data map[string]interface{}) {
	body, ok := data["body"].(map[string]any)
	if !ok || body == nil {
		return
	}
	t, _ := body["t"].(string)
	if t != "new-session" {
		return
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	var session wire.UpdateBodyNewSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return
	}
	if session.ID == "" || session.ID != m.sessionID {
		return
	}
	if session.DataEncryptionKey == nil || *session.DataEncryptionKey == "" {
		return
	}
	if err := m.setSessionDataEncryptionKey(*session.DataEncryptionKey); err != nil && m.debug {
		logger.Warnf("Failed to hydrate session dataEncryptionKey from update: %v", err)
	}
}

// handleSessionUpdate handles session update events
// handleSessionUpdate handles inbound session update events (best-effort observability).
func (m *Manager) handleSessionUpdate(data map[string]interface{}) {
	if m.debug {
		logger.Tracef("Session update: %+v", data)
	}
}

// keepAliveLoop sends periodic keep-alive pings for both machine and session.
func (m *Manager) keepAliveLoop() {
	machineTicker := time.NewTicker(20 * time.Second)
	sessionTicker := time.NewTicker(30 * time.Second)
	defer machineTicker.Stop()
	defer sessionTicker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-machineTicker.C:
			// Send machine keep-alive via machine-scoped socket.
			if m.machineClient != nil && m.machineClient.IsConnected() {
				if err := m.machineClient.EmitRaw("machine-alive", wire.MachineAlivePayload{
					MachineID: m.machineID,
					Time:      time.Now().UnixMilli(),
				}); err != nil && m.debug {
					logger.Warnf("Machine keep-alive error: %v", err)
				}
			} else if m.debug {
				now := time.Now()
				if m.lastMachineKeepAliveSkipAt.IsZero() || now.Sub(m.lastMachineKeepAliveSkipAt) > 2*time.Minute {
					m.lastMachineKeepAliveSkipAt = now
					logger.Debugf("Machine keep-alive skipped (no machine socket)")
				}
			}

		case <-sessionTicker.C:
			// Send session keep-alive.
			if m.wsClient != nil && m.wsClient.IsConnected() {
				if err := m.wsClient.KeepSessionAlive(m.sessionID, m.thinking); err != nil && m.debug {
					logger.Warnf("Session keep-alive error: %v", err)
				}
				// AgentState persistence retries are actor-owned; do not attempt to
				// repersist from here.
			}
		}
	}
}

// Wait waits for the current process (Claude or remote bridge) to exit
// This blocks until the session is closed, handling mode switches
func (m *Manager) Wait() error {
	if m.sessionActorClosed != nil {
		select {
		case <-m.stopCh:
			return nil
		case <-m.sessionActorClosed:
			if m.sessionActor != nil {
				if errStr := m.sessionActor.State().LastExitErr; errStr != "" {
					return fmt.Errorf("%s", errStr)
				}
			}
			return nil
		}
	}

	<-m.stopCh
	return nil
}

// Close cleans up resources.
func (m *Manager) Close() error {
	// Signal stop to all goroutines
	select {
	case <-m.stopCh:
		// Already closed
	default:
		close(m.stopCh)
	}

	// Stop session scanner
	// Session scanning is owned by the SessionActor runtime.

	// Close WebSocket
	if m.wsClient != nil {
		m.wsClient.Close()
	}
	if m.machineClient != nil {
		m.machineClient.Close()
	}

	m.shutdownSpawnedSessions()

	if m.rt != nil {
		m.rt.Stop()
	}

	if m.sessionActor != nil {
		_ = m.sessionActor.Enqueue(sessionactor.Shutdown(nil))
		m.sessionActor.Stop()
	}

	return nil
}

// scheduleShutdown triggers a delayed manager shutdown and process exit.
func (m *Manager) scheduleShutdown() {
	m.shutdownOnce.Do(func() {
		go func() {
			time.Sleep(200 * time.Millisecond)
			if m.debug {
				logger.Infof("Stop-daemon: shutting down")
			}
			go func() {
				_ = m.Close()
			}()
			time.Sleep(200 * time.Millisecond)
			if m.debug {
				logger.Infof("Stop-daemon: exiting")
			}
			os.Exit(0)
		}()
	})
}

// forceExitAfter terminates the process after the delay, regardless of cleanup.
func (m *Manager) forceExitAfter(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		if m.debug {
			logger.Warnf("Stop-daemon: forcing exit after %s", delay)
		}
		os.Exit(0)
	}()
}
