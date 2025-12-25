package websocket

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	socket "github.com/zishang520/socket.io/clients/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

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

// Event represents a Socket.IO event
type Event struct {
	Type EventType              `json:"type"`
	Data map[string]interface{} `json:"data"`
}

// MessageEvent represents a message event payload
type MessageEvent struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
	Seq       int64  `json:"seq,omitempty"`
}

// Client represents a Socket.IO client connection
type Client struct {
	serverURL  string
	token      string
	sessionID  string
	machineID  string
	clientType string
	socket     *socket.Socket
	mu         sync.RWMutex
	handlers   map[EventType]func(map[string]interface{})
	done       chan struct{}
	closeOnce  sync.Once
	debug      bool
	connected  bool
}

// NewClient creates a new Socket.IO client
func NewClient(serverURL, token, sessionID string, debug bool) *Client {
	return &Client{
		serverURL:  serverURL,
		token:      token,
		sessionID:  sessionID,
		clientType: "session-scoped",
		handlers:   make(map[EventType]func(map[string]interface{})),
		done:       make(chan struct{}),
		debug:      debug,
	}
}

// NewMachineClient creates a machine-scoped Socket.IO client.
func NewMachineClient(serverURL, token, machineID string, debug bool) *Client {
	return &Client{
		serverURL:  serverURL,
		token:      token,
		machineID:  machineID,
		clientType: "machine-scoped",
		handlers:   make(map[EventType]func(map[string]interface{})),
		done:       make(chan struct{}),
		debug:      debug,
	}
}

// NewUserClient creates a user-scoped Socket.IO client.
func NewUserClient(serverURL, token string, debug bool) *Client {
	return &Client{
		serverURL:  serverURL,
		token:      token,
		clientType: "user-scoped",
		handlers:   make(map[EventType]func(map[string]interface{})),
		done:       make(chan struct{}),
		debug:      debug,
	}
}

// On registers an event handler
func (c *Client) On(eventType EventType, handler func(map[string]interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[eventType] = handler
}

// Connect establishes a Socket.IO connection to the server
func (c *Client) Connect() error {
	if c.debug {
		log.Printf("Connecting to Socket.IO: %s (path: /v1/updates)", c.serverURL)
	}

	// Create Socket.IO options
	opts := socket.DefaultOptions()

	// Set the path (like JS client does)
	opts.SetPath("/v1/updates")
	opts.SetTransports(types.NewSet(socket.Polling, socket.WebSocket))

	// Set auth token, client type, and scope id (matching mobile app format)
	auth := map[string]interface{}{
		"token":      c.token,
		"clientType": c.clientType,
	}
	switch c.clientType {
	case "session-scoped":
		auth["sessionId"] = c.sessionID
	case "machine-scoped":
		auth["machineId"] = c.machineID
	}
	opts.SetAuth(auth)

	// Connect using base URL (path is set in options)
	sock, err := socket.Connect(c.serverURL, opts)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	c.socket = sock

	// Set up connect handler
	sock.On(types.EventName("connect"), func(args ...any) {
		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()

		if c.debug {
			log.Printf("Socket.IO connected! ID: %s", sock.Id())
		}
	})

	// Set up disconnect handler
	sock.On(types.EventName("disconnect"), func(args ...any) {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()

		reason := ""
		if len(args) > 0 {
			if r, ok := args[0].(string); ok {
				reason = r
			}
		}
		if c.debug {
			log.Printf("Socket.IO disconnected: %s", reason)
		}
	})

	// Set up error handler
	sock.On(types.EventName("connect_error"), func(args ...any) {
		if len(args) > 0 {
			log.Printf("Socket.IO connection error: %v", args[0])
		}
	})

	// Set up event handlers for each event type
	for _, eventType := range []EventType{EventMessage, EventUpdate, EventSessionAlive, EventUpdateMeta, EventUpdateState, EventSessionUpdate} {
		et := eventType // Capture for closure
		sock.On(types.EventName(et), func(args ...any) {
			if c.debug {
				log.Printf("Received event: %s", et)
			}

			// Convert args to map if possible
			var data map[string]interface{}
			if len(args) > 0 {
				if m, ok := args[0].(map[string]interface{}); ok {
					data = m
				}
			}

			c.mu.RLock()
			handler, ok := c.handlers[et]
			c.mu.RUnlock()

			if ok && handler != nil {
				go handler(data)
			}
		})
	}

	if c.debug {
		log.Println("Socket.IO connection initiated")
	}

	return nil
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
func (c *Client) Emit(eventType EventType, data map[string]interface{}) error {
	c.mu.RLock()
	sock := c.socket
	c.mu.RUnlock()

	if sock == nil {
		return fmt.Errorf("not connected")
	}

	if c.debug {
		log.Printf("Sending event: %s", eventType)
	}

	sock.Emit(string(eventType), data)
	return nil
}

// EmitRaw sends an arbitrary event name to the server.
func (c *Client) EmitRaw(event string, data map[string]interface{}) error {
	c.mu.RLock()
	sock := c.socket
	c.mu.RUnlock()

	if sock == nil {
		return fmt.Errorf("not connected")
	}

	if c.debug {
		log.Printf("Sending raw event: %s", event)
	}

	sock.Emit(event, data)
	return nil
}

// EmitWithAck sends an event and waits for an ACK response.
func (c *Client) EmitWithAck(event string, data map[string]interface{}, timeout time.Duration) (map[string]interface{}, error) {
	c.mu.RLock()
	sock := c.socket
	c.mu.RUnlock()

	if sock == nil {
		return nil, fmt.Errorf("not connected")
	}

	if c.debug {
		log.Printf("Sending raw event with ack: %s", event)
	}

	resultCh := make(chan map[string]interface{}, 1)
	errCh := make(chan error, 1)

	sock.Emit(event, data, func(args []any, err error) {
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.socket
}

// SendMessage sends a message for a session
func (c *Client) SendMessage(sessionID, message string) error {
	return c.Emit(EventMessage, map[string]interface{}{
		"sid":     sessionID,
		"message": message,
	})
}

// UpdateMetadata sends a metadata update for a session
func (c *Client) UpdateMetadata(sessionID string, metadata string, version int64) (int64, error) {
	resp, err := c.EmitWithAck(string(EventUpdateMeta), map[string]interface{}{
		"sid":             sessionID,
		"metadata":        metadata,
		"expectedVersion": version,
	}, 5*time.Second)
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
	resp, err := c.EmitWithAck(string(EventUpdateState), map[string]interface{}{
		"sid":             sessionID,
		"agentState":      agentState,
		"expectedVersion": version,
	}, 5*time.Second)
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
	return c.Emit(EventSessionAlive, map[string]interface{}{
		"sid":      sessionID,
		"time":     time.Now().UnixMilli(),
		"thinking": thinking,
	})
}

// EmitEphemeral sends an ephemeral event (activity/thinking state)
// These events are not persisted, just broadcast to connected clients
func (c *Client) EmitEphemeral(data map[string]interface{}) error {
	return c.Emit(EventEphemeral, data)
}

// EmitMessage sends a session message to the server
// Messages contain encrypted Claude conversation content
func (c *Client) EmitMessage(data map[string]interface{}) error {
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
	defer c.mu.Unlock()

	if c.socket != nil {
		c.socket.Disconnect()
		c.socket = nil
	}

	c.connected = false
	return nil
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	sock := c.socket
	connected := c.connected
	c.mu.RUnlock()

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
