package sdk

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/protocol/wire"
)

// Listener receives SDK events. Methods must be safe to call from any goroutine.
type Listener interface {
	OnConnected()
	OnDisconnected(reason string)
	OnUpdate(sessionID string, updateJSON string)
	OnError(message string)
}

// Client is a minimal mobile SDK client suitable for gomobile.
type Client struct {
	serverURL string
	token     string
	debug     bool

	mu                   sync.Mutex
	masterSecret         []byte
	dataKeys             map[string][]byte
	sessionFSM           map[string]sessionFSMState
	lastSessionHydrateAt int64
	listener             Listener
	userSocket           *websocket.Client
	httpClient           *http.Client
	logServer            *http.Server
	logServerURL         string

	dispatch  *dispatcher
	callbacks *dispatcher
}

// NewClient creates a new SDK client.
func NewClient(serverURL string) *Client {
	return &Client{
		serverURL:  serverURL,
		dataKeys:   make(map[string][]byte),
		sessionFSM: make(map[string]sessionFSMState),
		httpClient: &http.Client{Timeout: 15 * time.Second},
		dispatch:   newDispatcher(256),
		callbacks:  newDispatcher(256),
	}
}

type sessionFSMState struct {
	state            string
	active           bool
	controlledByUser bool
	connected        bool
	updatedAt        int64
	fetchedAt        int64
}

func controlledByUserFromAgentStateJSON(agentState string) (value bool, ok bool) {
	if agentState == "" {
		return true, false
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(agentState), &decoded); err != nil {
		return true, false
	}
	if v, ok := decoded["controlledByUser"].(bool); ok {
		return v, true
	}
	return true, false
}

func computeSessionFSM(connected bool, active bool, controlledByUser bool) sessionFSMState {
	switch {
	case !connected:
		return sessionFSMState{state: "disconnected", connected: false, active: active, controlledByUser: controlledByUser}
	case !active:
		return sessionFSMState{state: "offline", connected: true, active: false, controlledByUser: controlledByUser}
	case controlledByUser:
		return sessionFSMState{state: "local", connected: true, active: true, controlledByUser: true}
	default:
		return sessionFSMState{state: "remote", connected: true, active: true, controlledByUser: false}
	}
}

func deriveSessionUI(
	now int64,
	connected bool,
	active bool,
	agentState string,
	cached *sessionFSMState,
) (sessionFSMState, map[string]any) {
	controlledByUser, ok := controlledByUserFromAgentStateJSON(agentState)
	if !ok && cached != nil {
		controlledByUser = cached.controlledByUser
	}

	fsm := computeSessionFSM(connected, active, controlledByUser)
	fsm.fetchedAt = now

	ui := map[string]any{
		"state":            fsm.state, // disconnected|offline|local|remote
		"connected":        fsm.connected,
		"active":           fsm.active,
		"controlledByUser": fsm.controlledByUser,
		"canTakeControl":   fsm.state == "local",
		"canSend":          fsm.state == "remote",
	}

	return fsm, ui
}

func (c *Client) refreshSessionsForFSM() error {
	_, err := c.listSessions()
	return err
}

func (c *Client) ensureSessionFSM(sessionID string) (sessionFSMState, bool, error) {
	now := time.Now().UnixMilli()

	c.mu.Lock()
	state, ok := c.sessionFSM[sessionID]
	c.mu.Unlock()

	// If we've never seen this session (or we haven't refreshed recently), pull sessions
	// once. This makes control and send gating deterministic even when the caller hasn't
	// polled recently.
	const staleAfterMs = 2_000
	if !ok || state.fetchedAt == 0 || now-state.fetchedAt > staleAfterMs {
		if err := c.refreshSessionsForFSM(); err != nil {
			return sessionFSMState{}, false, err
		}
		c.mu.Lock()
		state, ok = c.sessionFSM[sessionID]
		c.mu.Unlock()
	}
	return state, ok, nil
}

// SetServerURL updates the server base URL.
func (c *Client) SetServerURL(serverURL string) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.serverURL = serverURL
		return nil, nil
	})
}

func generateMasterKeyBase64() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate master key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(secret), nil
}

// GenerateMasterKeyBase64Buffer returns a gomobile-safe Buffer containing a new
// 32-byte master key (base64).
func GenerateMasterKeyBase64Buffer() (*Buffer, error) {
	key, err := generateMasterKeyBase64()
	if err != nil {
		return nil, err
	}
	return newBufferFromString(key), nil
}

func generateEd25519KeyPairBase64() (publicKeyB64 string, privateKeyB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 keypair: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}

// GenerateEd25519KeyPairBuffers returns a new keypair as base64 Buffers
// (gomobile-safe).
func GenerateEd25519KeyPairBuffers() (*KeyPairBuffers, error) {
	pub, priv, err := generateEd25519KeyPairBase64()
	if err != nil {
		return nil, err
	}
	return newKeyPairBuffers(pub, priv), nil
}

// SetListener registers the listener for SDK events.
func (c *Client) SetListener(listener Listener) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.listener = listener
		return nil, nil
	})
}

// AuthWithKeyPairBuffer is a gomobile-safe wrapper that returns the token in a
// Buffer.
func (c *Client) AuthWithKeyPairBuffer(publicKeyB64, privateKeyB64 string) (*Buffer, error) {
	token, err := c.authWithKeyPairDispatch(publicKeyB64, privateKeyB64)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(token), nil
}

func (c *Client) authWithKeyPairDispatch(publicKeyB64, privateKeyB64 string) (string, error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.authWithKeyPair(publicKeyB64, privateKeyB64)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	token, _ := value.(string)
	return token, nil
}

func (c *Client) authWithKeyPair(publicKeyB64, privateKeyB64 string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("AuthWithKeyPair", r)
		}
	}()
	priv, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid private key length")
	}

	challenge := []byte("delight-auth-challenge")
	signature := ed25519.Sign(ed25519.PrivateKey(priv), challenge)

	reqBody := map[string]string{
		"publicKey": publicKeyB64,
		"challenge": base64.StdEncoding.EncodeToString(challenge),
		"signature": base64.StdEncoding.EncodeToString(signature),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal auth request: %w", err)
	}

	respBody, err := c.doRequest("POST", "/v1/auth", body)
	if err != nil {
		return "", err
	}

	var resp struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("parse auth response: %w", err)
	}
	if !resp.Success || resp.Token == "" {
		return "", fmt.Errorf("auth failed")
	}
	c.setToken(resp.Token)
	return resp.Token, nil
}

func parseTerminalURL(qrURL string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ParseTerminalURL", r)
		}
	}()
	parsed, err := url.Parse(qrURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "delight" && parsed.Scheme != "happy" {
		return "", fmt.Errorf("unsupported scheme: %s", parsed.Scheme)
	}
	if parsed.Host != "terminal" {
		return "", fmt.Errorf("unsupported host: %s", parsed.Host)
	}
	raw := parsed.RawQuery
	if raw == "" {
		return "", fmt.Errorf("missing public key in URL")
	}
	pubBytes, err := decodeBase64URL(raw)
	if err != nil {
		return "", fmt.Errorf("decode public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pubBytes), nil
}

// ParseTerminalURLBuffer is a gomobile-safe wrapper that returns the extracted
// terminal public key as a Buffer.
func ParseTerminalURLBuffer(qrURL string) (*Buffer, error) {
	value, err := parseTerminalURL(qrURL)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(value), nil
}

// ApproveTerminalAuth encrypts the master key and posts /v1/auth/response.
func (c *Client) ApproveTerminalAuth(terminalPublicKeyB64 string, masterKeyB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.approveTerminalAuth(terminalPublicKeyB64, masterKeyB64)
	})
	return err
}

func (c *Client) approveTerminalAuth(terminalPublicKeyB64 string, masterKeyB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ApproveTerminalAuth", r)
		}
	}()
	terminalPub, err := decodeBase64Any(terminalPublicKeyB64)
	if err != nil {
		return fmt.Errorf("decode terminal public key: %w", err)
	}
	if len(terminalPub) != 32 {
		return fmt.Errorf("invalid terminal public key length")
	}
	masterKey, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return fmt.Errorf("decode master key: %w", err)
	}
	if len(masterKey) != 32 {
		return fmt.Errorf("master key must be 32 bytes")
	}

	var terminalPubKey [32]byte
	copy(terminalPubKey[:], terminalPub)

	encrypted, err := crypto.EncryptBox(masterKey, &terminalPubKey)
	if err != nil {
		return fmt.Errorf("encrypt response: %w", err)
	}

	reqBody := map[string]string{
		"publicKey": terminalPublicKeyB64,
		"response":  base64.StdEncoding.EncodeToString(encrypted),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	_, err = c.doRequest("POST", "/v1/auth/response", body)
	return err
}

// SetDebug enables debug logging for underlying sockets.
func (c *Client) SetDebug(enabled bool) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.debug = enabled
		if c.userSocket != nil {
			c.userSocket.SetDebug(enabled)
		}
		return nil, nil
	})
}

// SetToken configures the auth token.
func (c *Client) SetToken(token string) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.setToken(token)
		return nil, nil
	})
}

func (c *Client) setToken(token string) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetToken", r)
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// SetLogDirectory configures the log directory for SDK logs.
func (c *Client) SetLogDirectory(path string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setLogDirectory(path)
	})
	return err
}

func (c *Client) setLogDirectory(path string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetLogDirectory", r)
		}
	}()
	return sdkLogs.setDir(path)
}

// LogLine forwards a log line into the SDK log buffer.
func (c *Client) LogLine(line string) {
	_ = c.dispatch.do(func() { c.logLine(line) })
}

func (c *Client) logLine(line string) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("LogLine", r)
		}
	}()
	logLine(line)
}

// SetMasterKeyBase64 sets the 32-byte master key from base64.
func (c *Client) SetMasterKeyBase64(keyB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setMasterKeyBase64(keyB64)
	})
	return err
}

func (c *Client) setMasterKeyBase64(keyB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetMasterKeyBase64", r)
		}
	}()
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode master key: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("master key must be 32 bytes, got %d", len(raw))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.masterSecret = raw
	return nil
}

// SetSessionDataKey stores a raw 32-byte data encryption key (base64).
func (c *Client) SetSessionDataKey(sessionID, keyB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setSessionDataKey(sessionID, keyB64)
	})
	return err
}

func (c *Client) setSessionDataKey(sessionID, keyB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetSessionDataKey", r)
		}
	}()
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode data key: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("data key must be 32 bytes, got %d", len(raw))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataKeys[sessionID] = raw
	return nil
}

// SetEncryptedSessionDataKey decrypts and stores a session data key.
func (c *Client) SetEncryptedSessionDataKey(sessionID, encryptedB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setEncryptedSessionDataKey(sessionID, encryptedB64)
	})
	return err
}

func (c *Client) setEncryptedSessionDataKey(sessionID, encryptedB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetEncryptedSessionDataKey", r)
		}
	}()
	c.mu.Lock()
	master := c.masterSecret
	c.mu.Unlock()

	if len(master) == 0 {
		return fmt.Errorf("master key not set")
	}

	decrypted, err := crypto.DecryptDataEncryptionKey(encryptedB64, master)
	if err != nil {
		// Fallback: accept raw base64 data keys (legacy server behavior).
		raw, decodeErr := base64.StdEncoding.DecodeString(encryptedB64)
		if decodeErr != nil || len(raw) != 32 {
			return fmt.Errorf("decrypt data key: %w", err)
		}
		decrypted = raw
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataKeys[sessionID] = decrypted
	return nil
}

// Connect opens a user-scoped websocket connection and begins emitting updates.
func (c *Client) Connect() error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.connect()
	})
	return err
}

func (c *Client) connect() error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("Connect", r)
		}
	}()
	c.mu.Lock()
	token := c.token
	debug := c.debug
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("token not set")
	}

	socket := websocket.NewUserClient(c.serverURL, token, debug)
	socket.On(websocket.EventUpdate, c.handleUpdateQueued)
	socket.On(websocket.EventEphemeral, c.handleEphemeralQueued)

	if err := socket.Connect(); err != nil {
		c.emitError(fmt.Sprintf("connect failed: %v", err))
		return err
	}
	if !socket.WaitForConnect(10 * time.Second) {
		err := fmt.Errorf("connect timeout")
		c.emitError(err.Error())
		return err
	}

	c.mu.Lock()
	c.userSocket = socket
	listener := c.listener
	c.mu.Unlock()

	if listener != nil {
		_ = c.callbacks.do(func() { listener.OnConnected() })
	}

	// Proactively hydrate per-session data keys so encrypted message updates can be
	// decrypted immediately, even if the app hasn't called ListSessions yet.
	_ = c.dispatch.do(func() { _, _ = c.listSessions() })
	return nil
}

// Disconnect closes the websocket connection.
func (c *Client) Disconnect() {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.disconnect()
		return nil, nil
	})
}

func (c *Client) disconnect() {
	defer func() {
		if r := recover(); r != nil {
			logPanic("Disconnect", r)
		}
	}()
	c.mu.Lock()
	socket := c.userSocket
	listener := c.listener
	c.userSocket = nil
	c.mu.Unlock()

	if socket != nil {
		_ = socket.Close()
	}
	if listener != nil {
		_ = c.callbacks.do(func() { listener.OnDisconnected("closed") })
	}
}

// SendMessage encrypts and sends a raw record JSON payload to a session.
func (c *Client) SendMessage(sessionID string, rawRecordJSON string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.sendMessageWithLocalID(sessionID, "", rawRecordJSON)
	})
	return err
}

// SendMessageWithLocalID encrypts and sends a raw record JSON payload to a session with a
// client-generated idempotency key ("localId"). The server will echo the localId back in
// message updates so callers can reconcile optimistic UI entries.
func (c *Client) SendMessageWithLocalID(sessionID string, localID string, rawRecordJSON string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.sendMessageWithLocalID(sessionID, localID, rawRecordJSON)
	})
	return err
}

// CallRPCBuffer issues an RPC call via the user-scoped websocket connection and
// returns the raw ACK payload as JSON.
func (c *Client) CallRPCBuffer(method string, paramsJSON string) (*Buffer, error) {
	resp, err := c.callRPCDispatch(method, paramsJSON)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

func (c *Client) callRPCDispatch(method string, paramsJSON string) (string, error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.callRPC(method, paramsJSON)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "{}", nil
	}
	if s, ok := value.(string); ok {
		return s, nil
	}
	return "{}", nil
}

func (c *Client) callRPC(method string, paramsJSON string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("CallRPC", r)
		}
	}()
	c.mu.Lock()
	socket := c.userSocket
	c.mu.Unlock()

	if socket == nil {
		return "", fmt.Errorf("not connected")
	}

	// Enforce control FSM for the control-switch RPC and keep the mobile UI dumb.
	// Phone can only take control if the CLI is online and currently in local mode.
	if strings.HasSuffix(method, ":switch") {
		sessionID := strings.TrimSuffix(method, ":switch")
		var params map[string]any
		_ = json.Unmarshal([]byte(paramsJSON), &params)
		mode, _ := params["mode"].(string)
		if mode == "remote" {
			state, ok, err := c.ensureSessionFSM(sessionID)
			if err != nil {
				return "", err
			}
			if ok {
				// Allow idempotent "take control" when already remote.
				if state.state == "remote" {
					// ok
				} else if state.state != "local" {
					return "", fmt.Errorf("cannot take control: session %s", state.state)
				}
			}
		}
	}

	resp, err := socket.EmitWithAck("rpc-call", wire.RPCCallPayload{
		Method: method,
		Params: paramsJSON,
	}, 10*time.Second)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("missing rpc ack")
	}

	if ok, _ := resp["ok"].(bool); !ok {
		if msg, _ := resp["error"].(string); msg != "" {
			return "", fmt.Errorf("rpc call failed: %s", msg)
		}
		return "", fmt.Errorf("rpc call failed")
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		return "{}", nil
	}

	// Update cached control FSM after a successful switch call so the UI can reflect
	// control immediately, even if agentState propagation is delayed or legacy-encrypted.
	if strings.HasSuffix(method, ":switch") {
		sessionID := strings.TrimSuffix(method, ":switch")
		mode := ""
		if result, _ := resp["result"].(map[string]any); result != nil {
			mode, _ = result["mode"].(string)
		}
		if mode == "local" || mode == "remote" {
			// Preserve active/connected bits if we have them, defaulting to optimistic "online".
			c.mu.Lock()
			prev := c.sessionFSM[sessionID]
			active := prev.active
			connected := prev.connected
			if prev.state == "" {
				active = true
				connected = true
			}
			controlledByUser := mode == "local"
			next := computeSessionFSM(connected, active, controlledByUser)
			next.updatedAt = prev.updatedAt
			next.fetchedAt = time.Now().UnixMilli()
			c.sessionFSM[sessionID] = next
			c.mu.Unlock()
		}
	}

	return string(encoded), nil
}

func (c *Client) sendMessageWithLocalID(sessionID string, localID string, rawRecordJSON string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SendMessage", r)
		}
	}()
	c.mu.Lock()
	socket := c.userSocket
	state := c.sessionFSM[sessionID]
	c.mu.Unlock()

	if socket == nil {
		return fmt.Errorf("not connected")
	}

	// Enforce phone-send rules:
	// - phone can only send while the session is online and phone-controlled (remote mode)
	if state.state != "remote" {
		// Refresh state once if we don't have a current snapshot.
		refreshed, ok, err := c.ensureSessionFSM(sessionID)
		if err != nil {
			return err
		}
		if ok && refreshed.state != "remote" {
			return fmt.Errorf("cannot send: session %s", refreshed.state)
		}
		if !ok {
			return fmt.Errorf("cannot send: unknown session")
		}
	}

	encrypted, err := c.encryptPayload(sessionID, []byte(rawRecordJSON))
	if err != nil {
		return err
	}
	if localID != "" {
		return socket.SendMessageWithLocalID(sessionID, encrypted, localID)
	}
	return socket.SendMessage(sessionID, encrypted)
}

// ListSessionsBuffer returns ListSessions JSON as a gomobile-safe Buffer.
func (c *Client) ListSessionsBuffer() (*Buffer, error) {
	resp, err := c.listSessionsDispatch()
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

func (c *Client) listSessionsDispatch() (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.listSessions()
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

func (c *Client) listSessions() (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ListSessions", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	respBody, err := c.doRequest("GET", "/v1/sessions", nil)
	if err != nil {
		return "", err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(respBody, &decoded); err == nil {
		if sessions, ok := decoded["sessions"].([]interface{}); ok {
			c.mu.Lock()
			socket := c.userSocket
			cachedFSM := make(map[string]sessionFSMState, len(c.sessionFSM))
			for k, v := range c.sessionFSM {
				cachedFSM[k] = v
			}
			c.mu.Unlock()
			connected := socket != nil && socket.IsConnected()
			now := time.Now().UnixMilli()

			for _, item := range sessions {
				session, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				sessionID, _ := session["id"].(string)
				dataKeyB64, _ := session["dataEncryptionKey"].(string)
				if sessionID != "" && dataKeyB64 != "" {
					_ = c.setEncryptedSessionDataKey(sessionID, dataKeyB64)
				}
				metadataB64, _ := session["metadata"].(string)
				if metadataB64 != "" {
					if decrypted, err := c.decryptLegacyString(metadataB64); err == nil {
						session["metadata"] = decrypted
					}
				}

				agentState, _ := session["agentState"].(string)
				active, _ := session["active"].(bool)
				updatedAt := int64(0)
				switch v := session["updatedAt"].(type) {
				case float64:
					updatedAt = int64(v)
				case int64:
					updatedAt = v
				case int:
					updatedAt = int64(v)
				}
				var cached *sessionFSMState
				if prev, ok := cachedFSM[sessionID]; ok {
					tmp := prev
					cached = &tmp
				}
				fsm, ui := deriveSessionUI(now, connected, active, agentState, cached)
				fsm.updatedAt = updatedAt

				// Inject a derived UI state so the iOS app can be a pure view layer.
				session["ui"] = ui

				if sessionID != "" {
					c.mu.Lock()
					c.sessionFSM[sessionID] = fsm
					c.mu.Unlock()
				}
			}
		}
		if encoded, err := json.Marshal(decoded); err == nil {
			return string(encoded), nil
		}
	}

	return string(respBody), nil
}

// ListMachinesBuffer returns ListMachines JSON as a gomobile-safe Buffer.
func (c *Client) ListMachinesBuffer() (*Buffer, error) {
	resp, err := c.listMachinesDispatch()
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

func (c *Client) listMachinesDispatch() (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.listMachines()
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

func (c *Client) listMachines() (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ListMachines", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	respBody, err := c.doRequest("GET", "/v1/machines", nil)
	if err != nil {
		return "", err
	}

	var decoded []interface{}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return string(respBody), nil
	}

	for _, item := range decoded {
		machine, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		metadataB64, _ := machine["metadata"].(string)
		if metadataB64 != "" {
			if decrypted, err := c.decryptLegacyString(metadataB64); err == nil {
				machine["metadata"] = decrypted
			}
		}
		daemonStateB64, _ := machine["daemonState"].(string)
		if daemonStateB64 != "" {
			if decrypted, err := c.decryptLegacyString(daemonStateB64); err == nil {
				machine["daemonState"] = decrypted
			}
		}
	}

	encoded, err := json.Marshal(decoded)
	if err != nil {
		return string(respBody), nil
	}
	return string(encoded), nil
}

func (c *Client) decryptLegacyString(payload string) (string, error) {
	c.mu.Lock()
	secret := make([]byte, len(c.masterSecret))
	copy(secret, c.masterSecret)
	c.mu.Unlock()
	if len(secret) != 32 {
		return "", fmt.Errorf("master key not configured")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var key [32]byte
	copy(key[:], secret)
	var decoded map[string]interface{}
	if err := crypto.DecryptLegacy(raw, &key, &decoded); err != nil {
		return "", fmt.Errorf("decrypt payload: %w", err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("marshal decrypted: %w", err)
	}
	return string(encoded), nil
}

// GetSessionMessagesBuffer returns GetSessionMessages JSON as a gomobile-safe Buffer.
func (c *Client) GetSessionMessagesBuffer(sessionID string, limit int) (*Buffer, error) {
	resp, err := c.getSessionMessagesDispatch(sessionID, limit)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// GetSessionMessagesPageBuffer returns a page of session messages for infinite scroll.
//
// If beforeSeq > 0, it fetches messages with seq < beforeSeq.
// If beforeSeq <= 0, it fetches the most recent page.
func (c *Client) GetSessionMessagesPageBuffer(sessionID string, limit int, beforeSeq int64) (*Buffer, error) {
	resp, err := c.getSessionMessagesPageDispatch(sessionID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

func (c *Client) getSessionMessagesDispatch(sessionID string, limit int) (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.getSessionMessages(sessionID, limit)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

func (c *Client) getSessionMessagesPageDispatch(sessionID string, limit int, beforeSeq int64) (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.getSessionMessagesPage(sessionID, limit, beforeSeq)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

func (c *Client) getSessionMessages(sessionID string, limit int) (resp string, err error) {
	return c.getSessionMessagesPage(sessionID, limit, 0)
}

func (c *Client) getSessionMessagesPage(sessionID string, limit int, beforeSeq int64) (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("GetSessionMessages", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	if sessionID == "" {
		return "", fmt.Errorf("sessionID required")
	}
	logLine(fmt.Sprintf("GetSessionMessages sessionID=%s limit=%d beforeSeq=%d", sessionID, limit, beforeSeq))

	endpoint := fmt.Sprintf("/v1/sessions/%s/messages", url.PathEscape(sessionID))
	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if beforeSeq > 0 {
		values.Set("beforeSeq", strconv.FormatInt(beforeSeq, 10))
	}
	if len(values) > 0 {
		endpoint = endpoint + "?" + values.Encode()
	}

	respBody, err := c.doRequest("GET", endpoint, nil)
	if err != nil {
		logLine(fmt.Sprintf("GetSessionMessages request error: %v", err))
		return "", err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return string(respBody), nil
	}

	messages, ok := decoded["messages"].([]interface{})
	if !ok {
		return string(respBody), nil
	}

	for _, item := range messages {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := msg["content"].(map[string]interface{})
		if !ok {
			continue
		}
		decrypted, err := c.decryptEnvelope(sessionID, content)
		if err != nil || decrypted == nil {
			continue
		}
		msg["content"] = decrypted
	}

	encoded, err := json.Marshal(decoded)
	if err != nil {
		return string(respBody), nil
	}
	return string(encoded), nil
}

func (c *Client) handleEphemeralQueued(data map[string]interface{}) {
	_ = c.dispatch.do(func() { c.handleEphemeral(data) })
}

func (c *Client) handleUpdateQueued(data map[string]interface{}) {
	_ = c.dispatch.do(func() { c.handleUpdate(data) })
}

func (c *Client) handleEphemeral(data map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("handleEphemeral", r)
		}
	}()
	c.emitUpdate("", data)
}

func (c *Client) handleUpdate(data map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("handleUpdate", r)
		}
	}()
	body, _ := data["body"].(map[string]interface{})
	if body == nil {
		c.emitUpdate("", data)
		return
	}

	updateType, _ := body["t"].(string)
	sessionID := ""

	switch updateType {
	case "new-message":
		sessionID, _ = body["sid"].(string)
		if msg, ok := body["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(map[string]interface{}); ok {
				decrypted, err := c.decryptEnvelope(sessionID, content)
				if err != nil {
					// Likely missing the per-session data key (race with initial ListSessions).
					// Refresh sessions (debounced) and retry once.
					c.maybeHydrateSessionKeys()
					if retry, retryErr := c.decryptEnvelope(sessionID, content); retryErr == nil && retry != nil {
						msg["content"] = retry
					}
				} else if decrypted != nil {
					msg["content"] = decrypted
				}
			}
		}
	case "new-session":
		sessionID, _ = body["id"].(string)
		if dataKey, ok := body["dataEncryptionKey"].(string); ok && dataKey != "" {
			_ = c.setEncryptedSessionDataKey(sessionID, dataKey)
		}
	case "update-session":
		sessionID, _ = body["id"].(string)
		// Keep the derived session control FSM up to date without requiring the UI
		// to poll ListSessions.
		//
		// The server emits agentState changes as part of update-session events, so
		// we can update our per-session cached UI state immediately.
		if sessionID != "" {
			if agentState, ok := body["agentState"].(map[string]interface{}); ok {
				if value, _ := agentState["value"].(string); value != "" {
					c.applyAgentStateToSessionFSM(sessionID, value)
				}
			}
		}
	}

	c.emitUpdate(sessionID, data)
}

func (c *Client) applyAgentStateToSessionFSM(sessionID string, agentState string) {
	now := time.Now().UnixMilli()

	c.mu.Lock()
	prev := c.sessionFSM[sessionID]
	socket := c.userSocket
	c.mu.Unlock()

	connected := prev.connected
	if socket != nil {
		connected = socket.IsConnected()
	}
	active := prev.active
	if prev.state == "" {
		// If we don't have a previous snapshot, receiving an update implies the
		// session is at least "online-ish".
		active = true
		connected = true
	}

	fsm, _ := deriveSessionUI(now, connected, active, agentState, &prev)
	fsm.updatedAt = prev.updatedAt

	c.mu.Lock()
	c.sessionFSM[sessionID] = fsm
	c.mu.Unlock()
}

func (c *Client) maybeHydrateSessionKeys() {
	now := time.Now().UnixMilli()
	const minIntervalMs = 2_000

	c.mu.Lock()
	last := c.lastSessionHydrateAt
	if last != 0 && now-last < minIntervalMs {
		c.mu.Unlock()
		return
	}
	c.lastSessionHydrateAt = now
	c.mu.Unlock()

	// We are already executing on the SDK dispatch queue when called from handleUpdate.
	// Calling listSessions directly avoids re-entrancy into the dispatcher.
	_, _ = c.listSessions()
}

func (c *Client) emitUpdate(sessionID string, payload map[string]interface{}) {
	listener := c.getListener()
	if listener == nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		_ = c.callbacks.do(func() { listener.OnError(fmt.Sprintf("encode update: %v", err)) })
		return
	}
	_ = c.callbacks.do(func() { listener.OnUpdate(sessionID, string(encoded)) })
}

func (c *Client) emitError(message string) {
	listener := c.getListener()
	if listener != nil {
		_ = c.callbacks.do(func() { listener.OnError(message) })
	}
}

func (c *Client) getListener() Listener {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listener
}

func (c *Client) encryptPayload(sessionID string, data []byte) (string, error) {
	c.mu.Lock()
	dataKey := c.dataKeys[sessionID]
	master := c.masterSecret
	c.mu.Unlock()

	var encrypted []byte
	var err error

	if len(dataKey) == 32 {
		encrypted, err = crypto.EncryptWithDataKey(json.RawMessage(data), dataKey)
	} else {
		if len(master) != 32 {
			return "", fmt.Errorf("master key not set")
		}
		var secretKey [32]byte
		copy(secretKey[:], master)
		encrypted, err = crypto.EncryptLegacy(json.RawMessage(data), &secretKey)
	}
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (c *Client) decryptEnvelope(sessionID string, content map[string]interface{}) (map[string]interface{}, error) {
	if content == nil {
		return nil, nil
	}
	t, _ := content["t"].(string)
	if t != "encrypted" {
		return nil, nil
	}
	cipherText, _ := content["c"].(string)
	if cipherText == "" {
		return nil, fmt.Errorf("empty encrypted content")
	}

	decrypted, err := c.decryptPayload(sessionID, cipherText)
	if err != nil {
		return nil, err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(decrypted, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func (c *Client) decryptPayload(sessionID, dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	if len(encrypted) == 0 {
		return nil, fmt.Errorf("empty encrypted data")
	}

	c.mu.Lock()
	dataKey := c.dataKeys[sessionID]
	master := c.masterSecret
	c.mu.Unlock()

	// AES-GCM format has version byte 0 and 12-byte nonce.
	if encrypted[0] == 0 && len(encrypted) >= 1+12+16 {
		var aesErr error
		key := dataKey
		if len(key) != 32 {
			if len(master) == 32 {
				key = master
			} else {
				return nil, fmt.Errorf("AES-GCM data but no key available")
			}
		}
		var result json.RawMessage
		if err := crypto.DecryptWithDataKey(encrypted, key, &result); err == nil {
			return []byte(result), nil
		} else {
			aesErr = err
		}

		// Fall back to legacy decoding if the AES decode fails. This avoids
		// misclassifying legacy SecretBox payloads whose nonce happens to start
		// with a 0 byte.
		decrypted, legacyErr := decryptLegacyPayload(encrypted, dataKey, master)
		if legacyErr == nil {
			return decrypted, nil
		}
		return nil, aesErr
	}

	return decryptLegacyPayload(encrypted, dataKey, master)
}

func decryptLegacyPayload(encrypted, dataKey, master []byte) ([]byte, error) {
	// Legacy SecretBox format: [nonce(24)][ciphertext]
	var secretKey [32]byte
	switch {
	case len(dataKey) == 32:
		copy(secretKey[:], dataKey)
	case len(master) == 32:
		copy(secretKey[:], master)
	default:
		return nil, fmt.Errorf("secret key not set")
	}

	var result json.RawMessage
	if err := crypto.DecryptLegacy(encrypted, &secretKey, &result); err != nil {
		return nil, err
	}
	return []byte(result), nil
}

func decodeBase64URL(input string) ([]byte, error) {
	if input == "" {
		return nil, fmt.Errorf("empty base64url")
	}
	if data, err := base64.URLEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.RawURLEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.StdEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(input)
}

func decodeBase64Any(input string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(input)
}

func (c *Client) doRequest(method, path string, body []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("doRequest", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	c.mu.Lock()
	token := c.token
	baseURL := c.serverURL
	client := c.httpClient
	c.mu.Unlock()

	if baseURL == "" {
		return nil, fmt.Errorf("server URL not set")
	}

	fullURL := fmt.Sprintf("%s%s", baseURL, path)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, fullURL, reader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpResp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed: %s", string(respBody))
	}
	return respBody, nil
}
