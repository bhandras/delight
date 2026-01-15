package session

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/notify"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/cli/internal/session/runtime"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
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
	sessionAgentStateJSON string
	sessionAgentStateVer  int64
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
	terminalMetadata      *types.TerminalMetadata
	terminalState         *types.DaemonState
	debug                 bool
	fakeAgent             bool
	acpSessionID          string
	acpAgent              string
	agent                 string

	workDir string

	working bool
	stopCh  chan struct{}

	pushover *notify.PushoverNotifier

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

// IsWorking returns whether an agent is currently working.
func (m *Manager) IsWorking() bool {
	return m.working
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

// broadcastWorking broadcasts working state to connected clients.
func (m *Manager) broadcastWorking(working bool) {
	if m.wsClient != nil && m.wsClient.IsConnected() {
		m.wsClient.EmitEphemeral(wire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       m.sessionID,
			Active:   true,
			Working:  working,
			ActiveAt: time.Now().UnixMilli(),
		})
	}
}
