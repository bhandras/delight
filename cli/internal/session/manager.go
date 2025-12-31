package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
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
	"github.com/bhandras/delight/protocol/wire"
	"golang.org/x/term"
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
	stateMu              sync.Mutex
	stateDirty           bool
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
	localRunMu      sync.Mutex
	localRunCancel  chan struct{}

	// Pending permission requests (for remote mode)
	pendingPermissions map[string]*pendingPermission
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

	// recentOutboundUserLocalIDs tracks local idempotency keys used when this
	// session forwards user messages up to the server (typically from the Claude
	// session scanner). These messages can later be echoed back through the
	// server's update stream; we suppress re-injecting them into the local agent.
	recentOutboundUserLocalIDsMu sync.Mutex
	recentOutboundUserLocalIDs   []outboundLocalIDRecord

	// desktopTakebackCancel is used to stop the "press any key to take back control"
	// watcher while the session is in remote mode.
	desktopTakebackMu     sync.Mutex
	desktopTakebackCancel chan struct{}
	desktopTakebackTTY    *os.File
	desktopTakebackDone   chan struct{}
	desktopTakebackState  *term.State
}

type codexMessage struct {
	text string
	meta map[string]interface{}
}

type pendingPermission struct {
	ch       chan *claude.PermissionResponse
	toolName string
	input    json.RawMessage
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
		pendingPermissions: make(map[string]*pendingPermission),
		spawnedSessions:    make(map[string]*Manager),
		state: &types.AgentState{
			ControlledByUser:  true,
			Requests:          make(map[string]types.AgentPendingRequest),
			CompletedRequests: make(map[string]types.AgentCompletedRequest),
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
	m.pendingPermissions[requestID] = &pendingPermission{
		ch:       responseCh,
		toolName: toolName,
		// Detach from caller-owned bytes.
		input: append(json.RawMessage(nil), input...),
	}
	m.permissionMu.Unlock()

	// Persist the request for reconnect durability and terminal list indicators.
	m.stateMu.Lock()
	if m.state.Requests == nil {
		m.state.Requests = make(map[string]types.AgentPendingRequest)
	}
	m.state.Requests[requestID] = types.AgentPendingRequest{
		ToolName:  toolName,
		Input:     string(input),
		CreatedAt: time.Now().UnixMilli(),
	}
	m.stateMu.Unlock()
	go m.updateState()

	// Send permission request to mobile app via RPC.
	//
	// Do not gate on IsConnected here; permission requests can race the initial
	// Socket.IO handshake, and Socket.IO will queue outbound emits.
	if m.wsClient != nil {
		if err := m.wsClient.EmitEphemeral(wire.PermissionRequestEphemeralPayload{
			Type:      "permission-request",
			ID:        m.sessionID,
			RequestID: requestID,
			ToolName:  toolName,
			Input:     string(input),
		}); err != nil && m.debug {
			log.Printf("Failed to send permission request: %v", err)
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
		m.clearPendingRequestState(requestID, false, "Permission request timed out")
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Permission request timed out",
		}, nil
	case <-m.stopCh:
		m.clearPendingRequestState(requestID, false, "Session closed")
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Session closed",
		}, nil
	}
}

// HandlePermissionResponse handles a permission response from the mobile app.
func (m *Manager) HandlePermissionResponse(requestID string, allow bool, message string) {
	m.permissionMu.Lock()
	pending, ok := m.pendingPermissions[requestID]
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

	response := &claude.PermissionResponse{
		Behavior: "deny",
		Message:  message,
	}
	if allow {
		// Claude Code SDK expects allow responses to include `updatedInput` (even
		// when unmodified). Mirror Happy’s behavior by echoing the requested input.
		response.Behavior = "allow"
		response.UpdatedInput = pending.input
		// Only Claude tool permissions require the strict allow schema. Other
		// agents (e.g. ACP) want to preserve the user message for downstream
		// acknowledgements.
		if m.agent == "claude" {
			response.Message = ""
		}
	}

	pending.ch <- response

	m.clearPendingRequestState(requestID, allow, message)

	if m.debug {
		log.Printf("Permission response delivered: %s allow=%v", requestID, allow)
	}
}

func (m *Manager) clearPendingRequestState(requestID string, allow bool, message string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	toolName := ""
	input := ""
	if m.state.Requests != nil {
		if prev, ok := m.state.Requests[requestID]; ok {
			toolName = prev.ToolName
			input = prev.Input
		}
	}

	if m.state.Requests != nil {
		delete(m.state.Requests, requestID)
	}
	if m.state.CompletedRequests == nil {
		m.state.CompletedRequests = make(map[string]types.AgentCompletedRequest)
	}
	if len(m.state.CompletedRequests) > 200 {
		m.state.CompletedRequests = make(map[string]types.AgentCompletedRequest)
	}

	m.state.CompletedRequests[requestID] = types.AgentCompletedRequest{
		ToolName:   toolName,
		Input:      input,
		Allow:      allow,
		Message:    message,
		ResolvedAt: time.Now().UnixMilli(),
	}
	go m.updateState()
}

// broadcastThinking broadcasts thinking state to connected clients.
func (m *Manager) broadcastThinking(thinking bool) {
	if m.wsClient != nil && m.wsClient.IsConnected() {
		m.wsClient.EmitEphemeral(wire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       m.sessionID,
			Active:   true,
			Thinking: thinking,
			ActiveAt: time.Now().UnixMilli(),
		})
	}
}

// updateState sends state update to server.
func (m *Manager) updateState() {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if m.wsClient == nil || !m.wsClient.IsConnected() {
		m.stateDirty = true
		return
	}

	// Persist agent state as plaintext JSON.
	//
	// The server stores `agentState` as an opaque string and clients (including the
	// iOS harness) parse it as JSON. Encrypting it here breaks parity because the
	// phone cannot derive `controlledByUser` and other UI-critical fields.
	stateData, err := json.Marshal(m.state)
	if err != nil {
		return
	}

	agentState := string(stateData)

	// Try once, and retry a version mismatch once with the server-provided version.
	for attempt := 0; attempt < 2; attempt++ {
		expectedVersion := m.stateVersion
		newVersion, err := m.wsClient.UpdateState(m.sessionID, agentState, expectedVersion)
		if err != nil {
			if errors.Is(err, websocket.ErrVersionMismatch) {
				if newVersion > 0 {
					m.stateVersion = newVersion
				}
				m.stateDirty = true
				continue
			}
			m.stateDirty = true
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
		m.stateDirty = false
		return
	}
}
