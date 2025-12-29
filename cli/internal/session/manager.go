package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/acp"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/protocol/wire"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
)

// Mode represents the current operation mode
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeRemote Mode = "remote"
)

// Manager manages a Delight session with advanced Claude tracking
type Manager struct {
	cfg                  *config.Config
	token                string
	machineID            string
	sessionID            string
	sessionTag           string
	dataKey              []byte
	masterSecret         []byte
	wsClient             *websocket.Client
	rpcManager           *websocket.RPCManager
	machineClient        *websocket.Client
	machineRPC           *websocket.RPCManager
	machineMetaVer       int64
	machineStateVer      int64
	disableMachineSocket bool
	claudeProcess        *claude.Process
	remoteBridge         *claude.RemoteBridge
	metadata             *types.Metadata
	metaVersion          int64
	state                *types.AgentState
	stateVersion         int64
	machineMetadata      *types.MachineMetadata
	machineState         *types.DaemonState
	debug                bool
	fakeAgent            bool
	acpClient            *acp.Client
	acpSessionID         string
	acpAgent             string
	agent                string
	codexClient          *codex.Client
	codexQueue           chan codexMessage
	codexStop            chan struct{}
	codexSessionActive   bool
	codexPermissionMode  string
	codexModel           string

	// Mode management
	mode     Mode
	modeMu   sync.RWMutex
	switchCh chan Mode
	workDir  string

	// Advanced Claude tracking
	claudeSessionID string
	sessionScanner  *claude.Scanner
	thinking        bool
	stopCh          chan struct{}

	// Pending permission requests (for remote mode)
	pendingPermissions map[string]chan *claude.PermissionResponse
	permissionMu       sync.Mutex

	spawnMu         sync.Mutex
	spawnedSessions map[string]*Manager

	spawnStoreMu sync.Mutex

	// inboundQueue serializes inbound events (socket updates, mobile RPC, etc.)
	// to avoid concurrent state mutation when clients deliver events in parallel.
	inboundOnce  sync.Once
	inboundQueue chan func()

	shutdownOnce sync.Once

	lastMachineKeepAliveSkipAt time.Time

	// recentRemoteInputs tracks user text that originated from the remote/mobile bridge.
	// Those inputs are injected into the local agent/Claude process, then also show up in
	// Claude's session transcript file. Our session scanner forwards transcript entries
	// back to the server — without suppression that causes remote user messages to be
	// persisted (and displayed) twice.
	recentRemoteInputsMu sync.Mutex
	recentRemoteInputs   []remoteInputRecord
}

type codexMessage struct {
	text string
	meta map[string]interface{}
}

// NewManager creates a new session manager
func NewManager(cfg *config.Config, token string, debug bool) (*Manager, error) {
	// Get or create master secret
	masterSecret, err := storage.GetOrCreateSecretKey(filepath.Join(cfg.DelightHome, "master.key"))
	if err != nil {
		return nil, fmt.Errorf("failed to get master secret: %w", err)
	}

	return &Manager{
		cfg:                cfg,
		token:              token,
		masterSecret:       masterSecret,
		debug:              debug,
		fakeAgent:          cfg.FakeAgent,
		acpAgent:           cfg.ACPAgent,
		agent:              cfg.Agent,
		codexQueue:         make(chan codexMessage, 100),
		codexStop:          make(chan struct{}),
		stopCh:             make(chan struct{}),
		inboundQueue:       make(chan func(), 256),
		switchCh:           make(chan Mode, 1),
		mode:               ModeLocal,
		pendingPermissions: make(map[string]chan *claude.PermissionResponse),
		spawnedSessions:    make(map[string]*Manager),
		state: &types.AgentState{
			ControlledByUser: true,
		},
	}, nil
}

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

	// Initialize machine metadata
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
				_ = m.machineClient.EmitRaw("machine-alive", map[string]interface{}{
					"machineId": m.machineID,
					"time":      time.Now().UnixMilli(),
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

	if m.cfg.ACPEnable && m.agent != "codex" && m.debug {
		log.Printf("ACP mode disabled (Claude TUI always on)")
	}

	if m.agent == "codex" {
		if err := m.startCodex(); err != nil {
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
	go m.handleSessionIDDetection()

	// Start thinking state handler
	go m.handleThinkingState()

	// Start keep-alive loop
	go m.keepAliveLoop()

	return nil
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
	reqBody := map[string]interface{}{
		"tag":      m.sessionTag,
		"metadata": base64.StdEncoding.EncodeToString(encryptedMeta),
	}

	body, err := json.Marshal(reqBody)
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

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Server returns {"session": {...}}
	sessionObj, ok := result["session"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response: missing session object")
	}

	sessionID, ok := sessionObj["id"].(string)
	if !ok {
		return fmt.Errorf("invalid response: missing session id")
	}

	m.sessionID = sessionID

	// Extract data key if present
	if dataKeyB64, ok := sessionObj["dataEncryptionKey"].(string); ok && dataKeyB64 != "" {
		decrypted, err := crypto.DecryptDataEncryptionKey(dataKeyB64, m.masterSecret)
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
	reqBody := map[string]interface{}{
		"id":          m.machineID,
		"metadata":    base64.StdEncoding.EncodeToString(encryptedMeta),
		"daemonState": base64.StdEncoding.EncodeToString(encryptedState),
	}

	body, err := json.Marshal(reqBody)
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

	var response struct {
		Machine struct {
			MetadataVersion    int64 `json:"metadataVersion"`
			DaemonStateVersion int64 `json:"daemonStateVersion"`
		} `json:"machine"`
	}
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

	cipher, ok, err := wire.ExtractNewMessageCipher(data)
	if err != nil {
		if m.debug {
			log.Printf("Update parse error: %v", err)
		}
		return
	}
	if !ok || cipher == "" {
		return
	}

	// Build a minimal payload compatible with handleMessage
	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"content": map[string]interface{}{
				"t": "encrypted",
				"c": cipher,
			},
		},
	}
	m.handleMessage(payload)
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
					if err := m.machineClient.EmitRaw("machine-alive", map[string]interface{}{
						"machineId": m.machineID,
						"time":      time.Now().UnixMilli(),
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
				return bridge.Wait()
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

	m.spawnMu.Lock()
	for _, child := range m.spawnedSessions {
		child.Close()
	}
	m.spawnedSessions = make(map[string]*Manager)
	m.spawnMu.Unlock()

	// Kill Claude process
	if m.claudeProcess != nil {
		m.claudeProcess.Kill()
	}

	// Kill remote bridge
	if m.remoteBridge != nil {
		m.remoteBridge.Kill()
	}

	if m.codexClient != nil {
		m.codexClient.Close()
	}

	return nil
}

// GetClaudeSessionID returns the detected Claude session ID
func (m *Manager) GetClaudeSessionID() string {
	return m.claudeSessionID
}

// IsThinking returns whether Claude is currently thinking
func (m *Manager) IsThinking() bool {
	return m.thinking
}

// handlePermissionRequest processes tool permission requests from Claude
func (m *Manager) handlePermissionRequest(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	if m.debug {
		log.Printf("Permission request: %s for tool %s", requestID, toolName)
	}

	// Create response channel
	responseCh := make(chan *claude.PermissionResponse, 1)

	m.permissionMu.Lock()
	m.pendingPermissions[requestID] = responseCh
	m.permissionMu.Unlock()

	// Emit permission request to mobile app via WebSocket
	if m.wsClient != nil && m.wsClient.IsConnected() {
		// Encrypt the request
		reqData, _ := json.Marshal(map[string]interface{}{
			"requestId": requestID,
			"toolName":  toolName,
			"input":     input,
		})

		encrypted, err := m.encrypt(reqData)
		if err == nil {
			m.wsClient.EmitEphemeral(map[string]interface{}{
				"type":      "permission-request",
				"id":        m.sessionID,
				"requestId": requestID,
				"data":      encrypted,
			})
		}
	}

	// Wait for response with timeout
	select {
	case response := <-responseCh:
		return response, nil
	case <-time.After(5 * time.Minute):
		m.permissionMu.Lock()
		delete(m.pendingPermissions, requestID)
		m.permissionMu.Unlock()
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Permission request timed out",
		}, nil
	case <-m.stopCh:
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Session closed",
		}, nil
	}
}

// HandlePermissionResponse handles a permission response from the mobile app
func (m *Manager) HandlePermissionResponse(requestID string, allow bool, message string) {
	m.permissionMu.Lock()
	responseCh, ok := m.pendingPermissions[requestID]
	if ok {
		delete(m.pendingPermissions, requestID)
	}
	m.permissionMu.Unlock()

	if !ok {
		if m.debug {
			log.Printf("Unknown permission request: %s", requestID)
		}
		return
	}

	behavior := "deny"
	if allow {
		behavior = "allow"
	}

	responseCh <- &claude.PermissionResponse{
		Behavior: behavior,
		Message:  message,
	}

	if m.debug {
		log.Printf("Permission response delivered: %s allow=%v", requestID, allow)
	}
}

// broadcastThinking broadcasts thinking state to connected clients
func (m *Manager) broadcastThinking(thinking bool) {
	if m.wsClient != nil && m.wsClient.IsConnected() {
		m.wsClient.EmitEphemeral(map[string]interface{}{
			"type":     "activity",
			"id":       m.sessionID,
			"active":   true,
			"thinking": thinking,
			"activeAt": time.Now().UnixMilli(),
		})
	}
}

// updateState sends state update to server
func (m *Manager) updateState() {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	// Encrypt state
	stateData, err := json.Marshal(m.state)
	if err != nil {
		return
	}

	encrypted, err := m.encrypt(stateData)
	if err != nil {
		return
	}

	expectedVersion := m.stateVersion
	newVersion, err := m.wsClient.UpdateState(m.sessionID, encrypted, expectedVersion)
	if err != nil {
		if errors.Is(err, websocket.ErrVersionMismatch) {
			if newVersion > 0 {
				m.stateVersion = newVersion
			}
			return
		}
		if m.debug {
			log.Printf("Update state error: %v", err)
		}
		return
	}
	if newVersion > 0 {
		m.stateVersion = newVersion
	} else {
		m.stateVersion = expectedVersion + 1
	}
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
