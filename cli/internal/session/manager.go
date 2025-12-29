package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/acp"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/codex"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/session/runtime"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
)

// Mode represents the current operation mode.
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeRemote Mode = "remote"
)

// Manager manages a Delight session with advanced Claude tracking.
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

	rt *runtime.Runtime

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

// NewManager creates a new session manager.
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

// GetClaudeSessionID returns the detected Claude session ID.
func (m *Manager) GetClaudeSessionID() string {
	return m.claudeSessionID
}

// IsThinking returns whether Claude is currently thinking.
func (m *Manager) IsThinking() bool {
	return m.thinking
}

// handlePermissionRequest processes tool permission requests from Claude.
func (m *Manager) handlePermissionRequest(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	if m.debug {
		log.Printf("Permission request: %s tool=%s", requestID, toolName)
	}

	// Store the request and wait for response
	responseCh := make(chan *claude.PermissionResponse, 1)
	m.permissionMu.Lock()
	m.pendingPermissions[requestID] = responseCh
	m.permissionMu.Unlock()

	// Send permission request to mobile app via RPC
	if m.wsClient != nil && m.wsClient.IsConnected() {
		if err := m.wsClient.EmitRaw("permission-request", map[string]interface{}{
			"sid":       m.sessionID,
			"requestId": requestID,
			"toolName":  toolName,
			"input":     string(input),
		}); err != nil {
			if m.debug {
				log.Printf("Failed to send permission request: %v", err)
			}
		}
	}

	// Wait for response (with timeout)
	select {
	case resp := <-responseCh:
		return resp, nil
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

// HandlePermissionResponse handles a permission response from the mobile app.
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

// broadcastThinking broadcasts thinking state to connected clients.
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

// updateState sends state update to server.
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
