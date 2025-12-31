package sdk

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/websocket"
)

const (
	// defaultHTTPTimeout is the per-request timeout used by the SDK HTTP client.
	defaultHTTPTimeout = 15 * time.Second
	// defaultDispatcherQueueSize is the mailbox size used by SDK dispatchers.
	defaultDispatcherQueueSize = 256
)

// Listener receives SDK events. Methods must be safe to call from any goroutine.
type Listener interface {
	// OnConnected is called after the SDK establishes its websocket connection.
	OnConnected()
	// OnDisconnected is called after the websocket disconnects.
	OnDisconnected(reason string)
	// OnUpdate delivers a server update or synthetic SDK update payload.
	OnUpdate(sessionID string, updateJSON string)
	// OnError delivers non-fatal SDK errors for display/logging.
	OnError(message string)
}

// Client is a minimal mobile SDK client suitable for gomobile.
//
// Client owns:
// - transport lifecycle (Socket.IO + HTTP)
// - encryption keys (master key, per-session data keys)
// - derived session UI state used by view layers
//
// UI layers should be pure views. They should not re-derive control state from
// raw agentState, and should instead render the derived "ui" map injected into
// ListSessions and synthetic "session-ui" updates emitted by this SDK.
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
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
		dispatch:   newDispatcher(defaultDispatcherQueueSize),
		callbacks:  newDispatcher(defaultDispatcherQueueSize),
	}
}

// SetServerURL updates the base server URL used for HTTP requests and
// Socket.IO connections.
//
// The SDK expects a URL without a trailing slash, because request paths are
// joined as `baseURL + "/v1/..."`.
func (c *Client) SetServerURL(serverURL string) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.setServerURL(serverURL)
		return nil, nil
	})
}

// setServerURL sets the base server URL without the SDK dispatch queue.
func (c *Client) setServerURL(serverURL string) {
	serverURL = strings.TrimRight(serverURL, "/")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.serverURL = serverURL
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
