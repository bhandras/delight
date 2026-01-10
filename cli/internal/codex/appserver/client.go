package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bhandras/delight/shared/logger"
)

const (
	// defaultRequestTimeout bounds how long we wait for a single request/response
	// round-trip with the app-server.
	defaultRequestTimeout = 30 * time.Second

	// stdoutScannerMaxToken bounds the size of a single JSONL message we accept
	// from the app-server.
	//
	// bufio.Scanner defaults to a 64KiB token limit; codex app-server messages
	// can exceed that when they include aggregated tool output or file patches.
	stdoutScannerMaxToken = 8 * 1024 * 1024

	// stderrCopyLimit bounds the amount of stderr we keep in memory for error
	// reporting.
	stderrCopyLimit = 64 * 1024
)

// ErrClosed is returned when a request cannot complete because the app-server
// client has been closed.
var ErrClosed = errors.New("codex app-server client closed")

// NotificationHandler receives server-initiated JSON-RPC notifications.
type NotificationHandler func(method string, params json.RawMessage)

// RequestHandler receives server-initiated JSON-RPC requests.
//
// Most integrations use this for approvals. Delight typically runs in
// `approvalPolicy: "never"` for remote control, but the handler is kept so we
// can fail safe if Codex requests an approval unexpectedly.
type RequestHandler func(method string, params json.RawMessage) (json.RawMessage, *RPCError)

// Client manages a `codex app-server` subprocess and provides a request/notify
// interface over its JSONL stdio protocol.
type Client struct {
	debug bool

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu         sync.Mutex
	nextID     int64
	pending    map[int64]chan rpcResponse
	closed     bool
	started    bool
	notifyFn   NotificationHandler
	requestFn  RequestHandler
	waitOnce   sync.Once
	waitErr    error
	waitCh     chan struct{}
	stderrTail []byte
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type rpcMessage struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error payload shape used by codex app-server.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewClient creates an unstarted app-server client.
func NewClient(debug bool) *Client {
	return &Client{
		debug:   debug,
		pending: make(map[int64]chan rpcResponse),
		waitCh:  make(chan struct{}),
	}
}

// SetNotificationHandler sets the handler for server notifications.
func (c *Client) SetNotificationHandler(handler NotificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyFn = handler
}

// SetRequestHandler sets the handler for server-initiated JSON-RPC requests.
func (c *Client) SetRequestHandler(handler RequestHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestFn = handler
}

// Start spawns the `codex app-server` process and begins reading its stdout
// event stream. It performs the required initialize/initialized handshake.
func (c *Client) Start(ctx context.Context, clientName string, clientVersion string) error {
	if c == nil {
		return fmt.Errorf("app-server client is nil")
	}

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()

	cmd := exec.Command("codex", "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	c.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return err
	}

	go c.readStdout()
	go c.readStderr()

	if err := c.initialize(ctx, clientName, clientVersion); err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

// Close terminates the app-server process and stops background goroutines.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cmd := c.cmd
	pending := c.pending
	c.pending = make(map[int64]chan rpcResponse)
	c.mu.Unlock()

	for _, ch := range pending {
		select {
		case ch <- rpcResponse{err: ErrClosed}:
		default:
		}
	}

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

// Wait waits for the underlying app-server process to exit.
func (c *Client) Wait() error {
	if c == nil {
		return nil
	}
	c.waitOnce.Do(func() {
		defer close(c.waitCh)
		c.mu.Lock()
		cmd := c.cmd
		c.mu.Unlock()
		if cmd == nil {
			c.waitErr = nil
			return
		}
		c.waitErr = cmd.Wait()
	})
	<-c.waitCh
	return c.waitErr
}

// Notify sends a JSON-RPC notification (no id).
func (c *Client) Notify(method string, params any) error {
	if c == nil {
		return fmt.Errorf("app-server client is nil")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	msg := rpcMessage{
		Method: method,
		Params: raw,
	}
	return c.send(msg)
}

// Call sends a JSON-RPC request and waits for a response.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("app-server client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > defaultRequestTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultRequestTimeout)
		defer cancel()
	}

	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	id := atomic.AddInt64(&c.nextID, 1)
	idRaw, _ := json.Marshal(id)
	idMsg := json.RawMessage(idRaw)

	respCh := make(chan rpcResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.pending[id] = respCh
	c.mu.Unlock()

	if err := c.send(rpcMessage{ID: &idMsg, Method: method, Params: rawParams}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-respCh:
		return resp.result, resp.err
	}
}

// initialize performs the required app-server handshake.
func (c *Client) initialize(ctx context.Context, clientName string, clientVersion string) error {
	if stringsTrim(clientName) == "" {
		clientName = "delight"
	}
	if stringsTrim(clientVersion) == "" {
		clientVersion = "unknown"
	}

	_, err := c.Call(ctx, MethodInitialize, map[string]any{
		"clientInfo": map[string]any{
			"name":    clientName,
			"title":   "Delight",
			"version": clientVersion,
		},
	})
	if err != nil {
		return err
	}
	return c.Notify(MethodInitialized, map[string]any{})
}

// readStdout reads newline-delimited JSON messages from the app-server and
// dispatches notifications, requests, and responses.
func (c *Client) readStdout() {
	c.mu.Lock()
	r := c.stdout
	debug := c.debug
	c.mu.Unlock()
	if r == nil {
		return
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), stdoutScannerMaxToken)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			logger.Warnf("app-server: dropped invalid JSONL message (len=%d): %v", len(line), err)
			continue
		}

		if msg.Method != "" && msg.ID == nil {
			if debug {
				logger.Debugf("app-server: notify %s (bytes=%d)", msg.Method, len(line))
			}
			c.dispatchNotification(msg.Method, msg.Params)
			continue
		}
		if msg.Method != "" && msg.ID != nil {
			if debug {
				logger.Debugf("app-server: request %s (bytes=%d)", msg.Method, len(line))
			}
			c.dispatchRequest(*msg.ID, msg.Method, msg.Params)
			continue
		}
		if msg.Method == "" && msg.ID != nil {
			if debug && msg.Error != nil {
				logger.Debugf("app-server: response error (bytes=%d): %s", len(line), msg.Error.Message)
			}
			c.dispatchResponse(*msg.ID, msg.Result, msg.Error)
			continue
		}

		if debug {
			logger.Debugf("app-server: ignored message: %s", string(line))
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Errorf("app-server: stdout stream ended with error: %v", err)
	}
	_ = c.Close()
}

// readStderr captures a bounded tail of stderr for diagnostics.
func (c *Client) readStderr() {
	c.mu.Lock()
	r := c.stderr
	c.mu.Unlock()
	if r == nil {
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.appendStderrTail(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// appendStderrTail appends bytes to stderrTail, keeping only the last N bytes.
func (c *Client) appendStderrTail(chunk []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(chunk) == 0 {
		return
	}
	c.stderrTail = append(c.stderrTail, chunk...)
	if len(c.stderrTail) > stderrCopyLimit {
		c.stderrTail = c.stderrTail[len(c.stderrTail)-stderrCopyLimit:]
	}
}

// dispatchNotification forwards a server notification to the handler.
func (c *Client) dispatchNotification(method string, params json.RawMessage) {
	c.mu.Lock()
	handler := c.notifyFn
	debug := c.debug
	c.mu.Unlock()
	if handler == nil {
		if debug {
			logger.Warnf("app-server: notification dropped (no handler): %s", method)
		}
		return
	}
	handler(method, params)
}

// dispatchRequest handles a server-initiated request by calling requestFn and
// writing a response payload.
func (c *Client) dispatchRequest(id json.RawMessage, method string, params json.RawMessage) {
	c.mu.Lock()
	handler := c.requestFn
	debug := c.debug
	c.mu.Unlock()

	var result json.RawMessage
	var rpcErr *RPCError
	if handler == nil {
		if debug {
			logger.Warnf("app-server: request dropped (no handler): %s", method)
		}
		rpcErr = &RPCError{Code: -32601, Message: "request handler not configured"}
	} else {
		result, rpcErr = handler(method, params)
	}

	if rpcErr != nil {
		_ = c.send(rpcMessage{ID: &id, Error: rpcErr})
		return
	}
	_ = c.send(rpcMessage{ID: &id, Result: result})
}

// dispatchResponse resolves a pending request.
func (c *Client) dispatchResponse(id json.RawMessage, result json.RawMessage, rpcErr *RPCError) {
	idNum, ok := parseNumericID(id)
	if !ok {
		return
	}

	c.mu.Lock()
	ch := c.pending[idNum]
	delete(c.pending, idNum)
	c.mu.Unlock()
	if ch == nil {
		return
	}

	if rpcErr != nil {
		ch <- rpcResponse{err: errors.New(rpcErr.Message)}
		return
	}
	ch <- rpcResponse{result: result, err: nil}
}

// send writes a single JSON-RPC message to stdin.
func (c *Client) send(msg rpcMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if c.stdin == nil {
		return fmt.Errorf("app-server stdin not initialized")
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = c.stdin.Write(raw)
	return err
}

// parseNumericID parses a JSON-RPC id field that is expected to be a number.
func parseNumericID(id json.RawMessage) (int64, bool) {
	var n int64
	if err := json.Unmarshal(id, &n); err != nil {
		return 0, false
	}
	return n, true
}

// stringsTrim trims whitespace without importing strings in every file.
func stringsTrim(s string) string {
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\n' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\n' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	if i == 0 && j == len(s) {
		return s
	}
	return s[i:j]
}
