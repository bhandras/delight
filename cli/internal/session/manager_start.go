package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/crypto"
	framework "github.com/bhandras/delight/cli/internal/actor"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
)

func (m *Manager) startInboundLoop() {
	m.inboundOnce.Do(func() {
		go func() {
			for {
				select {
				case <-m.stopCh:
					return
				case fn := <-m.inboundQueue:
					if fn != nil {
						fn()
					}
				}
			}
		}()
	})
}

func (m *Manager) enqueueInbound(fn func()) bool {
	if fn == nil {
		return false
	}
	select {
	case <-m.stopCh:
		return false
	default:
	}

	select {
	case m.inboundQueue <- fn:
		return true
	default:
		if m.debug {
			log.Printf("Inbound queue full; dropping event")
		}
		return false
	}
}

func (m *Manager) handleUpdateQueued(data map[string]interface{}) {
	_ = m.enqueueInbound(func() { m.handleUpdate(data) })
}

func (m *Manager) handleSessionUpdateQueued(data map[string]interface{}) {
	_ = m.enqueueInbound(func() { m.handleSessionUpdate(data) })
}

// Start starts a new Delight session
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

	// Ensure inbound processing is serialized.
	m.startInboundLoop()

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

	log.Printf("Machine registered: %s", m.machineID)

	// Create session on server
	if err := m.createSession(); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	log.Printf("Session created: %s", m.sessionID)

	m.initRuntime()
	m.initSpawnActor()

	if m.agent == "acp" {
		if !m.cfg.ACPEnable {
			return fmt.Errorf("acp agent selected but ACP is not configured")
		}
		if err := m.startACP(); err != nil {
			return err
		}
	}

	// Connect WebSocket
	m.wsClient = websocket.NewClient(m.cfg.ServerURL, m.token, m.sessionID, m.debug)

	// Register event handlers
	// Prefer structured updates; legacy message handler kept for backward compatibility
	m.wsClient.On(websocket.EventUpdate, m.handleUpdateQueued)
	m.wsClient.On(websocket.EventSessionUpdate, m.handleSessionUpdateQueued)

	if err := m.wsClient.Connect(); err != nil {
		// WebSocket is optional - log the error but don't fail
		log.Printf("Warning: WebSocket connection failed: %v", err)
		log.Println("Continuing without WebSocket (real-time updates disabled)")
	} else {
		// Set up RPC manager for mobile app commands (registers on connect)
		m.rpcManager = websocket.NewRPCManager(m.wsClient, m.debug)
		m.rpcManager.SetEncryption(m.encrypt, m.decrypt)
		m.rpcManager.SetupSocketHandlers(m.wsClient.RawSocket())
		m.registerRPCHandlers()

		if m.wsClient.WaitForConnect(5 * time.Second) {
			log.Println("WebSocket connected")
			m.rpcManager.RegisterAll()
			_ = m.wsClient.KeepSessionAlive(m.sessionID, m.thinking)
			// Initialize the session actor (Phase 4): actor-owned persistence of agentState.
			m.initSessionActor()
			// Persist the initial agent state immediately so mobile can derive control mode
			// deterministically (and to migrate any legacy/invalid agentState on the server).
			m.requestPersistAgentState()
		} else {
			log.Printf("Warning: WebSocket connection timeout")
		}
	}

	// Connect machine-scoped WebSocket (best-effort)
	if !m.disableMachineSocket {
		m.machineClient = websocket.NewMachineClient(m.cfg.ServerURL, m.token, m.machineID, m.debug)
		if err := m.machineClient.Connect(); err != nil {
			log.Printf("Warning: Machine WebSocket connection failed: %v", err)
			m.machineClient = nil
		} else {
			m.machineRPC = websocket.NewRPCManager(m.machineClient, m.debug)
			m.machineRPC.SetEncryption(m.encryptMachine, m.decryptMachine)
			m.machineRPC.SetupSocketHandlers(m.machineClient.RawSocket())
			m.registerMachineRPCHandlers()

			if m.machineClient.WaitForConnect(5 * time.Second) {
				if m.debug {
					log.Println("Machine WebSocket connected")
				}
				m.machineRPC.RegisterAll()
				_ = m.machineClient.EmitRaw("machine-alive", wire.MachineAlivePayload{
					MachineID: m.machineID,
					Time:      time.Now().UnixMilli(),
				})
				if err := m.updateMachineState(); err != nil && m.debug {
					log.Printf("Machine state update error: %v", err)
				}
				if err := m.updateMachineMetadata(); err != nil && m.debug {
					log.Printf("Machine metadata update error: %v", err)
				}
			} else if m.debug {
				log.Printf("Machine WebSocket connection timeout")
			}
		}
	}

	if err := m.restoreSpawnedSessions(); err != nil && m.debug {
		log.Printf("Failed to restore spawned sessions: %v", err)
	}

	if m.fakeAgent {
		log.Println("Fake agent mode enabled (no Claude process)")
		go m.keepAliveLoop()
		return nil
	}

	if m.cfg.ACPEnable && m.agent != "acp" && m.debug {
		log.Printf("ACP configured but disabled (agent=%s)", m.agent)
	}

	if m.agent == "codex" {
		if err := m.startCodex(); err != nil {
			return err
		}
		go m.keepAliveLoop()
		return nil
	}

	if m.agent == "acp" {
		go m.keepAliveLoop()
		return nil
	}

	// Optionally start Claude in remote mode immediately (before any messages).
	// This keeps parity with Happy’s "remote runner is ready" behavior and avoids
	// paying the spawn cost on the first mobile message.
	if m.agent == "claude" && m.cfg != nil && m.cfg.StartingMode == "remote" {
		if err := m.SwitchToRemote(); err != nil {
			return err
		}
		go m.keepAliveLoop()
		return nil
	}

	// Start Claude process with fd 3 tracking
	claudeProc, err := claude.NewProcess(workDir, m.debug)
	if err != nil {
		return fmt.Errorf("failed to create claude process: %w", err)
	}

	m.claudeProcess = claudeProc

	if err := claudeProc.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}

	log.Println("Claude Code started (with fd3 tracking)")

	// Start session ID detection handler
	localCancel := m.beginLocalRun()
	go m.handleSessionIDDetection(localCancel, claudeProc)

	// Start thinking state handler
	go m.handleThinkingState(localCancel, claudeProc)

	// Start keep-alive loop
	go m.keepAliveLoop()

	return nil
}

func (m *Manager) initSessionActor() {
	// Idempotent.
	if m.sessionActor != nil {
		if m.sessionActorRuntime != nil {
			m.sessionActorRuntime.WithSessionID(m.sessionID)
			if m.wsClient != nil {
				m.sessionActorRuntime.WithStateUpdater(m.wsClient)
			}
		}
		return
	}

	rt := sessionactor.NewRuntime(m.workDir, m.debug).
		WithSessionID(m.sessionID).
		WithStateUpdater(m.wsClient)

	hooks := framework.Hooks[sessionactor.State]{
		OnInput: func(input framework.Input) {
			// Keep Manager.stateDirty aligned with persistence outcomes while we
			// transition to actor-owned state.
			switch input.(type) {
			case sessionactor.EvAgentStatePersisted:
				m.stateMu.Lock()
				m.stateDirty = false
				m.stateMu.Unlock()
			case sessionactor.EvAgentStateVersionMismatch, sessionactor.EvAgentStatePersistFailed:
				m.stateMu.Lock()
				m.stateDirty = true
				m.stateMu.Unlock()
			}
		},
	}

	// Initialize with the current agent-state version (usually 0 until first persist).
	initial := sessionactor.State{
		FSM:                 sessionactor.StateLocalRunning,
		Mode:                sessionactor.ModeLocal,
		PersistRetryRemaining: 0,
		AgentStateVersion:    m.stateVersion,
	}

	m.sessionActorRuntime = rt
	m.sessionActor = framework.New(initial, sessionactor.Reduce, rt, framework.WithHooks(hooks))
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

	// Encrypt metadata
	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)

	encryptedMeta, err := crypto.EncryptLegacy(m.metadata, &secretKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt metadata: %w", err)
	}

	// Create session request (encode metadata as base64 string)
	body, err := json.Marshal(wire.CreateSessionRequest{
		Tag:      m.sessionTag,
		Metadata: base64.StdEncoding.EncodeToString(encryptedMeta),
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
	if result.Session.DataEncryptionKey != "" {
		decrypted, err := crypto.DecryptDataEncryptionKey(result.Session.DataEncryptionKey, m.masterSecret)
		if err != nil {
			log.Printf("Failed to decrypt data encryption key: %v", err)
		} else {
			m.dataKey = decrypted
			if m.debug {
				log.Println("Data encryption key decrypted")
			}
		}
	}

	return nil
}

func stableSessionTag(machineID, workDir string) string {
	hash := sha256.Sum256([]byte(workDir))
	return fmt.Sprintf("m-%s-%s", machineID, hex.EncodeToString(hash[:6]))
}

// createMachine creates or updates a machine on the server
func (m *Manager) createMachine() error {
	// Encrypt machine metadata
	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)

	encryptedMeta, err := crypto.EncryptLegacy(m.machineMetadata, &secretKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt machine metadata: %w", err)
	}

	// Encrypt daemon state
	encryptedState, err := crypto.EncryptLegacy(m.machineState, &secretKey)
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
		log.Printf("Machine created/updated successfully")
	}

	return nil
}

// handleMessage handles incoming messages from the server (from mobile app)
func (m *Manager) handleMessage(data map[string]interface{}) {
	if m.debug {
		log.Printf("Received message from server: %+v", data)
	}

	wire.DumpToTestdata("session_message_event", data)

	cipher, localID, ok, err := wire.ExtractMessageCipher(data)
	if err != nil {
		if m.debug {
			log.Printf("Message parse error: %v", err)
		}
		return
	}
	if !ok || cipher == "" {
		if m.debug {
			log.Println("Message has no usable ciphertext payload")
		}
		return
	}
	m.handleEncryptedUserMessage(cipher, localID)
}

func (m *Manager) handleEncryptedUserMessage(cipher string, localID string) {

	// Decrypt the message content
	decrypted, err := m.decrypt(cipher)
	if err != nil {
		if m.debug {
			log.Printf("Failed to decrypt message: %v", err)
		}
		return
	}

	text, meta, ok, err := wire.ParseUserTextRecord(decrypted)
	if err != nil {
		if m.debug {
			log.Printf("Failed to parse decrypted message: %v", err)
		}
		return
	}

	if m.debug {
		log.Printf("Decrypted message: localId=%s text=%s", localID, text)
	}

	if !ok || text == "" {
		if m.debug {
			log.Printf("Ignoring message with empty or unsupported content")
		}
		return
	}

	// Suppress local echoes: when the CLI forwards a user message to the server
	// (from the Claude session scanner), we can later receive that same message
	// back via the update stream. Re-injecting would duplicate input.
	if localID != "" && m.isRecentlySentOutboundUserLocalID(localID) {
		if m.debug {
			log.Printf("Skipping echoed local user message: localId=%s", localID)
		}
		return
	}

	if m.fakeAgent {
		m.sendFakeAgentResponse(text)
		return
	}

	if m.agent == "codex" {
		if text == "" {
			return
		}
		select {
		case m.codexQueue <- codexMessage{text: text, meta: meta}:
		default:
			if m.debug {
				log.Printf("Codex queue full; dropping message")
			}
		}
		return
	}

	messageContent := text
	if messageContent == "" {
		if m.debug {
			log.Printf("Empty message content, ignoring")
		}
		return
	}
	messageContent = strings.TrimRight(messageContent, "\r\n")

	if m.agent == "acp" {
		m.handleACPMessage(messageContent)
		return
	}

	if m.agent == "claude" {
		// If we're in local mode and we receive an inbound message from the server,
		// treat that as a mobile handoff request (Happy parity): switch to remote
		// mode and forward the message to the remote runner.
		if m.GetMode() == ModeLocal {
			if err := m.SwitchToRemote(); err != nil {
				if m.debug {
					log.Printf("Switch-to-remote failed (falling back to local): %v", err)
				}
			} else {
				if err := m.SendUserMessage(messageContent, meta); err == nil {
					return
				} else if m.debug {
					log.Printf("Remote send failed after switch (falling back to local): %v", err)
				}
			}
		} else {
			// Remote mode: forward input to the remote bridge.
			//
			// If the bridge died but the mode bit is still remote, SwitchToRemote()
			// reinitializes the bridge.
			if err := m.SendUserMessage(messageContent, meta); err == nil {
				return
			}
			if m.debug {
				log.Printf("Remote send failed; attempting to restart remote mode: %v", err)
			}
			if err := m.SwitchToRemote(); err == nil {
				if err := m.SendUserMessage(messageContent, meta); err == nil {
					return
				}
				if m.debug {
					log.Printf("Remote send still failing; falling back to local: %v", err)
				}
			}
		}
	}

	// This is a local-mode fallback path (remote bridge could not be started).
	m.rememberRemoteInput(messageContent)

	if m.claudeProcess == nil {
		if m.debug {
			log.Printf("Claude process not running; ignoring message")
		}
		return
	}

	if err := m.claudeProcess.SendLine(messageContent); err != nil && m.debug {
		log.Printf("Failed to send input to Claude TUI: %v", err)
	}
}

// handleUpdate handles structured "update" events (new-message, etc.)
func (m *Manager) handleUpdate(data map[string]interface{}) {
	if m.debug {
		log.Printf("Received update from server: %+v", data)
	}

	wire.DumpToTestdata("session_update_event", data)

	cipher, localID, ok, err := wire.ExtractNewMessageCipherAndLocalID(data)
	if err != nil {
		if m.debug {
			log.Printf("Update parse error: %v", err)
		}
		return
	}
	if !ok || cipher == "" {
		return
	}

	m.handleEncryptedUserMessage(cipher, localID)
}

// handleSessionUpdate handles session update events
func (m *Manager) handleSessionUpdate(data map[string]interface{}) {
	if m.debug {
		log.Printf("Session update: %+v", data)
	}
}

// keepAliveLoop sends periodic keep-alive pings for both machine and session
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
			_ = m.enqueueInbound(func() {
				// Send machine keep-alive via machine-scoped socket
				if m.machineClient != nil && m.machineClient.IsConnected() {
					if err := m.machineClient.EmitRaw("machine-alive", wire.MachineAlivePayload{
						MachineID: m.machineID,
						Time:      time.Now().UnixMilli(),
					}); err != nil && m.debug {
						log.Printf("Machine keep-alive error: %v", err)
					}
				} else if m.debug {
					now := time.Now()
					if m.lastMachineKeepAliveSkipAt.IsZero() || now.Sub(m.lastMachineKeepAliveSkipAt) > 2*time.Minute {
						m.lastMachineKeepAliveSkipAt = now
						log.Printf("Machine keep-alive skipped (no machine socket)")
					}
				}
			})

		case <-sessionTicker.C:
			_ = m.enqueueInbound(func() {
				// Send session keep-alive
				if m.wsClient != nil && m.wsClient.IsConnected() {
					if err := m.wsClient.KeepSessionAlive(m.sessionID, m.thinking); err != nil && m.debug {
						log.Printf("Session keep-alive error: %v", err)
					}
					// If a previous state update failed due to transient Socket.IO issues,
					// retry opportunistically once we're connected again.
					if m.stateDirty {
						m.requestPersistAgentState()
					}
				}
			})
		}
	}
}

// Wait waits for the current process (Claude or remote bridge) to exit
// This blocks until the session is closed, handling mode switches
func (m *Manager) Wait() error {
	if m.agent == "codex" && m.codexClient != nil {
		for {
			select {
			case <-m.stopCh:
				return nil
			default:
			}
			if err := m.codexClient.Wait(); err != nil {
				return err
			}
			return nil
		}
	}

	for {
		select {
		case <-m.stopCh:
			return nil
		default:
		}

		m.modeMu.RLock()
		mode := m.mode
		claudeProc := m.claudeProcess
		bridge := m.remoteBridge
		m.modeMu.RUnlock()

		switch mode {
		case ModeLocal:
			if claudeProc != nil {
				err := claudeProc.Wait()
				// If Claude was killed due to mode switch, continue waiting
				if err != nil && m.GetMode() == ModeRemote {
					continue
				}
				return err
			}
		case ModeRemote:
			if bridge != nil {
				err := bridge.Wait()
				// If the bridge exited because we switched back to local mode,
				// keep the CLI running and wait for the next active runner.
				if m.GetMode() == ModeLocal {
					continue
				}
				return err
			}
		}

		// No active process, wait a bit and check again
		// This handles the transition period during mode switch
		select {
		case <-m.stopCh:
			return nil
		case <-time.After(100 * time.Millisecond):
			continue
		}
	}
}

// Close cleans up resources
func (m *Manager) Close() error {
	// Signal stop to all goroutines
	select {
	case <-m.stopCh:
		// Already closed
	default:
		close(m.stopCh)
	}

	// Stop session scanner
	if m.sessionScanner != nil {
		m.sessionScanner.Stop()
	}

	// Close WebSocket
	if m.wsClient != nil {
		m.wsClient.Close()
	}
	if m.machineClient != nil {
		m.machineClient.Close()
	}

	m.shutdownSpawnedSessions()

	// Kill Claude process
	if m.claudeProcess != nil {
		m.claudeProcess.Kill()
	}

	// Kill remote bridge
	if m.remoteBridge != nil {
		m.remoteBridge.Kill()
	}

	// Stop any active stdin watcher.
	m.stopDesktopTakebackWatcher()

	if m.codexClient != nil {
		m.codexClient.Close()
	}

	if m.rt != nil {
		m.rt.Stop()
	}

	return nil
}

func (m *Manager) scheduleShutdown() {
	m.shutdownOnce.Do(func() {
		go func() {
			time.Sleep(200 * time.Millisecond)
			if m.debug {
				log.Printf("Stop-daemon: shutting down")
			}
			go func() {
				_ = m.Close()
			}()
			time.Sleep(200 * time.Millisecond)
			if m.debug {
				log.Printf("Stop-daemon: exiting")
			}
			os.Exit(0)
		}()
	})
}

func (m *Manager) forceExitAfter(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		if m.debug {
			log.Printf("Stop-daemon: forcing exit after %s", delay)
		}
		os.Exit(0)
	}()
}
