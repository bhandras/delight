package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// LauncherMessage represents a message from the launcher script via fd 3
type LauncherMessage struct {
	Type      string `json:"type"`      // "uuid", "fetch-start", "fetch-end"
	Value     string `json:"value"`     // For uuid
	ID        int    `json:"id"`        // For fetch tracking
	Hostname  string `json:"hostname"`  // For fetch-start
	Path      string `json:"path"`      // For fetch-start
	Method    string `json:"method"`    // For fetch-start
	Timestamp int64  `json:"timestamp"` // For all events
}

// Process represents a running Claude Code process with advanced tracking
type Process struct {
	cmd   *exec.Cmd
	mu    sync.Mutex
	debug bool

	// fd 3 communication
	fd3Reader *os.File
	fd3Writer *os.File

	// Session tracking
	claudeSessionID string
	sessionIDCh     chan string

	// Thinking state tracking
	activeFetches map[int]bool
	thinkingCh    chan bool
	thinking      bool

	// Stop channel for cleanup
	stopCh chan struct{}
}

// NewProcess creates a new Claude process wrapper with fd 3 tracking
// Uses our launcher script to intercept UUID and fetch events
func NewProcess(workDir string, debug bool) (*Process, error) {
	// Find launcher script
	launcherPath, err := findLauncher()
	if err != nil {
		return nil, fmt.Errorf("failed to find launcher: %w", err)
	}

	if debug {
		log.Printf("Using launcher at: %s", launcherPath)
	}

	// Create pipe for fd 3 communication
	fd3Reader, fd3Writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create fd3 pipe: %w", err)
	}

	// Create command using our launcher script
	cmd := exec.Command("node", launcherPath)
	cmd.Dir = workDir

	// Set up environment with NODE_PATH for module resolution
	// The launcher script needs to find @anthropic-ai/claude-code
	cmd.Env = append(os.Environ(), buildNodePath()...)

	// stdio: [inherit, inherit, inherit, pipe]
	// stdin, stdout, stderr are inherited for Claude's interactive TUI
	// fd 3 (ExtraFiles[0]) is our pipe for receiving events
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{fd3Writer}

	return &Process{
		cmd:           cmd,
		debug:         debug,
		fd3Reader:     fd3Reader,
		fd3Writer:     fd3Writer,
		sessionIDCh:   make(chan string, 1),
		thinkingCh:    make(chan bool, 10),
		activeFetches: make(map[int]bool),
		stopCh:        make(chan struct{}),
	}, nil
}

// findLauncher locates the claude_launcher.cjs script
func findLauncher() (string, error) {
	// Get the directory of the happy executable
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	execDir := filepath.Dir(execPath)

	// Try several locations
	candidates := []string{
		// Relative to executable
		filepath.Join(execDir, "scripts", "claude_launcher.cjs"),
		filepath.Join(execDir, "..", "scripts", "claude_launcher.cjs"),
		// Development path - relative to current working directory
		filepath.Join("scripts", "claude_launcher.cjs"),
	}

	// Also try from GOPATH/src location for development
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		homeDir, _ := os.UserHomeDir()
		candidates = append(candidates,
			filepath.Join(homeDir, "work", "happy-workspace", "delight", "cli", "scripts", "claude_launcher.cjs"),
		)
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

	return "", fmt.Errorf("claude_launcher.cjs not found in any of: %v", candidates)
}

// buildNodePath builds the NODE_PATH environment variable to help Node.js
// find the @anthropic-ai/claude-code package in common locations
func buildNodePath() []string {
	var nodePaths []string

	homeDir, _ := os.UserHomeDir()

	// Common locations where claude-code might be installed
	candidates := []string{
		// Global npm modules (homebrew on macOS)
		"/opt/homebrew/lib/node_modules",
		"/usr/local/lib/node_modules",
		// Global npm modules (Linux)
		"/usr/lib/node_modules",
		// User's local npm global
		filepath.Join(homeDir, ".npm/lib/node_modules"),
		// npm global prefix
		filepath.Join(homeDir, ".nvm/versions/node"),
		// Also check task-master-ai's node_modules (where claude-code is often a dep)
		"/opt/homebrew/lib/node_modules/task-master-ai/node_modules",
		// Delight CLI's node_modules (sibling project)
		filepath.Join(homeDir, "work/happy-workspace/delight/cli/node_modules"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			nodePaths = append(nodePaths, path)
		}
	}

	if len(nodePaths) > 0 {
		return []string{"NODE_PATH=" + filepath.Join(nodePaths[0], "..") + ":" + strings.Join(nodePaths, ":")}
	}

	return nil
}

// Start starts the Claude process and begins reading fd 3 messages
func (p *Process) Start() error {
	if p.debug {
		log.Println("Starting Claude Code process (interactive mode with fd3 tracking)...")
	}

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}

	// Close write end in parent process (child has it via ExtraFiles)
	p.fd3Writer.Close()

	// Start reading fd 3 messages in background
	go p.readFd3Messages()

	if p.debug {
		log.Printf("Claude Code started (PID: %d)", p.cmd.Process.Pid)
	}

	return nil
}

// readFd3Messages reads JSON messages from the launcher via fd 3
func (p *Process) readFd3Messages() {
	scanner := bufio.NewScanner(p.fd3Reader)

	for scanner.Scan() {
		select {
		case <-p.stopCh:
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg LauncherMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			if p.debug {
				log.Printf("Invalid fd3 message: %s (error: %v)", line, err)
			}
			continue
		}

		switch msg.Type {
		case "uuid":
			p.handleUUID(msg.Value)
		case "fetch-start":
			p.handleFetchStart(msg.ID)
		case "fetch-end":
			p.handleFetchEnd(msg.ID)
		default:
			if p.debug {
				log.Printf("Unknown fd3 message type: %s", msg.Type)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if p.debug {
			log.Printf("fd3 reader error: %v", err)
		}
	}
}

// handleUUID processes a UUID event (potential session ID)
func (p *Process) handleUUID(uuid string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.debug {
		log.Printf("UUID intercepted: %s", uuid)
	}

	// First UUID is likely the Claude session ID
	// Send it to anyone waiting (non-blocking)
	if p.claudeSessionID == "" {
		p.claudeSessionID = uuid
		select {
		case p.sessionIDCh <- uuid:
		default:
		}
	}
}

// handleFetchStart marks a fetch as active (thinking = true)
func (p *Process) handleFetchStart(id int) {
	p.mu.Lock()
	wasEmpty := len(p.activeFetches) == 0
	p.activeFetches[id] = true
	p.mu.Unlock()

	if wasEmpty {
		p.setThinking(true)
	}

	if p.debug {
		log.Printf("Fetch started: %d (active: %d)", id, len(p.activeFetches))
	}
}

// handleFetchEnd marks a fetch as complete
func (p *Process) handleFetchEnd(id int) {
	p.mu.Lock()
	delete(p.activeFetches, id)
	isEmpty := len(p.activeFetches) == 0
	p.mu.Unlock()

	if p.debug {
		log.Printf("Fetch ended: %d (active: %d)", id, len(p.activeFetches))
	}

	if isEmpty {
		// Debounce: wait 500ms before setting thinking=false
		// This prevents flickering during rapid fetch sequences
		go func() {
			time.Sleep(500 * time.Millisecond)
			p.mu.Lock()
			stillEmpty := len(p.activeFetches) == 0
			p.mu.Unlock()

			if stillEmpty {
				p.setThinking(false)
			}
		}()
	}
}

// setThinking updates the thinking state and notifies listeners
func (p *Process) setThinking(thinking bool) {
	p.mu.Lock()
	if p.thinking == thinking {
		p.mu.Unlock()
		return
	}
	p.thinking = thinking
	p.mu.Unlock()

	// Non-blocking send to channel
	select {
	case p.thinkingCh <- thinking:
	default:
	}

	if p.debug {
		log.Printf("Thinking state: %v", thinking)
	}
}

// SessionID returns a channel that receives the Claude session ID when detected
func (p *Process) SessionID() <-chan string {
	return p.sessionIDCh
}

// Thinking returns a channel that receives thinking state changes
func (p *Process) Thinking() <-chan bool {
	return p.thinkingCh
}

// IsThinking returns the current thinking state
func (p *Process) IsThinking() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.thinking
}

// GetSessionID returns the detected Claude session ID (empty if not yet detected)
func (p *Process) GetSessionID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.claudeSessionID
}

// Wait waits for the Claude process to exit
func (p *Process) Wait() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	return p.cmd.Wait()
}

// Kill terminates the Claude process
func (p *Process) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Signal stop to goroutines
	select {
	case <-p.stopCh:
		// Already closed
	default:
		close(p.stopCh)
	}

	// Close fd3 reader
	if p.fd3Reader != nil {
		p.fd3Reader.Close()
	}

	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	if p.debug {
		log.Println("Killing Claude process...")
	}

	return p.cmd.Process.Kill()
}

// IsRunning returns whether the process is still running
func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}

	// Try to send signal 0 to check if process exists
	err := p.cmd.Process.Signal(os.Signal(nil))
	return err == nil
}
