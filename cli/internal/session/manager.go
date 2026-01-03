package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/config"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/cli/internal/session/runtime"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
)

// Mode represents the current operation mode.
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeRemote Mode = "remote"
)

// Manager manages a Delight session with advanced Claude tracking.
type Manager struct {
	cfg                   *config.Config
	token                 string
	terminalID            string
	sessionID             string
	sessionTag            string
	dataKey               []byte
	masterSecret          []byte
	wsClient              *websocket.Client
	rpcManager            *websocket.RPCManager
	terminalClient        *websocket.Client
	terminalRPC           *websocket.RPCManager
	terminalMetaVer       int64
	terminalStateVer      int64
	disableTerminalSocket bool
	metadata              *types.Metadata
	metaVersion           int64
	terminalMetadata      *types.TerminalMetadata
	terminalState         *types.DaemonState
	debug                 bool
	fakeAgent             bool
	acpSessionID          string
	acpAgent              string
	agent                 string

	workDir string

	thinking bool
	stopCh   chan struct{}

	// Pending permission requests (for remote mode)
	// pendingPermissions was previously used to coordinate synchronous tool
	// permission prompts. This is now owned by the SessionActor FSM.

	spawnActor        *framework.Actor[spawnActorState]
	spawnActorRuntime *spawnActorRuntime

	shutdownOnce sync.Once

	rt *runtime.Runtime

	// sessionActor owns the session's agent-state persistence logic (Phase 4 wiring).
	// Additional responsibilities (mode switching, permission promises) will be migrated
	// into this actor in subsequent phases.
	sessionActor        *framework.Actor[sessionactor.State]
	sessionActorRuntime *sessionactor.Runtime

	sessionActorClosedOnce sync.Once
	sessionActorClosed     chan struct{}

	lastTerminalKeepAliveSkipAt time.Time
}

// NewManager creates a new session manager.
func NewManager(cfg *config.Config, token string, debug bool) (*Manager, error) {
	// Get or create master secret
	masterSecret, err := storage.GetOrCreateSecretKey(filepath.Join(cfg.DelightHome, "master.key"))
	if err != nil {
		return nil, fmt.Errorf("failed to get master secret: %w", err)
	}

	agent := cfg.Agent
	if cfg.FakeAgent {
		agent = "fake"
	}

	return &Manager{
		cfg:          cfg,
		token:        token,
		masterSecret: masterSecret,
		debug:        debug,
		fakeAgent:    cfg.FakeAgent,
		acpAgent:     cfg.ACPAgent,
		agent:        agent,
		stopCh:       make(chan struct{}),
		// Permission requests are owned by the SessionActor.
		sessionActorClosed: make(chan struct{}),
	}, nil
}

// GetClaudeSessionID returns the detected Claude session ID.
func (m *Manager) GetClaudeSessionID() string {
	if m.sessionActor == nil {
		return ""
	}
	return m.sessionActor.State().ClaudeSessionID
}

// IsThinking returns whether Claude is currently thinking.
func (m *Manager) IsThinking() bool {
	return m.thinking
}

// handlePermissionRequest processes tool permission requests from Claude.
func (m *Manager) handlePermissionRequest(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	if m.debug {
		logger.Debugf("Permission request: %s tool=%s", requestID, toolName)
	}

	if m.sessionActor == nil {
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Session actor not initialized",
		}, nil
	}

	decisionCh := make(chan sessionactor.PermissionDecision, 1)
	nowMs := time.Now().UnixMilli()
	if !m.sessionActor.Enqueue(sessionactor.AwaitPermission(requestID, toolName, input, nowMs, decisionCh)) {
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Failed to schedule permission request",
		}, nil
	}

	// Wait for response (with timeout). This blocks only the caller goroutine,
	// not the actor loop.
	select {
	case decision := <-decisionCh:
		response := &claude.PermissionResponse{
			Behavior: "deny",
			Message:  decision.Message,
		}
		if decision.Allow {
			response.Behavior = "allow"
			// Claude Code SDK expects allow responses to include updatedInput (even
			// when unmodified). Mirror legacy behavior by echoing the requested input.
			response.UpdatedInput = append(json.RawMessage(nil), input...)
		}
		return response, nil
	case <-time.After(5 * time.Minute):
		// Best-effort: mark as denied and clear durable request state.
		_ = m.sessionActor.Enqueue(sessionactor.SubmitPermissionDecision(
			requestID,
			false,
			"Permission request timed out",
			time.Now().UnixMilli(),
			nil,
		))
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Permission request timed out",
		}, nil
	case <-m.stopCh:
		_ = m.sessionActor.Enqueue(sessionactor.SubmitPermissionDecision(
			requestID,
			false,
			"Session closed",
			time.Now().UnixMilli(),
			nil,
		))
		return &claude.PermissionResponse{
			Behavior: "deny",
			Message:  "Session closed",
		}, nil
	}
}

// HandlePermissionResponse handles a permission response from the mobile app.
func (m *Manager) HandlePermissionResponse(requestID string, allow bool, message string) {
	if m.sessionActor == nil {
		return
	}
	_ = m.sessionActor.Enqueue(sessionactor.SubmitPermissionDecision(
		requestID,
		allow,
		message,
		time.Now().UnixMilli(),
		nil,
	))
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
