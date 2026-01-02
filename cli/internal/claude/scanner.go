package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/shared/logger"
)

// SessionMessage represents a message from Claude's session file
type SessionMessage struct {
	UUID       string          `json:"uuid"`
	ParentUUID *string         `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Message    json.RawMessage `json:"message"`
	Type       string          `json:"type"` // "user", "assistant", "summary", etc.
	Timestamp  time.Time       `json:"timestamp,omitempty"`
}

// Scanner watches Claude session files for new messages
type Scanner struct {
	projectPath    string
	sessionID      string
	lastPosition   int64
	processedUUIDs sync.Map // For deduplication across session resumes
	messageCh      chan *SessionMessage
	stopCh         chan struct{}
	debug          bool
	mu             sync.Mutex
}

// NewScanner creates a session file scanner
func NewScanner(projectPath, sessionID string, debug bool) *Scanner {
	return &Scanner{
		projectPath: projectPath,
		sessionID:   sessionID,
		messageCh:   make(chan *SessionMessage, 100),
		stopCh:      make(chan struct{}),
		debug:       debug,
	}
}

var projectPathSanitizer = regexp.MustCompile(`[\\\/\.:]`)

// getProjectDir returns the directory where Claude stores per-project session
// files, based on the configured Claude config root (or the default).
func getProjectDir(projectPath string) string {
	claudeConfigDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeConfigDir == "" {
		homeDir, _ := os.UserHomeDir()
		claudeConfigDir = filepath.Join(homeDir, ".claude")
	}
	projectID := projectPathSanitizer.ReplaceAllString(filepath.Clean(projectPath), "-")
	return filepath.Join(claudeConfigDir, "projects", projectID)
}

// SessionFilePath returns the path to Claude's session file
// Format: ~/.claude/projects/<project-hash>/<session-id>.jsonl
func (s *Scanner) SessionFilePath() string {
	projectDir := getProjectDir(s.projectPath)
	return filepath.Join(projectDir, s.sessionID+".jsonl")
}

// Start begins watching the session file for new messages
func (s *Scanner) Start() {
	go s.watchLoop()
}

// Stop stops the scanner
func (s *Scanner) Stop() {
	select {
	case <-s.stopCh:
		// Already closed
	default:
		close(s.stopCh)
	}
}

// Messages returns a channel receiving new session messages
func (s *Scanner) Messages() <-chan *SessionMessage {
	return s.messageCh
}

// watchLoop polls the session file for new content
func (s *Scanner) watchLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	filePath := s.SessionFilePath()

	if s.debug {
		logger.Debugf("Scanner watching: %s", filePath)
	}

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.scanFile(filePath)
		}
	}
}

// scanFile reads new lines from the session file
func (s *Scanner) scanFile(filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		// File may not exist yet, this is normal
		return
	}
	defer file.Close()

	s.mu.Lock()
	lastPos := s.lastPosition
	s.mu.Unlock()

	// Seek to last position
	if lastPos > 0 {
		_, err := file.Seek(lastPos, 0)
		if err != nil {
			if s.debug {
				logger.Debugf("Failed to seek: %v", err)
			}
			return
		}
	}

	scanner := bufio.NewScanner(file)

	// Increase buffer size for large messages
	const maxScannerBuffer = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, maxScannerBuffer)
	scanner.Buffer(buf, maxScannerBuffer)

	for scanner.Scan() {
		select {
		case <-s.stopCh:
			return
		default:
		}

		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var msg SessionMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			if s.debug {
				// Only log if it's not just a truncated line at end of file
				logger.Debugf("Failed to parse session line: %v", err)
			}
			continue
		}

		// Deduplicate by UUID
		// This handles the case when Claude resumes a session and replays history
		if msg.UUID != "" {
			if _, exists := s.processedUUIDs.LoadOrStore(msg.UUID, true); exists {
				continue // Already processed this message
			}
		}

		// Add timestamp if not present
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now()
		}

		if s.debug {
			logger.Debugf("New message: type=%s uuid=%s", msg.Type, msg.UUID)
		}

		// Send message (non-blocking if channel is full)
		select {
		case s.messageCh <- &msg:
		default:
			if s.debug {
				logger.Debugf("Message channel full, dropping message")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if s.debug {
			logger.Debugf("Scanner error: %v", err)
		}
	}

	// Update position
	pos, err := file.Seek(0, 1) // Get current position
	if err == nil {
		s.mu.Lock()
		s.lastPosition = pos
		s.mu.Unlock()
	}
}

// VerifySessionFile checks if a session file exists for the given session ID
// This is used for dual verification of session detection
func VerifySessionFile(projectPath, sessionID string) bool {
	projectDir := getProjectDir(projectPath)
	filePath := filepath.Join(projectDir, sessionID+".jsonl")

	_, err := os.Stat(filePath)
	return err == nil
}

// WaitForSessionFile waits for a session file to appear (with timeout)
// Returns true if file appears within timeout, false otherwise
func WaitForSessionFile(projectPath, sessionID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if VerifySessionFile(projectPath, sessionID) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
