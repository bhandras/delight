package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/bhandras/delight/shared/logger"
)

// RemoteMessage represents a message to/from the bridge script
type RemoteMessage struct {
	Type      string                 `json:"type"`
	Content   interface{}            `json:"content,omitempty"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
	Message   json.RawMessage        `json:"message,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
	Request   json.RawMessage        `json:"request,omitempty"`
	Response  json.RawMessage        `json:"response,omitempty"`
	Error     string                 `json:"error,omitempty"`

	// SDK message fields
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Role      string          `json:"role,omitempty"`
	Model     string          `json:"model,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
	Tools     []string        `json:"tools,omitempty"`
	NumTurns  int             `json:"num_turns,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"`
	Result    string          `json:"result,omitempty"`

	// For parent tracking (sidechain)
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
}

// PermissionRequest represents a tool permission request
type PermissionRequest struct {
	Subtype  string          `json:"subtype"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

// PermissionResponse represents a tool permission response
type PermissionResponse struct {
	Behavior     string          `json:"behavior"` // "allow" or "deny"
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// PermissionHandler is called when Claude requests permission to use a tool
type PermissionHandler func(requestID string, toolName string, input json.RawMessage) (*PermissionResponse, error)

// MessageHandler is called when a message is received from Claude
type MessageHandler func(msg *RemoteMessage) error

// RemoteBridge manages communication with the Node.js Claude bridge
type RemoteBridge struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu    sync.Mutex
	debug bool

	// Channels
	messages    chan *RemoteMessage
	errors      chan error
	ready       chan struct{}
	stopCh      chan struct{}
	stdinWriter *json.Encoder

	// Handlers
	permissionHandler PermissionHandler
	messageHandler    MessageHandler

	// State
	sessionID string
	running   bool
}

// NewRemoteBridge creates a new remote bridge instance
func NewRemoteBridge(workDir string, resumeSessionID string, debug bool) (*RemoteBridge, error) {
	// Find bridge script
	bridgePath, err := findBridge()
	if err != nil {
		return nil, fmt.Errorf("failed to find bridge script: %w", err)
	}

	if debug {
		logger.Debugf("Using bridge at: %s", bridgePath)
	}

	// Build command arguments
	args := []string{bridgePath, "--cwd", workDir}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	if debug {
		args = append(args, "--debug")
	}

	cmd := exec.Command("node", args...)
	cmd.Dir = workDir

	// Set up environment
	cmd.Env = append(os.Environ(), buildNodePath()...)

	// Set up pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	return &RemoteBridge{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		debug:     debug,
		messages:  make(chan *RemoteMessage, 100),
		errors:    make(chan error, 10),
		ready:     make(chan struct{}),
		stopCh:    make(chan struct{}),
		sessionID: resumeSessionID,
	}, nil
}

// findBridge locates the claude_remote_bridge.cjs script
func findBridge() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	execDir := filepath.Dir(execPath)

	candidates := []string{
		filepath.Join(execDir, "scripts", "claude_remote_bridge.cjs"),
		filepath.Join(execDir, "..", "scripts", "claude_remote_bridge.cjs"),
		filepath.Join("scripts", "claude_remote_bridge.cjs"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return path, nil
			}
			return absPath, nil
		}
	}

	return "", fmt.Errorf("claude_remote_bridge.cjs not found in any of: %v", candidates)
}

// SetPermissionHandler sets the handler for permission requests
func (b *RemoteBridge) SetPermissionHandler(handler PermissionHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.permissionHandler = handler
}

// SetMessageHandler sets the handler for SDK messages
func (b *RemoteBridge) SetMessageHandler(handler MessageHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messageHandler = handler
}

// Start starts the bridge process and begins reading messages
func (b *RemoteBridge) Start() error {
	if b.debug {
		logger.Infof("Starting Claude remote bridge...")
	}

	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start bridge: %w", err)
	}

	b.mu.Lock()
	b.running = true
	b.stdinWriter = json.NewEncoder(b.stdin)
	b.mu.Unlock()

	// Start reading stdout (JSON messages)
	go b.readMessages()

	// Start reading stderr (debug logs)
	go b.readStderr()

	// Wait for ready signal
	select {
	case <-b.ready:
		if b.debug {
			logger.Infof("Bridge ready")
		}
	case err := <-b.errors:
		return fmt.Errorf("bridge error: %w", err)
	case <-time.After(15 * time.Second):
		return fmt.Errorf("bridge ready timeout")
	case <-b.stopCh:
		return fmt.Errorf("bridge stopped")
	}

	return nil
}

// readMessages reads JSON messages from the bridge's stdout
func (b *RemoteBridge) readMessages() {
	scanner := bufio.NewScanner(b.stdout)
	// Increase buffer size for large messages
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-b.stopCh:
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg RemoteMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			if b.debug {
				logger.Debugf("Invalid bridge message: %s (error: %v)", line, err)
			}
			continue
		}

		b.handleMessage(&msg)
	}

	if err := scanner.Err(); err != nil {
		if b.debug {
			logger.Debugf("Bridge stdout error: %v", err)
		}
		select {
		case <-b.stopCh:
			return
		case b.errors <- err:
		default:
		}
		return
	}

	// EOF without an explicit "ready" or "error" message can deadlock Start().
	select {
	case <-b.stopCh:
		return
	case b.errors <- io.EOF:
	default:
	}
}

// readStderr reads debug output from the bridge's stderr
func (b *RemoteBridge) readStderr() {
	scanner := bufio.NewScanner(b.stderr)
	for scanner.Scan() {
		if b.debug {
			logger.Debugf("[bridge stderr] %s", scanner.Text())
		}
	}
}

// handleMessage processes a message from the bridge
func (b *RemoteBridge) handleMessage(msg *RemoteMessage) {
	switch msg.Type {
	case "ready":
		select {
		case <-b.ready:
			// Already closed
		default:
			close(b.ready)
		}

	case "error":
		if b.debug {
			logger.Debugf("Bridge error: %s", msg.Error)
		}
		select {
		case b.errors <- fmt.Errorf("bridge error: %s", msg.Error):
		default:
		}

	case "control_request":
		// Permission request from Claude.
		//
		// Backward compatibility:
		// - If a PermissionHandler is installed, keep the legacy "handler returns a
		//   response" behavior (and the bridge sends control_response itself).
		// - If no PermissionHandler is installed, forward the control_request to
		//   the message handler and allow the consumer to respond by calling
		//   SendPermissionResponse.
		b.mu.Lock()
		handler := b.permissionHandler
		b.mu.Unlock()
		if handler != nil {
			go b.handlePermissionRequest(msg)
			return
		}
		b.forwardMessage(msg)

	case "system":
		// Track session ID from init message
		if msg.Subtype == "init" && msg.SessionID != "" {
			b.mu.Lock()
			b.sessionID = msg.SessionID
			b.mu.Unlock()
			if b.debug {
				logger.Debugf("Session ID: %s", msg.SessionID)
			}
		}
		b.forwardMessage(msg)

	case "assistant", "user", "result", "message", "raw":
		b.forwardMessage(msg)

	case "aborted":
		if b.debug {
			logger.Infof("Query aborted")
		}

	default:
		// Forward unknown messages to handler
		b.forwardMessage(msg)
	}
}

// handlePermissionRequest processes a tool permission request
func (b *RemoteBridge) handlePermissionRequest(msg *RemoteMessage) {
	b.mu.Lock()
	handler := b.permissionHandler
	b.mu.Unlock()

	if handler == nil {
		// No handler - auto-allow
		b.sendPermissionResponse(msg.RequestID, &PermissionResponse{Behavior: "allow"})
		return
	}

	// Parse the request
	var req PermissionRequest
	if err := json.Unmarshal(msg.Request, &req); err != nil {
		if b.debug {
			logger.Debugf("Failed to parse permission request: %v", err)
		}
		b.sendPermissionResponse(msg.RequestID, &PermissionResponse{
			Behavior: "deny",
			Message:  "Invalid permission request",
		})
		return
	}

	// Call the handler
	response, err := handler(msg.RequestID, req.ToolName, req.Input)
	if err != nil {
		if b.debug {
			logger.Debugf("Permission handler error: %v", err)
		}
		b.sendPermissionResponse(msg.RequestID, &PermissionResponse{
			Behavior: "deny",
			Message:  err.Error(),
		})
		return
	}

	b.sendPermissionResponse(msg.RequestID, response)
}

// sendPermissionResponse sends a permission response to the bridge
func (b *RemoteBridge) sendPermissionResponse(requestID string, response *PermissionResponse) {
	responseJSON, _ := json.Marshal(response)
	b.sendMessage(&RemoteMessage{
		Type:      "control_response",
		RequestID: requestID,
		Response:  responseJSON,
	})
}

// SendPermissionResponse sends a control_response message for the given request.
//
// This is intended for callers that consume "control_request" messages via the
// message handler and want to respond asynchronously (e.g. after a mobile user
// approves a permission prompt).
func (b *RemoteBridge) SendPermissionResponse(requestID string, response *PermissionResponse) error {
	if requestID == "" {
		return fmt.Errorf("missing request id")
	}
	if response == nil {
		response = &PermissionResponse{Behavior: "deny", Message: "missing response"}
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return b.sendMessage(&RemoteMessage{
		Type:      "control_response",
		RequestID: requestID,
		Response:  responseJSON,
	})
}

// forwardMessage sends a message to the message handler
func (b *RemoteBridge) forwardMessage(msg *RemoteMessage) {
	b.mu.Lock()
	handler := b.messageHandler
	b.mu.Unlock()

	if handler != nil {
		if err := handler(msg); err != nil && b.debug {
			logger.Debugf("Message handler error: %v", err)
		}
	}

	// Also send to channel for direct consumers
	select {
	case b.messages <- msg:
	default:
		if b.debug {
			logger.Debugf("Message channel full, dropping message")
		}
	}
}

// sendMessage sends a JSON message to the bridge via stdin
func (b *RemoteBridge) sendMessage(msg *RemoteMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running || b.stdinWriter == nil {
		return fmt.Errorf("bridge not running")
	}

	if b.debug {
		logger.Tracef("Sending to bridge: %s", msg.Type)
	}

	return b.stdinWriter.Encode(msg)
}

// SendUserMessage sends a user message/prompt to Claude
func (b *RemoteBridge) SendUserMessage(content string, meta map[string]interface{}) error {
	return b.sendMessage(&RemoteMessage{
		Type:    "user",
		Content: content,
		Meta:    meta,
	})
}

// Abort aborts the current query
func (b *RemoteBridge) Abort() error {
	return b.sendMessage(&RemoteMessage{Type: "abort"})
}

// Shutdown gracefully shuts down the bridge
func (b *RemoteBridge) Shutdown() error {
	return b.sendMessage(&RemoteMessage{Type: "shutdown"})
}

// Messages returns a channel of incoming messages
func (b *RemoteBridge) Messages() <-chan *RemoteMessage {
	return b.messages
}

// Errors returns a channel of errors
func (b *RemoteBridge) Errors() <-chan error {
	return b.errors
}

// GetSessionID returns the current Claude session ID
func (b *RemoteBridge) GetSessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessionID
}

// Wait waits for the bridge process to exit
func (b *RemoteBridge) Wait() error {
	if b.cmd == nil || b.cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	return b.cmd.Wait()
}

// Kill terminates the bridge process
func (b *RemoteBridge) Kill() error {
	b.mu.Lock()
	b.running = false
	b.mu.Unlock()

	select {
	case <-b.stopCh:
		// Already closed
	default:
		close(b.stopCh)
	}

	// Close stdin to signal shutdown
	if b.stdin != nil {
		b.stdin.Close()
	}

	if b.cmd == nil || b.cmd.Process == nil {
		return nil
	}

	if b.debug {
		logger.Debugf("Killing bridge process...")
	}

	// Best-effort: send Ctrl+C first so Node can flush and exit cleanly.
	_ = b.cmd.Process.Signal(os.Interrupt)
	time.Sleep(200 * time.Millisecond)

	return b.cmd.Process.Kill()
}

// IsRunning returns whether the bridge is running
func (b *RemoteBridge) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}
