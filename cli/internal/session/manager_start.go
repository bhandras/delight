package session

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine/codexengine"
	cliauth "github.com/bhandras/delight/cli/internal/cli"
	"github.com/bhandras/delight/cli/internal/crypto"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/version"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// dataEncryptionKeyBytes is the byte length for session/terminal data keys.
	dataEncryptionKeyBytes = 32

	// restartExitCode is the process exit code used to request a wrapper-managed
	// restart. This mirrors sysexits(3) EX_TEMPFAIL (75) by convention.
	restartExitCode = 75
)

// managerSleep is a test seam for time.Sleep calls used during shutdown.
var managerSleep = time.Sleep

// managerExit is a test seam for process termination.
var managerExit = os.Exit

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

	// Get or create stable terminal ID scoped to this workdir.
	terminalID, err := storage.GetOrCreateTerminalID(m.cfg.DelightHome, workDir)
	if err != nil {
		return fmt.Errorf("failed to get terminal ID: %w", err)
	}
	m.terminalID = terminalID

	// Initialize metadata
	hostname, _ := os.Hostname()
	homeDir, _ := os.UserHomeDir()

	m.metadata = &types.Metadata{
		Path:           workDir,
		Host:           hostname,
		Version:        version.Version(),
		OS:             "darwin", // TODO: detect OS
		TerminalID:     terminalID,
		HomeDir:        homeDir,
		DelightHomeDir: m.cfg.DelightHome,
		Flavor:         m.agent,
	}

	// Initialize terminal metadata (best-effort)
	m.terminalMetadata = &types.TerminalMetadata{
		Host:              hostname,
		Platform:          "darwin", // TODO: detect platform
		DelightCliVersion: version.Version(),
		HomeDir:           homeDir,
		DelightHomeDir:    m.cfg.DelightHome,
	}

	// Initialize daemon state
	m.terminalState = &types.DaemonState{
		Status:    "running",
		PID:       os.Getpid(),
		StartedAt: time.Now().UnixMilli(),
	}

	// Create or update terminal on server
	if err := m.createTerminal(); err != nil {
		return fmt.Errorf("failed to create terminal: %w", err)
	}

	logger.Infof("Terminal registered: %s", m.terminalID)

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
	m.wsClient.SetTokenRefresher(func() (string, error) {
		// Refresh the on-disk token and return it so the websocket can reconnect
		// with fresh auth.
		//
		// We use the in-memory masterSecret and avoid loading it from disk.
		return cliauth.RefreshAccessToken(m.cfg, m.masterSecret)
	})
	// Re-wire the session actor runtime with the now-constructed websocket client.
	// initSessionActor is idempotent and updates runtime adapters when actor exists.
	m.initSessionActor()

	// Bridge socket lifecycle into the SessionActor so connection state and
	// persistence retries are deterministic.
	if m.sessionActor != nil {
		wireSessionActorToSocket(m.sessionActor, m.wsClient)
	}

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
			thinking := m.thinking
			if m.sessionActor != nil {
				thinking = m.sessionActor.State().Thinking
			}
			_ = m.wsClient.KeepSessionAlive(m.sessionID, thinking)
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

	// Connect terminal-scoped WebSocket (best-effort)
	if !m.disableTerminalSocket {
		m.terminalClient = websocket.NewTerminalClient(m.cfg.ServerURL, m.token, m.terminalID, m.cfg.SocketIOTransport, m.debug)
		m.terminalClient.SetTokenRefresher(func() (string, error) {
			return cliauth.RefreshAccessToken(m.cfg, m.masterSecret)
		})
		if m.sessionActor != nil {
			wireSessionActorToTerminalSocket(m.sessionActor, m.terminalClient)
		}
		if err := m.terminalClient.Connect(); err != nil {
			logger.Warnf("Terminal WebSocket connection failed: %v", err)
			m.terminalClient = nil
		} else {
			m.terminalRPC = websocket.NewRPCManager(m.terminalClient, m.debug)
			m.terminalRPC.SetEncryption(m.encryptTerminal, m.decryptTerminal)
			m.terminalRPC.SetupSocketHandlers(m.terminalClient.RawSocket())
			m.registerTerminalRPCHandlers()

			if m.terminalClient.WaitForConnect(5 * time.Second) {
				if m.debug {
					logger.Infof("Terminal WebSocket connected")
				}
				m.terminalRPC.RegisterAll()
				_ = m.terminalClient.EmitRaw("terminal-alive", wire.TerminalAlivePayload{
					TerminalID: m.terminalID,
					Time:       time.Now().UnixMilli(),
				})
				if err := m.updateTerminalState(); err != nil && m.debug {
					logger.Warnf("Terminal state update error: %v", err)
				}
				if err := m.updateTerminalMetadata(); err != nil && m.debug {
					logger.Warnf("Terminal metadata update error: %v", err)
				}
			} else if m.debug {
				logger.Warnf("Terminal WebSocket connection timeout")
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
			if m.cfg != nil {
				m.sessionActorRuntime.WithDelightHome(m.cfg.DelightHome)
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
		WithDelightHome(m.cfg.DelightHome).
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
	agentState := m.seedAgentStateFromServer()
	stateData, _ := json.Marshal(agentState)
	initial := sessionactor.State{
		SessionID:             m.sessionID,
		FSM:                   sessionactor.StateClosed,
		Mode:                  sessionactor.ModeLocal,
		ResumeToken:           strings.TrimSpace(agentState.ResumeToken),
		AgentState:            agentState,
		AgentStateJSON:        string(stateData),
		PersistRetryRemaining: 0,
		AgentStateVersion:     m.sessionAgentStateVer,
	}
	if m.cfg != nil {
		if stored, ok, err := storage.LoadLocalSessionInfo(m.cfg.DelightHome, m.sessionID); err == nil && ok {
			if strings.TrimSpace(initial.RolloutPath) == "" {
				initial.RolloutPath = strings.TrimSpace(stored.RolloutPath)
			}
			if strings.TrimSpace(initial.ResumeToken) == "" {
				initial.ResumeToken = strings.TrimSpace(stored.ResumeToken)
				initial.AgentState.ResumeToken = strings.TrimSpace(stored.ResumeToken)
				if refreshed, err := json.Marshal(initial.AgentState); err == nil {
					initial.AgentStateJSON = string(refreshed)
				}
			}
		}
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

// seedAgentStateFromServer initializes the session's agent state based on the
// server-provided value (when present), then overlays CLI-provided flags and
// engine defaults.
func (m *Manager) seedAgentStateFromServer() types.AgentState {
	agentState := types.AgentState{
		AgentType:         m.agent,
		ControlledByUser:  true,
		Requests:          make(map[string]types.AgentPendingRequest),
		CompletedRequests: make(map[string]types.AgentCompletedRequest),
	}

	// Preserve durable config from the server when available (best-effort).
	if strings.TrimSpace(m.sessionAgentStateJSON) != "" {
		var decoded types.AgentState
		if err := json.Unmarshal([]byte(m.sessionAgentStateJSON), &decoded); err == nil {
			agentState = decoded
			agentState.AgentType = m.agent
			agentState.ControlledByUser = true
			if agentState.Requests == nil {
				agentState.Requests = make(map[string]types.AgentPendingRequest)
			}
			if agentState.CompletedRequests == nil {
				agentState.CompletedRequests = make(map[string]types.AgentCompletedRequest)
			}
		}
	}

	// Overlay CLI-provided flags (highest precedence).
	if m.cfg != nil {
		if model := strings.TrimSpace(m.cfg.Model); model != "" {
			agentState.Model = model
		}
		if resume := strings.TrimSpace(m.cfg.ResumeToken); resume != "" {
			agentState.ResumeToken = resume
		}
	}

	// Seed stable defaults for Codex sessions when not configured yet.
	if m.agent == "codex" {
		if strings.TrimSpace(agentState.Model) == "" {
			agentState.Model = codexengine.DefaultModel()
		}
		if strings.TrimSpace(agentState.ReasoningEffort) == "" {
			agentState.ReasoningEffort = codexengine.DefaultReasoningEffort()
		}
		if strings.TrimSpace(agentState.PermissionMode) == "" {
			agentState.PermissionMode = "default"
		}
	}

	return agentState
}

// createSession creates a new session on the server
func (m *Manager) createSession() error {
	// Generate session tag (stable by default).
	if m.cfg.ForceNewSession {
		m.sessionTag = fmt.Sprintf("%s-%d", m.terminalID, time.Now().Unix())
	} else {
		if sess, ok, err := m.findExistingSessionForAgent(); err == nil && ok {
			m.sessionTag = sess.tag
			m.sessionID = sess.id
			m.sessionAgentStateJSON = sess.agentStateJSON
			m.sessionAgentStateVer = sess.agentStateVersion

			// Prefer the server-provided data key when available.
			if sess.dataEncryptionKey != "" {
				if err := m.setSessionDataEncryptionKey(sess.dataEncryptionKey); err != nil && m.debug {
					logger.Warnf("Failed to load session dataEncryptionKey: %v", err)
				}
			}
			return nil
		}
		m.sessionTag = stableSessionTagForAgent(m.terminalID, m.agent)
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
	dataKeyB64, err := crypto.EncryptDataEncryptionKey(m.dataKey, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to wrap dataEncryptionKey: %w", err)
	}
	body, err := json.Marshal(wire.CreateSessionRequest{
		Tag:        m.sessionTag,
		TerminalID: m.terminalID,
		Metadata:   encodedMeta,
		// Do not include AgentState here: the server overwrites agentState for
		// existing sessions. We restore any existing durable config from the
		// CreateSession response and then persist desired updates over websocket.
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
	m.sessionAgentStateJSON = ""
	m.sessionAgentStateVer = 0
	if result.Session.AgentState != nil && strings.TrimSpace(*result.Session.AgentState) != "" {
		m.sessionAgentStateJSON = *result.Session.AgentState
		m.sessionAgentStateVer = result.Session.AgentStateVersion
	}

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
// The server stores and returns `dataEncryptionKey` as a wrapped key that must
// be decrypted locally using the account master secret.
func (m *Manager) setSessionDataEncryptionKey(encoded string) error {
	if encoded == "" {
		return nil
	}

	raw, err := crypto.DecryptDataEncryptionKey(encoded, m.masterSecret)
	if err != nil {
		return err
	}
	if len(raw) != dataEncryptionKeyBytes {
		return fmt.Errorf("invalid decrypted dataEncryptionKey length: %d", len(raw))
	}
	m.dataKey = raw
	if m.debug {
		logger.Debugf("Session dataEncryptionKey loaded")
	}
	return nil
}

type listSessionItem struct {
	ID                string  `json:"id"`
	TerminalID        string  `json:"terminalId"`
	Metadata          string  `json:"metadata"`
	AgentState        *string `json:"agentState"`
	AgentStateVersion int64   `json:"agentStateVersion"`
	DataEncryptionKey *string `json:"dataEncryptionKey"`
}

type existingSessionMatch struct {
	id                string
	tag               string
	agentStateJSON    string
	agentStateVersion int64
	dataEncryptionKey string
}

// findExistingSessionForAgent selects an existing session owned by this terminal
// and directory that matches the current agent.
//
// This supports the "one session per agent per directory" model, and avoids
// creating duplicate sessions after introducing agent-scoped session tags.
func (m *Manager) findExistingSessionForAgent() (existingSessionMatch, bool, error) {
	if m == nil || m.cfg == nil {
		return existingSessionMatch{}, false, fmt.Errorf("missing manager config")
	}
	if strings.TrimSpace(m.token) == "" {
		return existingSessionMatch{}, false, fmt.Errorf("missing auth token")
	}
	if strings.TrimSpace(m.terminalID) == "" {
		return existingSessionMatch{}, false, fmt.Errorf("missing terminal id")
	}
	if strings.TrimSpace(m.workDir) == "" {
		return existingSessionMatch{}, false, fmt.Errorf("missing workdir")
	}

	const (
		sessionsListLimit = 50
		// maxSessionsListBytes is a safety cap to prevent reading arbitrarily
		// large session lists into memory during startup.
		//
		// A very large response can happen if the server has many sessions and
		// metadata/agentState blobs grow unexpectedly.
		maxSessionsListBytes = 8 << 20
	)

	url := fmt.Sprintf("%s/v1/sessions?limit=%d", strings.TrimRight(m.cfg.ServerURL, "/"), sessionsListLimit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return existingSessionMatch{}, false, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.token))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return existingSessionMatch{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return existingSessionMatch{}, false, fmt.Errorf("list sessions failed: %s", msg)
	}

	limitedBody := io.LimitReader(resp.Body, maxSessionsListBytes)
	decoder := json.NewDecoder(limitedBody)

	// Decode the top-level object manually so we can scan sessions one-by-one
	// and bail out early once we find a match. The server returns sessions
	// ordered by updated_at DESC, so the first match is the best match.
	tok, err := decoder.Token()
	if err != nil {
		return existingSessionMatch{}, false, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return existingSessionMatch{}, false, fmt.Errorf("invalid sessions response shape")
	}

	for decoder.More() {
		keyTok, err := decoder.Token()
		if err != nil {
			return existingSessionMatch{}, false, err
		}
		key, _ := keyTok.(string)
		if key != "sessions" {
			// Best-effort skip: decode into RawMessage to consume the value.
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return existingSessionMatch{}, false, err
			}
			continue
		}

		// Expect an array of session objects.
		tok, err := decoder.Token()
		if err != nil {
			return existingSessionMatch{}, false, err
		}
		if delim, ok := tok.(json.Delim); !ok || delim != '[' {
			return existingSessionMatch{}, false, fmt.Errorf("invalid sessions response shape")
		}

		for decoder.More() {
			var sess listSessionItem
			if err := decoder.Decode(&sess); err != nil {
				return existingSessionMatch{}, false, err
			}
			if strings.TrimSpace(sess.ID) == "" || strings.TrimSpace(sess.TerminalID) != m.terminalID {
				continue
			}
			metaPath, ok := decodeSessionMetadataPath(sess.Metadata)
			if !ok || metaPath != m.workDir {
				continue
			}
			agentType, agentStateJSON, ok := decodeSessionAgentType(sess.AgentState)
			if !ok || agentType != m.agent {
				continue
			}

			match := existingSessionMatch{
				id:                strings.TrimSpace(sess.ID),
				tag:               stableSessionTagForAgent(m.terminalID, m.agent),
				agentStateJSON:    agentStateJSON,
				agentStateVersion: sess.AgentStateVersion,
			}
			if sess.DataEncryptionKey != nil {
				match.dataEncryptionKey = strings.TrimSpace(*sess.DataEncryptionKey)
			}
			_ = resp.Body.Close()
			return match, true, nil
		}

		// Consume closing bracket.
		if _, err := decoder.Token(); err != nil {
			return existingSessionMatch{}, false, err
		}
	}

	// Consume closing brace.
	_, _ = decoder.Token()
	return existingSessionMatch{}, false, nil
}

// decodeSessionMetadataPath extracts the session metadata path from the
// base64-encoded metadata payload.
func decodeSessionMetadataPath(encoded string) (string, bool) {
	if strings.TrimSpace(encoded) == "" {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	meta := types.Metadata{}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", false
	}
	path := strings.TrimSpace(meta.Path)
	if path == "" {
		return "", false
	}
	return path, true
}

// decodeSessionAgentType determines the agent type for a session based on its
// agent state JSON payload.
func decodeSessionAgentType(agentState *string) (agentType string, agentStateJSON string, ok bool) {
	if agentState == nil || strings.TrimSpace(*agentState) == "" {
		return "", "", false
	}
	stateJSON := strings.TrimSpace(*agentState)
	state := types.AgentState{}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", "", false
	}
	agentType = strings.TrimSpace(state.AgentType)
	if agentType == "" {
		return "", "", false
	}
	return agentType, stateJSON, true
}

// stableSessionTagForAgent returns the stable tag used for the "primary" session
// for a terminal+agent combination.
//
// Under the one-terminal-per-directory model, this is effectively one session
// per agent per directory.
func stableSessionTagForAgent(terminalID string, agent string) string {
	terminalID = strings.TrimSpace(terminalID)
	agent = strings.TrimSpace(agent)
	if terminalID == "" {
		return ""
	}
	if agent == "" {
		return terminalID
	}
	// Use a simple join that stays stable and readable.
	return terminalID + ":" + agent
}

// createTerminal creates or updates a terminal on the server.
func (m *Manager) createTerminal() error {
	// Encrypt terminal metadata
	if len(m.masterSecret) != dataEncryptionKeyBytes {
		return fmt.Errorf("master secret must be %d bytes, got %d", dataEncryptionKeyBytes, len(m.masterSecret))
	}
	encryptedMeta, err := crypto.EncryptWithDataKey(m.terminalMetadata, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt terminal metadata: %w", err)
	}

	// Encrypt daemon state
	encryptedState, err := crypto.EncryptWithDataKey(m.terminalState, m.masterSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt daemon state: %w", err)
	}

	// Create terminal request
	body, err := json.Marshal(wire.CreateTerminalRequest{
		ID:          m.terminalID,
		Metadata:    base64.StdEncoding.EncodeToString(encryptedMeta),
		DaemonState: base64.StdEncoding.EncodeToString(encryptedState),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send request
	url := fmt.Sprintf("%s/v1/terminals", m.cfg.ServerURL)
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
		return fmt.Errorf("create terminal failed: %s - %s", resp.Status, string(respBody))
	}

	var response wire.CreateTerminalResponse
	if err := json.Unmarshal(respBody, &response); err == nil {
		m.terminalMetaVer = response.Terminal.MetadataVersion
		m.terminalStateVer = response.Terminal.DaemonStateVersion
	}

	if m.debug {
		logger.Infof("Terminal created/updated successfully")
	}

	return nil
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

// keepAliveLoop sends periodic keep-alive pings for both terminal and session.
func (m *Manager) keepAliveLoop() {
	terminalTicker := time.NewTicker(20 * time.Second)
	sessionTicker := time.NewTicker(30 * time.Second)
	defer terminalTicker.Stop()
	defer sessionTicker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-terminalTicker.C:
			// Send terminal keep-alive via terminal-scoped socket.
			if m.terminalClient != nil && m.terminalClient.IsConnected() {
				if err := m.terminalClient.EmitRaw("terminal-alive", wire.TerminalAlivePayload{
					TerminalID: m.terminalID,
					Time:       time.Now().UnixMilli(),
				}); err != nil && m.debug {
					logger.Warnf("Terminal keep-alive error: %v", err)
				}
			} else if m.debug {
				now := time.Now()
				if m.lastTerminalKeepAliveSkipAt.IsZero() || now.Sub(m.lastTerminalKeepAliveSkipAt) > 2*time.Minute {
					m.lastTerminalKeepAliveSkipAt = now
					logger.Debugf("Terminal keep-alive skipped (no terminal socket)")
				}
			}

		case <-sessionTicker.C:
			// Send session keep-alive.
			if m.wsClient != nil && m.wsClient.IsConnected() {
				thinking := m.thinking
				if m.sessionActor != nil {
					thinking = m.sessionActor.State().Thinking
				}
				if err := m.wsClient.KeepSessionAlive(m.sessionID, thinking); err != nil && m.debug {
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
	if m.terminalClient != nil {
		m.terminalClient.Close()
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
			managerSleep(200 * time.Millisecond)
			if m.debug {
				logger.Infof("Stop-daemon: shutting down")
			}
			go func() {
				_ = m.Close()
			}()
			managerSleep(200 * time.Millisecond)
			if m.debug {
				logger.Infof("Stop-daemon: exiting")
			}
			managerExit(0)
		}()
	})
}

// scheduleRestart triggers a delayed manager shutdown and process exit with a
// restart code, allowing wrapper scripts to re-launch the CLI automatically.
func (m *Manager) scheduleRestart() {
	m.shutdownOnce.Do(func() {
		go func() {
			managerSleep(200 * time.Millisecond)
			if m.debug {
				logger.Infof("Restart-daemon: shutting down")
			}
			go func() {
				_ = m.Close()
			}()
			managerSleep(200 * time.Millisecond)
			if m.debug {
				logger.Infof("Restart-daemon: exiting")
			}
			managerExit(restartExitCode)
		}()
	})
}

// forceExitAfter terminates the process after the delay, regardless of cleanup.
func (m *Manager) forceExitAfter(delay time.Duration, exitCode int) {
	go func() {
		managerSleep(delay)
		if m.debug {
			logger.Warnf("Stop-daemon: forcing exit after %s", delay)
		}
		managerExit(exitCode)
	}()
}
