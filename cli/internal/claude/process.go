package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bhandras/delight/shared/logger"
	"github.com/creack/pty"
	"golang.org/x/term"
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
	ptyFile   *os.File
	ttyFile   *os.File
	ownsTTY   bool
	ttyState  *term.State
	ttyFD     int

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

func (p *Process) restoreTTYLocked() {
	if p.ttyState == nil {
		return
	}
	if p.ttyFD >= 0 {
		_ = term.Restore(p.ttyFD, p.ttyState)
	}
	p.ttyState = nil
	p.ttyFD = -1
}

// NewProcess creates a new Claude process wrapper with fd 3 tracking.
//
// When resumeToken is non-empty, the underlying Claude Code CLI is started with
// `--resume <resumeToken>` so local mode can continue an existing session.
//
// Uses our launcher script to intercept UUID and fetch events.
func NewProcess(workDir string, resumeToken string, debug bool) (*Process, error) {
	// Find launcher script
	launcherPath, err := findLauncher()
	if err != nil {
		return nil, fmt.Errorf("failed to find launcher: %w", err)
	}

	if debug {
		logger.Debugf("Using launcher at: %s", launcherPath)
	}

	// Create pipe for fd 3 communication
	fd3Reader, fd3Writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create fd3 pipe: %w", err)
	}

	// Create command using our launcher script
	args := []string{launcherPath}
	resumeToken = strings.TrimSpace(resumeToken)
	if resumeToken != "" {
		args = append(args, "--resume", resumeToken)
	}
	cmd := exec.Command("node", args...)
	cmd.Dir = workDir

	// Set up environment with NODE_PATH for module resolution
	// The launcher script needs to find @anthropic-ai/claude-code
	cmd.Env = append(os.Environ(), buildNodePath()...)

	// stdio: [pty, pty, pty, pipe]
	// stdin, stdout, stderr are attached to a PTY for Claude's interactive TUI
	// fd 3 (ExtraFiles[0]) is our pipe for receiving events
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
		ttyFD:         -1,
	}, nil
}

// findLauncher locates the claude_launcher.cjs script
func findLauncher() (string, error) {
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
	execPath, _ := os.Executable()
	execDir := ""
	if execPath != "" {
		execDir = filepath.Dir(execPath)
	}
	cwd, _ := os.Getwd()

	// Common locations where claude-code might be installed
	candidates := []string{
		// Local project install locations (useful in development and when bundled).
		filepath.Join(execDir, "node_modules"),
		filepath.Join(execDir, "..", "node_modules"),
		filepath.Join(cwd, "node_modules"),
		filepath.Join(cwd, "cli", "node_modules"),
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
	}

	existing := filepath.SplitList(os.Getenv("NODE_PATH"))
	for _, path := range existing {
		if path != "" {
			nodePaths = append(nodePaths, path)
		}
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			nodePaths = append(nodePaths, path)
		}
	}

	if len(nodePaths) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(nodePaths))
	deduped := make([]string, 0, len(nodePaths))
	for _, path := range nodePaths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		deduped = append(deduped, path)
	}

	// Only override NODE_PATH if we found at least one existing directory to
	// include. Use the platform path list separator (':' on unix, ';' on windows).
	return []string{"NODE_PATH=" + strings.Join(deduped, string(os.PathListSeparator))}
}

// Start starts the Claude process and begins reading fd 3 messages
func (p *Process) Start() error {
	if p.debug {
		logger.Infof("Starting Claude Code process (interactive mode with fd3 tracking)...")
	}

	ptyFile, err := pty.Start(p.cmd)
	if err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}
	p.ptyFile = ptyFile

	// Avoid reading directly from os.Stdin so we can reliably stop consuming terminal
	// input when switching to remote mode. Using /dev/tty provides a handle we can
	// close on Kill(), unblocking the copy goroutine immediately.
	tty := os.Stdin
	ownsTTY := false
	if term.IsTerminal(int(os.Stdin.Fd())) {
		ttyFile, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err == nil {
			tty = ttyFile
			p.ttyFile = ttyFile
			ownsTTY = true
		}
	}
	p.ownsTTY = ownsTTY

	// Put the user's terminal in raw mode so the Claude interactive UI gets
	// keystrokes immediately (enter, arrows, ctrl keys, etc.).
	p.ttyFD = -1
	if term.IsTerminal(int(tty.Fd())) {
		fd := int(tty.Fd())
		if state, err := term.MakeRaw(fd); err == nil {
			p.ttyState = state
			p.ttyFD = fd
		}
	}

	_ = pty.InheritSize(tty, ptyFile)

	go func() {
		_, _ = io.Copy(os.Stdout, ptyFile)
	}()
	go func() {
		_, _ = io.Copy(ptyFile, tty)
	}()
	go p.watchWindowSize()

	// Close write end in parent process (child has it via ExtraFiles)
	p.fd3Writer.Close()

	// Start reading fd 3 messages in background
	go p.readFd3Messages()

	if p.debug {
		logger.Infof("Claude Code started (PID: %d)", p.cmd.Process.Pid)
	}

	return nil
}

func (p *Process) watchWindowSize() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		select {
		case <-p.stopCh:
			return
		case <-ch:
			if p.ptyFile != nil {
				tty := os.Stdin
				if p.ttyFile != nil {
					tty = p.ttyFile
				}
				_ = pty.InheritSize(tty, p.ptyFile)
			}
		}
	}
}

// readFd3Messages reads JSON messages from the launcher via fd 3
func (p *Process) readFd3Messages() {
	scanner := bufio.NewScanner(p.fd3Reader)
	const maxScannerBuffer = 10 * 1024 * 1024
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, maxScannerBuffer)

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
				logger.Debugf("Invalid fd3 message: %s (error: %v)", line, err)
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
				logger.Debugf("Unknown fd3 message type: %s", msg.Type)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if p.debug {
			logger.Debugf("fd3 reader error: %v", err)
		}
	}
}

// handleUUID processes a UUID event (potential session ID)
func (p *Process) handleUUID(uuid string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.debug {
		logger.Debugf("UUID intercepted: %s", uuid)
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
		logger.Tracef("Fetch started: %d (active: %d)", id, len(p.activeFetches))
	}
}

// handleFetchEnd marks a fetch as complete
func (p *Process) handleFetchEnd(id int) {
	p.mu.Lock()
	delete(p.activeFetches, id)
	isEmpty := len(p.activeFetches) == 0
	p.mu.Unlock()

	if p.debug {
		logger.Tracef("Fetch ended: %d (active: %d)", id, len(p.activeFetches))
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
		logger.Debugf("Thinking state: %v", thinking)
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
	err := p.cmd.Wait()
	p.mu.Lock()
	p.restoreTTYLocked()
	p.mu.Unlock()
	return err
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
	// Close our TTY handle to stop stdin->PTY copying goroutines immediately.
	p.restoreTTYLocked()
	if p.ownsTTY && p.ttyFile != nil {
		_ = p.ttyFile.Close()
		p.ttyFile = nil
		p.ownsTTY = false
	}
	if p.ptyFile != nil {
		_ = p.ptyFile.Close()
	}

	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	if p.debug {
		logger.Infof("Killing Claude process...")
	}

	// Best-effort: send Ctrl+C first so Claude can restore terminal state.
	_ = p.cmd.Process.Signal(os.Interrupt)
	go func(cmd *exec.Cmd) {
		time.Sleep(500 * time.Millisecond)
		if cmd == nil || cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
	}(p.cmd)
	return nil
}

// SendInput injects input into the Claude TUI (as if typed by the user).
func (p *Process) SendInput(text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ptyFile == nil {
		return fmt.Errorf("pty not initialized")
	}

	_, err := io.WriteString(p.ptyFile, text)
	return err
}

// SendLine injects text and then presses Enter, with a small delay to avoid paste buffering.
func (p *Process) SendLine(text string) error {
	p.mu.Lock()
	if p.ptyFile == nil {
		p.mu.Unlock()
		return fmt.Errorf("pty not initialized")
	}
	_, err := io.WriteString(p.ptyFile, text)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()

	time.Sleep(50 * time.Millisecond)
	return p.SendInput("\r")
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
