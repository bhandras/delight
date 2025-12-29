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

type remoteInputRecord struct {
	text string
	at   time.Time
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

func normalizeRemoteInputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	return text
}

func (m *Manager) rememberRemoteInput(text string) {
	text = normalizeRemoteInputText(text)
	if text == "" {
		return
	}

	now := time.Now()

	m.recentRemoteInputsMu.Lock()
	defer m.recentRemoteInputsMu.Unlock()

	// Keep a small, time-bounded list; most sessions only need a handful of recent inputs.
	const maxItems = 64
	const ttl = 20 * time.Second

	// Drop expired entries.
	cutoff := now.Add(-ttl)
	dst := m.recentRemoteInputs[:0]
	for _, rec := range m.recentRemoteInputs {
		if rec.at.After(cutoff) {
			dst = append(dst, rec)
		}
	}
	m.recentRemoteInputs = dst

	m.recentRemoteInputs = append(m.recentRemoteInputs, remoteInputRecord{text: text, at: now})
	if len(m.recentRemoteInputs) > maxItems {
		m.recentRemoteInputs = m.recentRemoteInputs[len(m.recentRemoteInputs)-maxItems:]
	}
}

func (m *Manager) isRecentlyInjectedRemoteInput(text string) bool {
	text = normalizeRemoteInputText(text)
	if text == "" {
		return false
	}

	now := time.Now()
	const ttl = 20 * time.Second
	cutoff := now.Add(-ttl)

	m.recentRemoteInputsMu.Lock()
	defer m.recentRemoteInputsMu.Unlock()

	// Iterate newest-first.
	for i := len(m.recentRemoteInputs) - 1; i >= 0; i-- {
		rec := m.recentRemoteInputs[i]
		if rec.at.Before(cutoff) {
			break
		}
		if rec.text == text {
			return true
		}
	}
	return false
}

func extractClaudeUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	var walk func(any) string
	walk = func(v any) string {
		switch t := v.(type) {
		case string:
			return t
		case map[string]any:
			// Common shapes we have seen:
			// - {"content":"..."}
			// - {"content":[{"type":"text","text":"..."}]}
			// - {"text":"..."}
			if content, ok := t["content"]; ok {
				if s := walk(content); s != "" {
					return s
				}
			}
			if text, ok := t["text"]; ok {
				if s := walk(text); s != "" {
					return s
				}
			}
			if message, ok := t["message"]; ok {
				if s := walk(message); s != "" {
					return s
				}
			}
			if data, ok := t["data"]; ok {
				if s := walk(data); s != "" {
					return s
				}
			}
			return ""
		case []any:
			for _, part := range t {
				if s := walk(part); s != "" {
					return s
				}
			}
			return ""
		default:
			return ""
		}
	}

	return normalizeRemoteInputText(walk(value))
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
			if !m.enqueueInbound(func() {
				m.claudeSessionID = claudeSessionID
				log.Printf("Claude session verified: %s", claudeSessionID)

				// Start session file scanner
				m.sessionScanner = claude.NewScanner(m.metadata.Path, claudeSessionID, m.debug)
				m.sessionScanner.Start()

				// Forward session messages to server
				go m.forwardSessionMessages()
			}) {
				return
			}
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
			if !m.enqueueInbound(func() {
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
			}) {
				return
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

			// Detach from the scanner's internal buffers/slices so the inbound queue
			// processes an immutable snapshot.
			payload, err := json.Marshal(msg)
			if err != nil {
				if m.debug {
					log.Printf("Failed to marshal scanner message: %v", err)
				}
				continue
			}
			var copyMsg claude.SessionMessage
			if err := json.Unmarshal(payload, &copyMsg); err != nil {
				if m.debug {
					log.Printf("Failed to unmarshal scanner message: %v", err)
				}
				continue
			}

			if !m.enqueueInbound(func() { m.forwardSessionMessageInbound(&copyMsg) }) {
				if m.debug {
					log.Printf("Inbound queue full; dropping scanner message uuid=%s type=%s", copyMsg.UUID, copyMsg.Type)
				}
			}
		}
	}
}

func (m *Manager) forwardSessionMessageInbound(msg *claude.SessionMessage) {
	if msg == nil {
		return
	}

	if m.debug {
		log.Printf("Forwarding session message: type=%s uuid=%s", msg.Type, msg.UUID)
	}

	// Suppress forwarding "user" messages that came from the remote bridge.
	// The remote/mobile bridge already sent that user text to the server; Claude's
	// session file will contain a copy which we would otherwise forward back and
	// cause duplicates in the UI.
	if msg.Type == "user" {
		userText := extractClaudeUserText(msg.Message)
		if m.isRecentlyInjectedRemoteInput(userText) {
			if m.debug {
				log.Printf("Skipping echoed remote user input from scanner: %q", userText)
			}
			return
		}
	}

	encrypted, err := m.encryptMessage(msg)
	if err != nil {
		if m.debug {
			log.Printf("Failed to encrypt message: %v", err)
		}
		return
	}

	if m.wsClient != nil && m.wsClient.IsConnected() {
		m.wsClient.EmitMessage(map[string]interface{}{
			"sid":     m.sessionID,
			"localId": msg.UUID,
			"message": encrypted,
		})
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
	bridge.SetPermissionHandler(m.handleRemotePermission)

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

func (m *Manager) handleRemotePermission(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	// IMPORTANT: do not run this on the inbound queue.
	//
	// Permission requests block waiting for a mobile response. If we block the inbound
	// queue, we can deadlock because permission responses are delivered via queued
	// inbound RPC handlers.
	if input == nil {
		input = json.RawMessage("null")
	} else {
		// Detach from caller-owned bytes to avoid concurrent mutation.
		input = append(json.RawMessage(nil), input...)
	}
	return m.handlePermissionRequest(requestID, toolName, input)
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
	if msg == nil {
		return nil
	}

	// Detach from caller-owned struct/slices to avoid concurrent mutation issues.
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	var copyMsg claude.RemoteMessage
	if err := json.Unmarshal(payload, &copyMsg); err != nil {
		return err
	}

	return m.runInboundErr(func() error { return m.handleRemoteMessageInbound(&copyMsg) })
}

func (m *Manager) handleRemoteMessageInbound(msg *claude.RemoteMessage) error {
	if msg == nil {
		return nil
	}

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

	m.renderRemoteMessage(msg)

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

func (m *Manager) renderRemoteMessage(msg *claude.RemoteMessage) {
	if m.GetMode() != ModeRemote {
		return
	}

	switch msg.Type {
	case "raw":
		if len(msg.Message) == 0 {
			return
		}
		var record map[string]interface{}
		if err := json.Unmarshal(msg.Message, &record); err != nil {
			return
		}
		printRawRecord(record)
	case "error":
		if msg.Error != "" {
			fmt.Fprintf(os.Stdout, "claude error: %s\n", msg.Error)
		}
	}
}

func printRawRecord(record map[string]interface{}) {
	role, _ := record["role"].(string)
	if role != "agent" {
		return
	}
	content, _ := record["content"].(map[string]interface{})
	if content == nil {
		return
	}
	contentType, _ := content["type"].(string)
	if contentType != "output" {
		return
	}
	data, _ := content["data"].(map[string]interface{})
	if data == nil {
		return
	}
	message, _ := data["message"].(map[string]interface{})
	if message == nil {
		return
	}
	msgRole, _ := message["role"].(string)
	blocks := extractRemoteContentBlocks(message["content"])
	if msgRole == "" || len(blocks) == 0 {
		return
	}

	prefix := "you> "
	if msgRole == "assistant" {
		prefix = "claude> "
	}

	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if text, _ := block["text"].(string); text != "" {
				fmt.Fprintln(os.Stdout, prefix+text)
			}
		case "tool_use":
			name, _ := block["name"].(string)
			id, _ := block["id"].(string)
			summary := formatToolInput(block["input"])
			line := prefix + "[tool] " + name
			if id != "" {
				line += " " + id
			}
			if summary != "" {
				line += " - " + summary
			}
			fmt.Fprintln(os.Stdout, line)
		case "tool_result":
			summary := formatToolResult(block["content"])
			line := prefix + "[tool_result]"
			if summary != "" {
				line += " " + summary
			}
			fmt.Fprintln(os.Stdout, line)
		}
	}
}

func formatToolInput(input interface{}) string {
	if input == nil {
		return ""
	}
	if inputMap, ok := input.(map[string]interface{}); ok {
		if cmd, _ := inputMap["command"].(string); cmd != "" {
			return cmd
		}
		if query, _ := inputMap["query"].(string); query != "" {
			return query
		}
		if url, _ := inputMap["url"].(string); url != "" {
			return url
		}
	}
	blob, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	s := string(blob)
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func formatToolResult(content interface{}) string {
	switch v := content.(type) {
	case string:
		if len(v) > 160 {
			return v[:160] + "..."
		}
		return v
	case []interface{}:
		if len(v) == 0 {
			return ""
		}
		if text, ok := v[0].(map[string]interface{}); ok {
			if t, _ := text["type"].(string); t == "text" {
				if val, _ := text["text"].(string); val != "" {
					if len(val) > 160 {
						return val[:160] + "..."
					}
					return val
				}
			}
		}
	}
	return ""
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
