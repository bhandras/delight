package session

import (
	"bytes"
	"context"
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
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/acp"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
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

	shutdownOnce sync.Once
}

type codexMessage struct {
	text string
	meta map[string]interface{}
}

type spawnEntry struct {
	SessionID string `json:"sessionId"`
	Directory string `json:"directory"`
}

type spawnRegistry struct {
	Owners map[string]map[string]spawnEntry `json:"owners"`
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
		switchCh:           make(chan Mode, 1),
		mode:               ModeLocal,
		pendingPermissions: make(map[string]chan *claude.PermissionResponse),
		spawnedSessions:    make(map[string]*Manager),
		state: &types.AgentState{
			ControlledByUser: true,
		},
	}, nil
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
	m.wsClient.On(websocket.EventUpdate, m.handleUpdate)
	m.wsClient.On(websocket.EventSessionUpdate, m.handleSessionUpdate)

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

	if m.cfg.ACPEnable && m.agent != "codex" {
		if err := m.ensureACPSessionID(); err != nil {
			return fmt.Errorf("failed to load ACP session id: %w", err)
		}
		m.acpClient = acp.NewClient(m.cfg.ACPURL, m.acpAgent, m.debug)
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		m.state.ControlledByUser = false
		m.updateState()
		log.Printf("ACP mode enabled: %s (%s)", m.cfg.ACPURL, m.acpAgent)
		go m.keepAliveLoop()
		return nil
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

func (m *Manager) ensureACPSessionID() error {
	if m.acpSessionID != "" {
		return nil
	}
	if m.sessionID == "" {
		return fmt.Errorf("missing happy session id")
	}

	path := filepath.Join(m.cfg.DelightHome, "acp.sessions.json")
	store := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		var payload struct {
			Sessions map[string]string `json:"sessions"`
		}
		if err := json.Unmarshal(data, &payload); err == nil && payload.Sessions != nil {
			store = payload.Sessions
		}
	}

	if existing, ok := store[m.sessionID]; ok && existing != "" {
		m.acpSessionID = existing
		return nil
	}

	sessionID, err := acp.NewUUID()
	if err != nil {
		return err
	}
	store[m.sessionID] = sessionID

	payload := struct {
		Sessions map[string]string `json:"sessions"`
	}{
		Sessions: store,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		return err
	}

	m.acpSessionID = sessionID
	return nil
}

// handleSessionIDDetection uses dual verification to detect Claude session ID
func (m *Manager) handleSessionIDDetection() {
	// Wait for UUID from fd 3
	select {
	case <-m.stopCh:
		return
	case claudeSessionID := <-m.claudeProcess.SessionID():
		if m.debug {
			log.Printf("Received Claude session ID from fd3: %s", claudeSessionID)
		}

		// Dual verification: also check that the session file exists
		if claude.WaitForSessionFile(m.metadata.Path, claudeSessionID, 5*time.Second) {
			m.claudeSessionID = claudeSessionID
			log.Printf("Claude session verified: %s", claudeSessionID)

			// Start session file scanner
			m.sessionScanner = claude.NewScanner(m.metadata.Path, claudeSessionID, m.debug)
			m.sessionScanner.Start()

			// Forward session messages to server
			go m.forwardSessionMessages()
		} else {
			if m.debug {
				log.Printf("Session file not found for UUID: %s (may be a different UUID)", claudeSessionID)
			}
			// Continue listening for more UUIDs
			go m.handleSessionIDDetection()
		}
	}
}

// handleThinkingState broadcasts thinking state changes to the server
func (m *Manager) handleThinkingState() {
	for {
		select {
		case <-m.stopCh:
			return
		case thinking, ok := <-m.claudeProcess.Thinking():
			if !ok {
				return
			}

			m.thinking = thinking

			if m.debug {
				log.Printf("Thinking state changed: %v", thinking)
			}

			// Broadcast to server via WebSocket
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
	}
}

// forwardSessionMessages sends Claude session messages to the server
func (m *Manager) forwardSessionMessages() {
	if m.sessionScanner == nil {
		return
	}

	for {
		select {
		case <-m.stopCh:
			return
		case msg, ok := <-m.sessionScanner.Messages():
			if !ok {
				return
			}

			if m.debug {
				log.Printf("Forwarding session message: type=%s uuid=%s", msg.Type, msg.UUID)
			}

			// Encrypt message content
			encrypted, err := m.encryptMessage(msg)
			if err != nil {
				if m.debug {
					log.Printf("Failed to encrypt message: %v", err)
				}
				continue
			}

			// Send to server via WebSocket
			if m.wsClient != nil && m.wsClient.IsConnected() {
				m.wsClient.EmitMessage(map[string]interface{}{
					"sid":     m.sessionID,
					"localId": msg.UUID,
					"message": encrypted,
				})
			}
		}
	}
}

// encryptMessage encrypts a session message for transmission
func (m *Manager) encryptMessage(msg *claude.SessionMessage) (string, error) {
	// Marshal the message to JSON
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt message: %w", err)
	}

	return encrypted, nil
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

	// Extract encrypted content - mobile app sends "message", web might send "content"
	// Accept both string and structured envelopes
	contentB64 := ""

	// message field could be string or map
	if msgVal, ok := data["message"]; ok {
		switch v := msgVal.(type) {
		case string:
			contentB64 = v
		case map[string]interface{}:
			if cVal, ok := v["content"]; ok {
				switch c := cVal.(type) {
				case string:
					contentB64 = c
				case map[string]interface{}:
					if inner, ok := c["c"].(string); ok {
						contentB64 = inner
					}
				}
			}
		}
	}

	// content field could be string or map
	if contentB64 == "" {
		if cVal, ok := data["content"]; ok {
			switch c := cVal.(type) {
			case string:
				contentB64 = c
			case map[string]interface{}:
				if inner, ok := c["c"].(string); ok {
					contentB64 = inner
				}
			}
		}
	}

	if contentB64 == "" {
		if m.debug {
			log.Println("Message has no usable content field")
		}
		return
	}

	// Decrypt the message content
	decrypted, err := m.decrypt(contentB64)
	if err != nil {
		if m.debug {
			log.Printf("Failed to decrypt message: %v", err)
		}
		return
	}

	// Parse the decrypted message
	// Mobile app sends: { "role": "user", "content": { "type": "text", "text": "message" } }
	var msg struct {
		Role    string `json:"role"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Meta map[string]interface{} `json:"meta,omitempty"`
	}
	if err := json.Unmarshal(decrypted, &msg); err != nil {
		if m.debug {
			log.Printf("Failed to parse decrypted message: %v", err)
		}
		return
	}

	if m.debug {
		log.Printf("Decrypted message: role=%s type=%s text=%s", msg.Role, msg.Content.Type, msg.Content.Text)
	}

	if msg.Role != "user" || msg.Content.Type != "text" || msg.Content.Text == "" {
		if m.debug {
			log.Printf("Ignoring message with empty or unsupported content: role=%s type=%s", msg.Role, msg.Content.Type)
		}
		return
	}

	if m.fakeAgent {
		m.sendFakeAgentResponse(msg.Content.Text)
		return
	}

	if m.acpClient != nil {
		text := msg.Content.Text
		if text == "" {
			return
		}
		go m.handleACPMessage(text)
		return
	}

	if m.agent == "codex" {
		if msg.Content.Text == "" {
			return
		}
		select {
		case m.codexQueue <- codexMessage{text: msg.Content.Text, meta: msg.Meta}:
		default:
			if m.debug {
				log.Printf("Codex queue full; dropping message")
			}
		}
		return
	}

	// Handle the message based on current mode
	m.modeMu.RLock()
	currentMode := m.mode
	m.modeMu.RUnlock()

	// Get the message content from the nested text field
	messageContent := msg.Content.Text
	if messageContent == "" {
		if m.debug {
			log.Printf("Empty message content, ignoring")
		}
		return
	}

	if currentMode == ModeLocal {
		// We're in local mode (terminal) - need to switch to remote mode
		log.Println("Received message while in local mode - switching to remote mode")

		// Switch to remote mode
		if err := m.SwitchToRemote(); err != nil {
			log.Printf("Failed to switch to remote mode: %v", err)
			return
		}

		// Now send the message to the remote bridge
		if err := m.SendUserMessage(messageContent, msg.Meta); err != nil {
			log.Printf("Failed to send message to remote bridge: %v", err)
		}
	} else {
		// Already in remote mode - forward to bridge
		if err := m.SendUserMessage(messageContent, msg.Meta); err != nil {
			log.Printf("Failed to send message to remote bridge: %v", err)
		}
	}
}

func (m *Manager) handleACPMessage(content string) {
	if m.acpClient == nil {
		return
	}

	m.thinking = true
	m.broadcastThinking(true)

	ctx := context.Background()
	result, err := m.acpClient.Run(ctx, m.acpSessionID, content)
	if err != nil {
		if m.debug {
			log.Printf("ACP run error: %v", err)
		}
		m.thinking = false
		m.broadcastThinking(false)
		return
	}
	if m.debug && result != nil {
		log.Printf("ACP run status: %s runId=%s awaiting=%v", result.Status, result.RunID, result.AwaitRequest != nil)
	}

	for result != nil && result.Status == "awaiting" && result.AwaitRequest != nil {
		if m.debug {
			log.Printf("ACP awaiting permission: runId=%s", result.RunID)
		}
		m.thinking = false
		m.broadcastThinking(false)

		resumePayload, ok := m.requestACPAwaitResume(result.AwaitRequest)
		if !ok {
			if m.debug {
				log.Printf("ACP await resume aborted")
			}
			return
		}
		if m.debug {
			log.Printf("ACP resuming run: %s", result.RunID)
		}
		result, err = m.acpClient.Resume(ctx, result.RunID, resumePayload)
		if err != nil {
			if m.debug {
				log.Printf("ACP resume error: %v", err)
			}
			return
		}
		if m.debug {
			log.Printf("ACP resume status: %s", result.Status)
		}

		m.thinking = true
		m.broadcastThinking(true)
	}

	if result != nil && result.OutputText != "" {
		if m.debug {
			log.Printf("ACP output: %q", result.OutputText)
		}
		m.sendAgentOutput(result.OutputText)
	}

	m.thinking = false
	m.broadcastThinking(false)
}

func (m *Manager) requestACPAwaitResume(awaitRequest map[string]interface{}) (map[string]interface{}, bool) {
	requestID, err := acp.NewUUID()
	if err != nil {
		if m.debug {
			log.Printf("ACP await request id error: %v", err)
		}
		return nil, false
	}

	payload, err := json.Marshal(map[string]interface{}{
		"await": awaitRequest,
	})
	if err != nil {
		return nil, false
	}

	response, err := m.handlePermissionRequest(requestID, "acp.await", payload)
	if err != nil {
		if m.debug {
			log.Printf("ACP permission error: %v", err)
		}
		return nil, false
	}
	if m.debug {
		log.Printf("ACP permission response received: requestId=%s", requestID)
	}

	allow := response != nil && response.Behavior == "allow"
	message := ""
	if response != nil {
		message = response.Message
	}

	return map[string]interface{}{
		"allow":   allow,
		"message": message,
	}, true
}

func (m *Manager) sendAgentOutput(text string) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	uuid := types.NewCUID()

	message := map[string]interface{}{
		"role":    "assistant",
		"model":   "acp",
		"content": []map[string]interface{}{{"type": "text", "text": text}},
	}
	payload := map[string]interface{}{
		"role": "agent",
		"content": map[string]interface{}{
			"type": "output",
			"data": map[string]interface{}{
				"type":             "assistant",
				"isSidechain":      false,
				"isCompactSummary": false,
				"isMeta":           false,
				"uuid":             uuid,
				"parentUuid":       nil,
				"message":          message,
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("ACP output marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		if m.debug {
			log.Printf("ACP output encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(map[string]interface{}{
		"sid":     m.sessionID,
		"message": encrypted,
	})
}

func (m *Manager) startCodex() error {
	client := codex.NewClient(m.workDir, m.debug)
	client.SetEventHandler(m.handleCodexEvent)
	client.SetPermissionHandler(m.handleCodexPermission)
	if err := client.Start(); err != nil {
		return fmt.Errorf("failed to start codex: %w", err)
	}
	m.codexClient = client

	m.modeMu.Lock()
	m.mode = ModeRemote
	m.modeMu.Unlock()
	m.state.ControlledByUser = false
	m.updateState()

	go m.runCodexLoop()

	log.Println("Codex MCP session ready")
	return nil
}

func (m *Manager) runCodexLoop() {
	for {
		select {
		case <-m.stopCh:
			return
		case <-m.codexStop:
			return
		case msg := <-m.codexQueue:
			if m.codexClient == nil {
				continue
			}
			permissionMode, model, modeChanged := m.resolveCodexMode(msg.meta)
			if modeChanged {
				m.codexClient.ClearSession()
				m.codexSessionActive = false
			}

			cfg := codex.SessionConfig{
				Prompt:         msg.text,
				ApprovalPolicy: codexApprovalPolicy(permissionMode),
				Sandbox:        codexSandbox(permissionMode),
				Cwd:            m.workDir,
				Model:          model,
			}

			ctx := context.Background()
			if !m.codexSessionActive {
				if _, err := m.codexClient.StartSession(ctx, cfg); err != nil {
					if m.debug {
						log.Printf("Codex start error: %v", err)
					}
					m.codexSessionActive = false
					continue
				}
				m.codexSessionActive = true
				m.codexPermissionMode = permissionMode
				m.codexModel = model
				continue
			}

			if _, err := m.codexClient.ContinueSession(ctx, msg.text); err != nil {
				if m.debug {
					log.Printf("Codex continue error: %v", err)
				}
				m.codexSessionActive = false
			}
		}
	}
}

func (m *Manager) resolveCodexMode(meta map[string]interface{}) (string, string, bool) {
	permissionMode := m.codexPermissionMode
	model := m.codexModel
	changed := false

	if permissionMode == "" {
		permissionMode = "default"
	}

	if meta != nil {
		if raw, ok := meta["permissionMode"]; ok {
			if value, ok := raw.(string); ok && value != "" {
				if value != permissionMode {
					permissionMode = value
					changed = true
				}
			}
		}

		if raw, ok := meta["model"]; ok {
			if raw == nil {
				if model != "" {
					model = ""
					changed = true
				}
			} else if value, ok := raw.(string); ok {
				if value != model {
					model = value
					changed = true
				}
			}
		}
	}

	return permissionMode, model, changed
}

func codexApprovalPolicy(permissionMode string) string {
	switch permissionMode {
	case "read-only":
		return "never"
	case "safe-yolo", "yolo":
		return "on-failure"
	default:
		return "untrusted"
	}
}

func codexSandbox(permissionMode string) string {
	switch permissionMode {
	case "read-only":
		return "read-only"
	case "yolo":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

func (m *Manager) handleCodexEvent(event map[string]interface{}) {
	if event == nil {
		return
	}

	evtType, _ := event["type"].(string)
	switch evtType {
	case "task_started":
		m.thinking = true
		m.broadcastThinking(true)
	case "task_complete", "turn_aborted":
		m.thinking = false
		m.broadcastThinking(false)
	}

	switch evtType {
	case "agent_message":
		message, _ := event["message"].(string)
		if message == "" {
			if raw, ok := event["message"]; ok {
				message = fmt.Sprint(raw)
			}
		}
		if message == "" {
			return
		}
		m.sendCodexRecord(map[string]interface{}{
			"type":    "message",
			"message": message,
		})
	case "agent_reasoning":
		text, _ := event["text"].(string)
		if text == "" {
			text, _ = event["message"].(string)
		}
		if text == "" {
			if raw, ok := event["text"]; ok {
				text = fmt.Sprint(raw)
			}
		}
		if text == "" {
			return
		}
		m.sendCodexRecord(map[string]interface{}{
			"type":    "reasoning",
			"message": text,
		})
	case "exec_command_begin", "exec_approval_request":
		callID := extractString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		input := filterMap(event, "type", "call_id", "callId")
		m.sendCodexRecord(map[string]interface{}{
			"type":   "tool-call",
			"callId": callID,
			"name":   "CodexBash",
			"input":  input,
			"id":     types.NewCUID(),
		})
	case "exec_command_end":
		callID := extractString(event, "call_id", "callId")
		if callID == "" {
			callID = types.NewCUID()
		}
		output := filterMap(event, "type", "call_id", "callId")
		m.sendCodexRecord(map[string]interface{}{
			"type":   "tool-call-result",
			"callId": callID,
			"output": output,
			"id":     types.NewCUID(),
		})
	}
}

func (m *Manager) handleCodexPermission(requestID string, toolName string, input map[string]interface{}) (*codex.PermissionDecision, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	response, err := m.handlePermissionRequest(requestID, toolName, payload)
	if err != nil {
		return &codex.PermissionDecision{
			Decision: "denied",
			Message:  err.Error(),
		}, nil
	}
	decision := "denied"
	if response != nil && response.Behavior == "allow" {
		decision = "approved"
	}
	message := ""
	if response != nil {
		message = response.Message
	}
	return &codex.PermissionDecision{
		Decision: decision,
		Message:  message,
	}, nil
}

func (m *Manager) sendCodexRecord(data map[string]interface{}) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	payload := map[string]interface{}{
		"role": "agent",
		"content": map[string]interface{}{
			"type": "codex",
			"data": data,
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("Codex marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(raw)
	if err != nil {
		if m.debug {
			log.Printf("Codex encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(map[string]interface{}{
		"sid":     m.sessionID,
		"message": encrypted,
	})
}

func extractString(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := data[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}

func filterMap(data map[string]interface{}, excludeKeys ...string) map[string]interface{} {
	if data == nil {
		return nil
	}
	exclude := map[string]struct{}{}
	for _, key := range excludeKeys {
		exclude[key] = struct{}{}
	}
	out := make(map[string]interface{}, len(data))
	for key, val := range data {
		if _, ok := exclude[key]; ok {
			continue
		}
		out[key] = val
	}
	return out
}

// handleUpdate handles structured "update" events (new-message, etc.)
func (m *Manager) handleUpdate(data map[string]interface{}) {
	if m.debug {
		log.Printf("Received update from server: %+v", data)
	}

	body, ok := data["body"].(map[string]interface{})
	if !ok {
		return
	}

	t, _ := body["t"].(string)
	switch t {
	case "new-message":
		// Extract cipher from structured content and delegate to handleMessage
		msgObj, ok := body["message"].(map[string]interface{})
		if !ok {
			return
		}
		contentObj, ok := msgObj["content"].(map[string]interface{})
		if !ok {
			return
		}
		cipher, _ := contentObj["c"].(string)
		if cipher == "" {
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
	default:
		// Other update types are ignored here
	}
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
			// Send machine keep-alive via machine-scoped socket
			if m.machineClient != nil && m.machineClient.IsConnected() {
				if err := m.machineClient.EmitRaw("machine-alive", map[string]interface{}{
					"machineId": m.machineID,
					"time":      time.Now().UnixMilli(),
				}); err != nil && m.debug {
					log.Printf("Machine keep-alive error: %v", err)
				}
			} else if m.debug {
				log.Printf("Machine keep-alive skipped (no machine socket)")
			}

		case <-sessionTicker.C:
			// Send session keep-alive
			if m.wsClient != nil && m.wsClient.IsConnected() {
				if err := m.wsClient.KeepSessionAlive(m.sessionID, m.thinking); err != nil && m.debug {
					log.Printf("Session keep-alive error: %v", err)
				}
			}
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

func (m *Manager) registerMachineRPCHandlers() {
	if m.machineRPC == nil {
		return
	}

	prefix := m.machineID + ":"

	m.machineRPC.RegisterHandler(prefix+"spawn-happy-session", func(params json.RawMessage) (json.RawMessage, error) {
		var req struct {
			Directory                    string `json:"directory"`
			ApprovedNewDirectoryCreation bool   `json:"approvedNewDirectoryCreation"`
			SessionID                    string `json:"sessionId"`
			MachineID                    string `json:"machineId"`
			Agent                        string `json:"agent"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		if req.Directory == "" {
			return nil, fmt.Errorf("directory is required")
		}

		if _, err := os.Stat(req.Directory); err != nil {
			if os.IsNotExist(err) {
				if !req.ApprovedNewDirectoryCreation {
					return json.Marshal(map[string]interface{}{
						"type":      "requestToApproveDirectoryCreation",
						"directory": req.Directory,
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
		if req.Agent == "claude" || req.Agent == "codex" {
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

		return json.Marshal(map[string]interface{}{
			"type":      "success",
			"sessionId": child.sessionID,
		})
	})

	m.machineRPC.RegisterHandler(prefix+"stop-session", func(params json.RawMessage) (json.RawMessage, error) {
		var req struct {
			SessionID string `json:"sessionId"`
		}
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
		return json.Marshal(map[string]string{"message": "Session stopped"})
	})

	m.machineRPC.RegisterHandler(prefix+"stop-daemon", func(params json.RawMessage) (json.RawMessage, error) {
		log.Printf("Stop-daemon requested")
		m.scheduleShutdown()
		m.forceExitAfter(2 * time.Second)
		return json.Marshal(map[string]string{"message": "Daemon stop request acknowledged, starting shutdown sequence..."})
	})

	m.machineRPC.RegisterHandler(prefix+"ping", func(params json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]bool{"success": true})
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

	resp, err := m.machineClient.EmitWithAck("machine-update-state", map[string]interface{}{
		"machineId":       m.machineID,
		"daemonState":     base64.StdEncoding.EncodeToString(encryptedState),
		"expectedVersion": m.machineStateVer,
	}, 5*time.Second)
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

	resp, err := m.machineClient.EmitWithAck("machine-update-metadata", map[string]interface{}{
		"machineId":       m.machineID,
		"metadata":        base64.StdEncoding.EncodeToString(encryptedMeta),
		"expectedVersion": m.machineMetaVer,
	}, 5*time.Second)
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

// GetClaudeSessionID returns the detected Claude session ID
func (m *Manager) GetClaudeSessionID() string {
	return m.claudeSessionID
}

// IsThinking returns whether Claude is currently thinking
func (m *Manager) IsThinking() bool {
	return m.thinking
}

// GetMode returns the current operation mode
func (m *Manager) GetMode() Mode {
	m.modeMu.RLock()
	defer m.modeMu.RUnlock()
	return m.mode
}

// SwitchToRemote switches to remote mode (mobile app control)
func (m *Manager) SwitchToRemote() error {
	m.modeMu.Lock()
	if m.mode == ModeRemote {
		m.modeMu.Unlock()
		return nil
	}
	m.mode = ModeRemote
	m.modeMu.Unlock()

	log.Println("Switching to remote mode...")

	// Kill the interactive Claude process
	if m.claudeProcess != nil {
		m.claudeProcess.Kill()
		m.claudeProcess = nil
	}

	// Stop session scanner
	if m.sessionScanner != nil {
		m.sessionScanner.Stop()
		m.sessionScanner = nil
	}

	// Start remote bridge
	bridge, err := claude.NewRemoteBridge(m.workDir, m.claudeSessionID, m.debug)
	if err != nil {
		m.modeMu.Lock()
		m.mode = ModeLocal
		m.modeMu.Unlock()
		return fmt.Errorf("failed to create remote bridge: %w", err)
	}

	// Set up message handler
	bridge.SetMessageHandler(m.handleRemoteMessage)

	// Set up permission handler
	bridge.SetPermissionHandler(m.handlePermissionRequest)

	if err := bridge.Start(); err != nil {
		m.modeMu.Lock()
		m.mode = ModeLocal
		m.modeMu.Unlock()
		return fmt.Errorf("failed to start remote bridge: %w", err)
	}

	m.remoteBridge = bridge

	// Update state to show we're in remote mode
	m.state.ControlledByUser = false
	m.updateState()

	log.Println("Remote mode active")
	return nil
}

// SwitchToLocal switches to local mode (terminal control)
func (m *Manager) SwitchToLocal() error {
	m.modeMu.Lock()
	if m.mode == ModeLocal {
		m.modeMu.Unlock()
		return nil
	}
	m.mode = ModeLocal
	m.modeMu.Unlock()

	log.Println("Switching to local mode...")

	// Kill the remote bridge
	if m.remoteBridge != nil {
		m.remoteBridge.Kill()
		m.remoteBridge = nil
	}

	// Start Claude process with fd 3 tracking
	claudeProc, err := claude.NewProcess(m.workDir, m.debug)
	if err != nil {
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		return fmt.Errorf("failed to create claude process: %w", err)
	}

	m.claudeProcess = claudeProc

	if err := claudeProc.Start(); err != nil {
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		return fmt.Errorf("failed to start claude: %w", err)
	}

	// Start session ID detection handler
	go m.handleSessionIDDetection()

	// Start thinking state handler
	go m.handleThinkingState()

	// Update state to show we're in local mode
	m.state.ControlledByUser = true
	m.updateState()

	log.Println("Local mode active")
	return nil
}

// SendUserMessage sends a user message to Claude (remote mode only)
func (m *Manager) SendUserMessage(content string, meta map[string]interface{}) error {
	m.modeMu.RLock()
	mode := m.mode
	bridge := m.remoteBridge
	m.modeMu.RUnlock()

	if mode != ModeRemote {
		return fmt.Errorf("not in remote mode")
	}

	if bridge == nil {
		return fmt.Errorf("remote bridge not running")
	}

	return bridge.SendUserMessage(content, meta)
}

// AbortRemote aborts the current remote query
func (m *Manager) AbortRemote() error {
	m.modeMu.RLock()
	bridge := m.remoteBridge
	m.modeMu.RUnlock()

	if bridge == nil {
		return fmt.Errorf("remote bridge not running")
	}

	return bridge.Abort()
}

// handleRemoteMessage processes messages from the remote bridge
func (m *Manager) handleRemoteMessage(msg *claude.RemoteMessage) error {
	if m.debug {
		log.Printf("Remote message: type=%s", msg.Type)
	}

	// Track thinking state from assistant messages
	if msg.Type == "assistant" {
		m.thinking = true
		m.broadcastThinking(true)
	} else if msg.Type == "result" {
		m.thinking = false
		m.broadcastThinking(false)
	}

	// Track session ID from system init
	if msg.Type == "system" && msg.SessionID != "" {
		m.claudeSessionID = msg.SessionID
	}

	// Forward to server
	if m.wsClient != nil && m.wsClient.IsConnected() {
		payload := m.buildRawRecordFromRemote(msg)
		if payload == nil {
			return nil
		}

		data, err := json.Marshal(payload)
		if err != nil {
			if m.debug {
				log.Printf("Failed to marshal remote payload: %v", err)
			}
			return nil
		}

		encrypted, err := m.encrypt(data)
		if err != nil {
			if m.debug {
				log.Printf("Failed to encrypt remote message: %v", err)
			}
			return nil
		}

		m.wsClient.EmitMessage(map[string]interface{}{
			"sid":     m.sessionID,
			"message": encrypted,
		})
	}

	// Send usage report if present
	if len(msg.Usage) > 0 && m.wsClient != nil && m.wsClient.IsConnected() {
		var usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		}
		if err := json.Unmarshal(msg.Usage, &usage); err == nil {
			total := usage.InputTokens + usage.OutputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
			_ = m.wsClient.EmitRaw("usage-report", map[string]interface{}{
				"key":       "claude-session",
				"sessionId": m.sessionID,
				"tokens": map[string]interface{}{
					"total":          total,
					"input":          usage.InputTokens,
					"output":         usage.OutputTokens,
					"cache_creation": usage.CacheCreationInputTokens,
					"cache_read":     usage.CacheReadInputTokens,
				},
				"cost": map[string]interface{}{
					"total":  0,
					"input":  0,
					"output": 0,
				},
			})
		}
	}

	return nil
}

// buildRawRecordFromRemote converts bridge RemoteMessage into the raw record
// format expected by the mobile app (matches RawRecordSchema on client).
func (m *Manager) buildRawRecordFromRemote(msg *claude.RemoteMessage) map[string]interface{} {
	// If bridge already sent structured payload, prefer that
	if len(msg.Message) > 0 {
		var existing map[string]interface{}
		if err := json.Unmarshal(msg.Message, &existing); err == nil {
			if wrapped := wrapLegacySDKMessage(existing); wrapped != nil {
				return wrapped
			}
			if isRawRecordMap(existing) {
				return existing
			}
			if msg.Type == "raw" {
				return nil
			}
		}
	}

	switch msg.Type {
	case "message":
		role := msg.Role
		contentBlocks := extractRemoteContentBlocks(msg.Content)
		if role == "" || len(contentBlocks) == 0 {
			return nil
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}

		if role == "assistant" {
			message := map[string]interface{}{
				"role":    "assistant",
				"model":   model,
				"content": contentBlocks,
			}
			if len(msg.Usage) > 0 {
				var usage interface{}
				if err := json.Unmarshal(msg.Usage, &usage); err == nil && usage != nil {
					message["usage"] = usage
				}
			}

			data := map[string]interface{}{
				"type":             "assistant",
				"isSidechain":      false,
				"isCompactSummary": false,
				"isMeta":           false,
				"uuid":             types.NewCUID(),
				"parentUuid":       nil,
				"message":          message,
			}
			if msg.ParentToolUseID != "" {
				data["parent_tool_use_id"] = msg.ParentToolUseID
			}

			return map[string]interface{}{
				"role": "agent",
				"content": map[string]interface{}{
					"type": "output",
					"data": data,
				},
			}
		}

		if role == "user" {
			return map[string]interface{}{
				"role": "agent",
				"content": map[string]interface{}{
					"type": "output",
					"data": map[string]interface{}{
						"type":             "user",
						"isSidechain":      false,
						"isCompactSummary": false,
						"isMeta":           false,
						"uuid":             types.NewCUID(),
						"parentUuid":       nil,
						"message": map[string]interface{}{
							"role":    "user",
							"content": contentBlocks,
						},
					},
				},
			}
		}

		return nil
	case "result":
		// Ignore result events to avoid duplicate assistant messages.
		return nil
	case "assistant":
		// Build minimal assistant message with a single text chunk
		uuid := types.NewCUID()

		text := extractRemoteContentText(msg.Content)
		if text == "" {
			if msg.Result != "" {
				text = msg.Result
			} else {
				return nil
			}
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}

		message := map[string]interface{}{
			"role":    "assistant",
			"model":   model,
			"content": []map[string]interface{}{{"type": "text", "text": text}},
		}
		if len(msg.Usage) > 0 {
			var usage interface{}
			if err := json.Unmarshal(msg.Usage, &usage); err == nil && usage != nil {
				message["usage"] = usage
			}
		}

		return map[string]interface{}{
			"role": "agent",
			"content": map[string]interface{}{
				"type": "output",
				"data": map[string]interface{}{
					"type":             "assistant",
					"isSidechain":      false,
					"isCompactSummary": false,
					"isMeta":           false,
					"uuid":             uuid,
					"parentUuid":       nil,
					"message":          message,
				},
			},
		}
	case "user":
		switch v := msg.Content.(type) {
		case nil:
			return nil
		case string:
			if v == "" {
				return nil
			}
		}
		return map[string]interface{}{
			"role": "user",
			"content": map[string]interface{}{
				"type": "text",
				"text": msg.Content,
			},
		}
	default:
		// Ignore unsupported types (system, control, etc.)
		return nil
	}
}

func isRawRecordMap(existing map[string]interface{}) bool {
	role, _ := existing["role"].(string)
	if role != "agent" && role != "user" {
		return false
	}
	content, ok := existing["content"].(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = content["type"].(string)
	return ok
}

func wrapLegacySDKMessage(existing map[string]interface{}) map[string]interface{} {
	msgType, _ := existing["type"].(string)
	if msgType != "message" {
		return nil
	}

	role, _ := existing["role"].(string)
	if role != "assistant" && role != "user" {
		return nil
	}

	rawContent, ok := existing["content"].([]interface{})
	if !ok || len(rawContent) == 0 {
		return nil
	}

	contentBlocks := make([]map[string]interface{}, 0, len(rawContent))
	for _, item := range rawContent {
		if block, ok := item.(map[string]interface{}); ok {
			contentBlocks = append(contentBlocks, block)
		}
	}
	if len(contentBlocks) == 0 {
		return nil
	}

	uuid := ""
	if id, _ := existing["id"].(string); id != "" {
		uuid = id
	} else {
		uuid = types.NewCUID()
	}

	message := map[string]interface{}{
		"role":    role,
		"content": contentBlocks,
	}

	if role == "assistant" {
		model, _ := existing["model"].(string)
		if model == "" {
			model = "unknown"
		}
		message["model"] = model
		if usage, ok := existing["usage"]; ok && usage != nil {
			message["usage"] = usage
		}
	}

	data := map[string]interface{}{
		"type":             map[bool]string{true: "assistant", false: "user"}[role == "assistant"],
		"isSidechain":      false,
		"isCompactSummary": false,
		"isMeta":           false,
		"uuid":             uuid,
		"parentUuid":       nil,
		"message":          message,
	}

	return map[string]interface{}{
		"role": "agent",
		"content": map[string]interface{}{
			"type": "output",
			"data": data,
		},
	}
}

func extractRemoteContentBlocks(content interface{}) []map[string]interface{} {
	switch v := content.(type) {
	case []map[string]interface{}:
		return v
	case []interface{}:
		blocks := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			if block, ok := item.(map[string]interface{}); ok {
				blocks = append(blocks, block)
			}
		}
		return blocks
	default:
		return nil
	}
}

func extractRemoteContentText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := block["type"].(string); t == "text" {
				if text, _ := block["text"].(string); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func (m *Manager) sendFakeAgentResponse(userText string) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	reply := fmt.Sprintf("fake-agent: %s", userText)
	uuid := types.NewCUID()

	message := map[string]interface{}{
		"role":    "assistant",
		"model":   "fake-agent",
		"content": []map[string]interface{}{{"type": "text", "text": reply}},
	}

	payload := map[string]interface{}{
		"role": "agent",
		"content": map[string]interface{}{
			"type": "output",
			"data": map[string]interface{}{
				"type":             "assistant",
				"isSidechain":      false,
				"isCompactSummary": false,
				"isMeta":           false,
				"uuid":             uuid,
				"parentUuid":       nil,
				"message":          message,
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("Fake agent marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		if m.debug {
			log.Printf("Fake agent encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(map[string]interface{}{
		"sid":     m.sessionID,
		"message": encrypted,
	})
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

// encrypt encrypts data using the session key
func (m *Manager) encrypt(data []byte) (string, error) {
	var encrypted []byte
	var err error

	if m.dataKey != nil {
		encrypted, err = crypto.EncryptWithDataKey(json.RawMessage(data), m.dataKey)
	} else {
		var secretKey [32]byte
		copy(secretKey[:], m.masterSecret)
		encrypted, err = crypto.EncryptLegacy(json.RawMessage(data), &secretKey)
	}
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// decrypt decrypts base64-encoded data using the session key
// Detects encryption format: AES-GCM (version byte 0) vs legacy SecretBox
func (m *Manager) decrypt(dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	if len(encrypted) == 0 {
		return nil, fmt.Errorf("empty encrypted data")
	}

	if m.debug {
		log.Printf("[decrypt] data length: %d, first bytes: %v", len(encrypted), encrypted[:min(10, len(encrypted))])
		log.Printf("[decrypt] dataKey set: %v, masterSecret set: %v", m.dataKey != nil, m.masterSecret != nil)
	}

	// Check if this is AES-GCM format (version byte 0)
	// AES-GCM format: [version(1)] [nonce(12)] [ciphertext+tag(16+)]
	// Minimum length: 1 + 12 + 16 = 29 bytes
	if encrypted[0] == 0 && len(encrypted) >= 29 {
		if m.debug {
			log.Printf("[decrypt] Detected AES-GCM format (version byte 0)")
		}
		// AES-GCM format - use dataKey if available, otherwise try master secret
		key := m.dataKey
		if key == nil {
			if m.debug {
				log.Printf("[decrypt] No dataKey, trying masterSecret for AES-GCM")
			}
			key = m.masterSecret
		}
		if key == nil {
			return nil, fmt.Errorf("AES-GCM encrypted data but no key available")
		}
		var result json.RawMessage
		if err := crypto.DecryptWithDataKey(encrypted, key, &result); err != nil {
			return nil, fmt.Errorf("failed to decrypt AES-GCM: %w", err)
		}
		return result, nil
	}

	if m.debug {
		log.Printf("[decrypt] Using legacy SecretBox format")
	}

	// Legacy SecretBox format: [nonce(24)] [ciphertext(16+)]
	var secretKey [32]byte
	if m.dataKey != nil {
		copy(secretKey[:], m.dataKey)
		if m.debug {
			log.Printf("[decrypt] Using dataKey, first 8 bytes: %v", m.dataKey[:8])
		}
	} else {
		copy(secretKey[:], m.masterSecret)
		if m.debug {
			log.Printf("[decrypt] Using masterSecret, first 8 bytes: %v", m.masterSecret[:8])
		}
	}

	var result json.RawMessage
	if err := crypto.DecryptLegacy(encrypted, &secretKey, &result); err != nil {
		return nil, fmt.Errorf("failed to decrypt SecretBox: %w", err)
	}

	return result, nil
}

func (m *Manager) encryptMachine(data []byte) (string, error) {
	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)
	encrypted, err := crypto.EncryptLegacy(json.RawMessage(data), &secretKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (m *Manager) decryptMachine(dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)

	var raw json.RawMessage
	if err := crypto.DecryptLegacy(encrypted, &secretKey, &raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (m *Manager) spawnRegistryPath() string {
	return filepath.Join(m.cfg.DelightHome, "spawned.sessions.json")
}

func (m *Manager) loadSpawnRegistryLocked() (*spawnRegistry, error) {
	registry := &spawnRegistry{Owners: make(map[string]map[string]spawnEntry)}
	data, err := os.ReadFile(m.spawnRegistryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return registry, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, registry); err != nil {
		return nil, err
	}
	if registry.Owners == nil {
		registry.Owners = make(map[string]map[string]spawnEntry)
	}
	return registry, nil
}

func (m *Manager) saveSpawnRegistryLocked(registry *spawnRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.spawnRegistryPath(), data, 0600)
}

func (m *Manager) registerSpawnedSession(sessionID, directory string) {
	if m.sessionID == "" {
		return
	}
	m.spawnStoreMu.Lock()
	defer m.spawnStoreMu.Unlock()

	registry, err := m.loadSpawnRegistryLocked()
	if err != nil {
		if m.debug {
			log.Printf("Failed to load spawn registry: %v", err)
		}
		return
	}

	owner := registry.Owners[m.sessionID]
	if owner == nil {
		owner = make(map[string]spawnEntry)
		registry.Owners[m.sessionID] = owner
	}
	owner[sessionID] = spawnEntry{SessionID: sessionID, Directory: directory}
	if err := m.saveSpawnRegistryLocked(registry); err != nil && m.debug {
		log.Printf("Failed to save spawn registry: %v", err)
	}
}

func (m *Manager) removeSpawnedSession(sessionID string) {
	if m.sessionID == "" {
		return
	}
	m.spawnStoreMu.Lock()
	defer m.spawnStoreMu.Unlock()

	registry, err := m.loadSpawnRegistryLocked()
	if err != nil {
		if m.debug {
			log.Printf("Failed to load spawn registry: %v", err)
		}
		return
	}

	owner := registry.Owners[m.sessionID]
	if owner == nil {
		return
	}
	delete(owner, sessionID)
	if len(owner) == 0 {
		delete(registry.Owners, m.sessionID)
	}
	if err := m.saveSpawnRegistryLocked(registry); err != nil && m.debug {
		log.Printf("Failed to save spawn registry: %v", err)
	}
}

func (m *Manager) restoreSpawnedSessions() error {
	if m.sessionID == "" {
		return nil
	}

	m.spawnStoreMu.Lock()
	registry, err := m.loadSpawnRegistryLocked()
	m.spawnStoreMu.Unlock()
	if err != nil {
		return err
	}

	owner := registry.Owners[m.sessionID]
	if len(owner) == 0 {
		return nil
	}

	for _, entry := range owner {
		if entry.Directory == "" || entry.SessionID == "" {
			continue
		}
		if entry.SessionID == m.sessionID {
			continue
		}

		m.spawnMu.Lock()
		_, exists := m.spawnedSessions[entry.SessionID]
		m.spawnMu.Unlock()
		if exists {
			continue
		}

		child, err := NewManager(m.cfg, m.token, m.debug)
		if err != nil {
			if m.debug {
				log.Printf("Failed to restore session %s: %v", entry.SessionID, err)
			}
			continue
		}
		child.disableMachineSocket = true

		if err := child.Start(entry.Directory); err != nil {
			if m.debug {
				log.Printf("Failed to start restored session %s: %v", entry.SessionID, err)
			}
			continue
		}
		if child.sessionID == "" {
			continue
		}

		m.spawnMu.Lock()
		m.spawnedSessions[child.sessionID] = child
		m.spawnMu.Unlock()

		if child.sessionID != entry.SessionID {
			m.registerSpawnedSession(child.sessionID, entry.Directory)
		}

		go func(sessionID string, mgr *Manager) {
			_ = mgr.Wait()
			m.spawnMu.Lock()
			delete(m.spawnedSessions, sessionID)
			m.spawnMu.Unlock()
		}(child.sessionID, child)
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

// encryptRemoteMessage encrypts a remote message for transmission
func (m *Manager) encryptRemoteMessage(msg *claude.RemoteMessage) (string, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return m.encrypt(data)
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
		if err := m.AbortRemote(); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"success": true})
	})

	// Switch handler - switch between local/remote modes
	m.rpcManager.RegisterHandler(prefix+"switch", func(params json.RawMessage) (json.RawMessage, error) {
		var req struct {
			Mode string `json:"mode"`
		}
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
		var req struct {
			RequestID string `json:"requestId"`
			Allow     bool   `json:"allow"`
			Message   string `json:"message"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}

		m.HandlePermissionResponse(req.RequestID, req.Allow, req.Message)
		return json.Marshal(map[string]bool{"success": true})
	})
}
