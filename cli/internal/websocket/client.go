package websocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bhandras/delight/protocol/logger"
	"github.com/bhandras/delight/protocol/wire"
	socket "github.com/zishang520/socket.io/clients/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

func normalizePayload(data any) any {
	if data == nil {
		return nil
	}

	switch data.(type) {
	case map[string]interface{}:
		return data
	case []any:
		return data
	case string, bool, int, int64, float64:
		return data
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return data
	}

	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return data
	}

	return decoded
}

// EventType represents different types of Socket.IO events
type EventType string

const (
	EventMessage       EventType = "message"
	EventSessionAlive  EventType = "session-alive"
	EventUpdateMeta    EventType = "update-metadata"
	EventUpdateState   EventType = "update-state"
	EventSessionUpdate EventType = "session-update"
	EventEphemeral     EventType = "ephemeral"
	EventUpdate        EventType = "update"
)

// Client represents a Socket.IO client connection
type Client struct {
	serverURL  string
	token      string
	sessionID  string
	machineID  string
	clientType string
	transport  string
	socket     *socket.Socket
	mu         sync.Mutex
	handlers   map[EventType]func(map[string]interface{})
	done       chan struct{}
	closeOnce  sync.Once
	debug      bool
	connected  bool

	lastConnectErrAt  time.Time
	lastConnectErrMsg string

	onConnect    []func()
	onDisconnect []func(reason string)
}

const (
	// TransportWebSocket forces Socket.IO to use WebSocket only.
	TransportWebSocket = "websocket"
	// TransportPolling forces Socket.IO to use HTTP long-polling only.
	TransportPolling = "polling"
)

// normalizeTransport returns a supported transport mode or the default.
func normalizeTransport(mode string) string {
	switch mode {
	case TransportWebSocket:
		return TransportWebSocket
	case TransportPolling:
		return TransportPolling
	default:
		return TransportWebSocket
	}
}

// NewClient creates a new Socket.IO client
func NewClient(serverURL, token, sessionID, transport string, debug bool) *Client {
	return &Client{
		serverURL:  serverURL,
		token:      token,
		sessionID:  sessionID,
		clientType: "session-scoped",
		transport:  normalizeTransport(transport),
		handlers:   make(map[EventType]func(map[string]interface{})),
		done:       make(chan struct{}),
		debug:      debug,
	}
}

// NewMachineClient creates a machine-scoped Socket.IO client.
func NewMachineClient(serverURL, token, machineID, transport string, debug bool) *Client {
	return &Client{
		serverURL:  serverURL,
		token:      token,
		machineID:  machineID,
		clientType: "machine-scoped",
		transport:  normalizeTransport(transport),
		handlers:   make(map[EventType]func(map[string]interface{})),
		done:       make(chan struct{}),
		debug:      debug,
	}
}

// NewUserClient creates a user-scoped Socket.IO client.
func NewUserClient(serverURL, token, transport string, debug bool) *Client {
	return &Client{
		serverURL:  serverURL,
		token:      token,
		clientType: "user-scoped",
		transport:  normalizeTransport(transport),
		handlers:   make(map[EventType]func(map[string]interface{})),
		done:       make(chan struct{}),
		debug:      debug,
	}
}

// SetDebug toggles debug logging.
func (c *Client) SetDebug(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debug = enabled
}

// OnConnect registers a callback that runs when the Socket.IO client reports a
// successful connection.
func (c *Client) OnConnect(fn func()) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onConnect = append(c.onConnect, fn)
	c.mu.Unlock()
}

// OnDisconnect registers a callback that runs when the Socket.IO client reports
// a disconnect.
func (c *Client) OnDisconnect(fn func(reason string)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onDisconnect = append(c.onDisconnect, fn)
	c.mu.Unlock()
}

// On registers an event handler
func (c *Client) On(eventType EventType, handler func(map[string]interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[eventType] = handler
}

// Connect establishes a Socket.IO connection to the server
func (c *Client) Connect() error {
	c.mu.Lock()
	debug := c.debug
	serverURL := c.serverURL
	token := c.token
	clientType := c.clientType
	sessionID := c.sessionID
	machineID := c.machineID
	transport := c.transport
	prevSock := c.socket
	c.socket = nil
	c.connected = false
	c.mu.Unlock()
	if debug {
		logger.Debugf("Connecting to Socket.IO: %s (path: /v1/updates)", serverURL)
	}
	if prevSock != nil {
		prevSock.Disconnect()
	}

	// Create Socket.IO options
	opts := socket.DefaultOptions()

	// Set the path (like JS client does)
	opts.SetPath("/v1/updates")

	switch transport {
	case TransportWebSocket:
		if debug {
			logger.Debugf("Socket.IO transports: websocket only")
		}
		opts.SetTransports(types.NewSet(socket.WebSocket))
	case TransportPolling:
		if debug {
			logger.Debugf("Socket.IO transports: polling only")
		}
		opts.SetTransports(types.NewSet(socket.Polling))
	default:
		opts.SetTransports(types.NewSet(socket.WebSocket))
	}
	opts.SetForceNew(true)
	opts.SetMultiplex(false)

	// Reduce connect_error log spam by backing off reconnection attempts.
	opts.SetReconnectionDelay(5_000)
	opts.SetReconnectionDelayMax(30_000)
	opts.SetTimeout(15 * time.Second)

	// Set auth token, client type, and scope id (matching mobile app format)
	auth := wire.SocketAuthPayload{
		Token:      token,
		ClientType: clientType,
	}
	switch clientType {
	case "session-scoped":
		auth.SessionID = sessionID
	case "machine-scoped":
		auth.MachineID = machineID
	}
	authMap, ok := normalizePayload(auth).(map[string]any)
	if !ok {
		return fmt.Errorf("invalid auth payload type: %T", auth)
	}
	opts.SetAuth(authMap)

	// Connect using base URL (path is set in options)
	sock, err := socket.Connect(serverURL, opts)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	c.mu.Lock()
	c.socket = sock
	c.mu.Unlock()

	// Set up connect handler
	sock.On(types.EventName("connect"), func(args ...any) {
		c.mu.Lock()
		c.connected = true
		debug := c.debug
		callbacks := append([]func(){}, c.onConnect...)
		c.mu.Unlock()

		if debug {
			logger.Debugf("Socket.IO connected! ID: %s", sock.Id())
		}
		for _, cb := range callbacks {
			cb()
		}
	})

	// Set up disconnect handler
	sock.On(types.EventName("disconnect"), func(args ...any) {
		reason := ""
		if len(args) > 0 {
			if r, ok := args[0].(string); ok {
				reason = r
			}
		}

		c.mu.Lock()
		c.connected = false
		debug := c.debug
		callbacks := append([]func(string){}, c.onDisconnect...)
		c.mu.Unlock()

		if debug {
			logger.Debugf("Socket.IO disconnected: %s", reason)
		}
		for _, cb := range callbacks {
			cb(reason)
		}
	})

	// Set up error handler
	sock.On(types.EventName("connect_error"), func(args ...any) {
		c.logConnectError(args)
	})

	// Set up event handlers for each event type.
	//
	// Note: EventEphemeral is used for user-scoped signals (e.g. permission
	// prompts). It must be subscribed so mobile clients receive them.
	for _, eventType := range []EventType{EventMessage, EventUpdate, EventEphemeral, EventSessionAlive, EventUpdateMeta, EventUpdateState, EventSessionUpdate} {
		et := eventType // Capture for closure
		sock.On(types.EventName(et), func(args ...any) {
			c.mu.Lock()
			debug := c.debug
			c.mu.Unlock()
			if debug {
				logger.Tracef("Received event: %s", et)
			}

			// Convert args to map if possible
			var data map[string]interface{}
			if len(args) > 0 {
				if m, ok := args[0].(map[string]interface{}); ok {
					data = m
				}
			}

			c.mu.Lock()
			handler, ok := c.handlers[et]
			c.mu.Unlock()

			if ok && handler != nil {
				// Keep handler execution synchronous to avoid unbounded goroutine
				// creation and to preserve event ordering. Handlers should return quickly.
				handler(data)
			}
		})
	}

	c.mu.Lock()
	debug = c.debug
	c.mu.Unlock()
	if debug {
		logger.Debugf("Socket.IO connection initiated")
	}

	return nil
}

func (c *Client) logConnectError(args []any) {
	c.mu.Lock()
	debug := c.debug
	now := time.Now()
	msg := ""
	if len(args) > 0 && args[0] != nil {
		msg = fmt.Sprint(args[0])
	}
	lastAt := c.lastConnectErrAt
	lastMsg := c.lastConnectErrMsg
	shouldLog := debug && (msg != "" && (msg != lastMsg || now.Sub(lastAt) > 5*time.Second))
	if shouldLog {
		c.lastConnectErrAt = now
		c.lastConnectErrMsg = msg
	}
	c.mu.Unlock()
	if shouldLog {
		logger.Debugf("Socket.IO connection error: %s", msg)
	}
}

// WaitForConnect waits for the socket to report connected or times out.
func (c *Client) WaitForConnect(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.IsConnected() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return c.IsConnected()
}

// Emit sends an event to the server
func (c *Client) Emit(eventType EventType, data any) error {
	c.mu.Lock()
	sock := c.socket
	debug := c.debug
	if sock == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	if debug {
		logger.Tracef("Sending event: %s", eventType)
	}
	sock.Emit(string(eventType), normalizePayload(data))
	c.mu.Unlock()
	return nil
}

// EmitRaw sends an arbitrary event name to the server.
func (c *Client) EmitRaw(event string, data any) error {
	c.mu.Lock()
	sock := c.socket
	debug := c.debug
	if sock == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	if debug {
		logger.Tracef("Sending raw event: %s", event)
	}
	sock.Emit(event, normalizePayload(data))
	c.mu.Unlock()
	return nil
}

// EmitWithAck sends an event and waits for an ACK response.
func (c *Client) EmitWithAck(event string, data any, timeout time.Duration) (map[string]interface{}, error) {
	c.mu.Lock()
	sock := c.socket
	debug := c.debug
	if sock == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("not connected")
	}
	if debug {
		logger.Tracef("Sending raw event with ack: %s", event)
	}

	resultCh := make(chan map[string]interface{}, 1)
	errCh := make(chan error, 1)

	sock.Emit(event, normalizePayload(data), func(args []any, err error) {
		if err != nil {
			errCh <- err
			return
		}
		if len(args) == 0 {
			resultCh <- nil
			return
		}
		if payload, ok := args[0].(map[string]interface{}); ok {
			resultCh <- payload
			return
		}
		resultCh <- nil
	})
	c.mu.Unlock()

	select {
	case res := <-resultCh:
		return res, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("ack timeout")
	}
}

// RawSocket exposes the underlying Socket.IO socket for low-level handlers.
func (c *Client) RawSocket() *socket.Socket {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.socket
}

// SendMessage sends a message for a session
func (c *Client) SendMessage(sessionID, message string) error {
	return c.SendMessageWithLocalID(sessionID, message, "")
}

// SendMessageWithLocalID sends a message for a session with an idempotency key.
func (c *Client) SendMessageWithLocalID(sessionID, message, localID string) error {
	payload := wire.OutboundMessagePayload{
		SID:     sessionID,
		LocalID: localID,
		Message: message,
	}
	return c.Emit(EventMessage, payload)
}

// UpdateMetadata sends a metadata update for a session
func (c *Client) UpdateMetadata(sessionID string, metadata string, version int64) (int64, error) {
	resp, err := c.EmitWithAck(
		string(EventUpdateMeta),
		wire.UpdateMetadataPayload{
			SID:             sessionID,
			Metadata:        metadata,
			ExpectedVersion: version,
		},
		5*time.Second,
	)
	if err != nil {
		return version, err
	}
	if resp == nil {
		return version, fmt.Errorf("missing ack")
	}

	result, _ := resp["result"].(string)
	switch result {
	case "success":
		return getInt64(resp["version"]), nil
	case "version-mismatch":
		return getInt64(resp["version"]), ErrVersionMismatch
	default:
		return version, fmt.Errorf("update-metadata failed: %v", result)
	}
}

// UpdateState sends a state update for a session
func (c *Client) UpdateState(sessionID string, agentState string, version int64) (int64, error) {
	stateVal := agentState
	resp, err := c.EmitWithAck(
		string(EventUpdateState),
		wire.UpdateStatePayload{
			SID:             sessionID,
			AgentState:      &stateVal,
			ExpectedVersion: version,
		},
		5*time.Second,
	)
	if err != nil {
		return version, err
	}
	if resp == nil {
		return version, fmt.Errorf("missing ack")
	}

	result, _ := resp["result"].(string)
	switch result {
	case "success":
		return getInt64(resp["version"]), nil
	case "version-mismatch":
		return getInt64(resp["version"]), ErrVersionMismatch
	default:
		return version, fmt.Errorf("update-state failed: %v", result)
	}
}

var ErrVersionMismatch = errors.New("version mismatch")

func getInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// KeepSessionAlive sends a keep-alive ping for a session
func (c *Client) KeepSessionAlive(sessionID string, thinking bool) error {
	return c.Emit(EventSessionAlive, wire.SessionAlivePayload{
		SID:      sessionID,
		Time:     time.Now().UnixMilli(),
		Thinking: thinking,
	})
}

// EmitEphemeral sends an ephemeral event (activity/thinking state)
// These events are not persisted, just broadcast to connected clients
func (c *Client) EmitEphemeral(data any) error {
	return c.Emit(EventEphemeral, data)
}

// EmitMessage sends a session message to the server
// Messages contain encrypted Claude conversation content
func (c *Client) EmitMessage(data any) error {
	return c.Emit(EventMessage, data)
}

// Close closes the Socket.IO connection
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.done != nil {
			close(c.done)
		}
	})

	c.mu.Lock()
	sock := c.socket
	c.socket = nil
	c.connected = false
	c.mu.Unlock()
	if sock != nil {
		sock.Disconnect()
	}
	return nil
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	sock := c.socket
	connected := c.connected
	c.mu.Unlock()

	if connected {
		return true
	}

	if sock != nil && sock.Connected() {
		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()
		return true
	}

	return false
}
