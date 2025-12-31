package session

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type remoteInputRecord struct {
	text string
	at   time.Time
}

type outboundLocalIDRecord struct {
	id string
	at time.Time
}

const ansiClearLine = "\r\033[2K"

var whitespaceRE = regexp.MustCompile(`\s+`)

func normalizeRemoteInputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	return text
}

func remoteTermWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err == nil && width > 20 {
		return width
	}
	tty, err := os.Open("/dev/tty")
	if err == nil {
		defer tty.Close()
		width, _, err = term.GetSize(int(tty.Fd()))
		if err == nil && width > 20 {
			return width
		}
	}
	return 100
}

func writeRemoteLine(line string) {
	// Make sure remote logs always start at column 0 even if the previous local
	// TUI left the cursor mid-line.
	fmt.Fprint(os.Stdout, ansiClearLine)
	fmt.Fprintln(os.Stdout, line)
}

func writeRemoteBlankLine() {
	fmt.Fprint(os.Stdout, ansiClearLine)
	fmt.Fprintln(os.Stdout, "")
}

func wrapWithPrefix(text, prefix string, width int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return []string{prefix}
	}

	indent := strings.Repeat(" ", len(prefix))
	max := width - len(prefix)
	if max < 10 {
		max = 10
	}

	var out []string
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			out = append(out, prefix)
			continue
		}

		// Collapsing whitespace tends to make streamed output look much closer to
		// the mobile rendering.
		line = whitespaceRE.ReplaceAllString(line, " ")
		words := strings.Fields(line)
		if len(words) == 0 {
			out = append(out, prefix)
			continue
		}

		cur := ""
		for _, w := range words {
			if cur == "" {
				cur = w
				continue
			}
			if len(cur)+1+len(w) <= max {
				cur = cur + " " + w
				continue
			}
			out = append(out, prefix+cur)
			cur = w
			prefix = indent
			max = width - len(prefix)
			if max < 10 {
				max = 10
			}
		}
		if cur != "" {
			out = append(out, prefix+cur)
		}
		prefix = indent
		max = width - len(prefix)
		if max < 10 {
			max = 10
		}
	}
	return out
}

func (m *Manager) rememberRemoteInput(text string) {
	text = normalizeRemoteInputText(text)
	if text == "" {
		return
	}

	now := time.Now()

	m.recentRemoteInputsMu.Lock()
	defer m.recentRemoteInputsMu.Unlock()

	// Keep a small, time-bounded list; most sessions only need a handful of recent inputs.
	const maxItems = 64
	const ttl = 20 * time.Second

	// Drop expired entries.
	cutoff := now.Add(-ttl)
	dst := m.recentRemoteInputs[:0]
	for _, rec := range m.recentRemoteInputs {
		if rec.at.After(cutoff) {
			dst = append(dst, rec)
		}
	}
	m.recentRemoteInputs = dst

	m.recentRemoteInputs = append(m.recentRemoteInputs, remoteInputRecord{text: text, at: now})
	if len(m.recentRemoteInputs) > maxItems {
		m.recentRemoteInputs = m.recentRemoteInputs[len(m.recentRemoteInputs)-maxItems:]
	}
}

func (m *Manager) isRecentlyInjectedRemoteInput(text string) bool {
	text = normalizeRemoteInputText(text)
	if text == "" {
		return false
	}

	now := time.Now()
	const ttl = 20 * time.Second
	cutoff := now.Add(-ttl)

	m.recentRemoteInputsMu.Lock()
	defer m.recentRemoteInputsMu.Unlock()

	// Iterate newest-first.
	for i := len(m.recentRemoteInputs) - 1; i >= 0; i-- {
		rec := m.recentRemoteInputs[i]
		if rec.at.Before(cutoff) {
			break
		}
		if rec.text == text {
			return true
		}
	}
	return false
}

func (m *Manager) rememberOutboundUserLocalID(localID string) {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return
	}

	now := time.Now()

	m.recentOutboundUserLocalIDsMu.Lock()
	defer m.recentOutboundUserLocalIDsMu.Unlock()

	const maxItems = 128
	const ttl = 30 * time.Second

	cutoff := now.Add(-ttl)
	dst := m.recentOutboundUserLocalIDs[:0]
	for _, rec := range m.recentOutboundUserLocalIDs {
		if rec.at.After(cutoff) {
			dst = append(dst, rec)
		}
	}
	m.recentOutboundUserLocalIDs = dst

	m.recentOutboundUserLocalIDs = append(m.recentOutboundUserLocalIDs, outboundLocalIDRecord{id: localID, at: now})
	if len(m.recentOutboundUserLocalIDs) > maxItems {
		m.recentOutboundUserLocalIDs = m.recentOutboundUserLocalIDs[len(m.recentOutboundUserLocalIDs)-maxItems:]
	}
}

func (m *Manager) isRecentlySentOutboundUserLocalID(localID string) bool {
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return false
	}

	now := time.Now()
	const ttl = 30 * time.Second
	cutoff := now.Add(-ttl)

	m.recentOutboundUserLocalIDsMu.Lock()
	defer m.recentOutboundUserLocalIDsMu.Unlock()

	for i := len(m.recentOutboundUserLocalIDs) - 1; i >= 0; i-- {
		rec := m.recentOutboundUserLocalIDs[i]
		if rec.at.Before(cutoff) {
			break
		}
		if rec.id == localID {
			return true
		}
	}
	return false
}

func (m *Manager) startDesktopTakebackWatcher() {
	m.desktopTakebackMu.Lock()
	if m.desktopTakebackCancel != nil {
		m.desktopTakebackMu.Unlock()
		return
	}
	cancel := make(chan struct{})
	done := make(chan struct{})
	m.desktopTakebackCancel = cancel
	m.desktopTakebackDone = done
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		m.desktopTakebackCancel = nil
		m.desktopTakebackDone = nil
		m.desktopTakebackMu.Unlock()
		return
	}
	m.desktopTakebackTTY = tty
	m.desktopTakebackMu.Unlock()

	fd := int(tty.Fd())
	if !term.IsTerminal(fd) {
		_ = tty.Close()
		m.stopDesktopTakebackWatcher()
		return
	}

	go func() {
		restored := false
		// Always restore terminal state.
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			_ = tty.Close()
			m.desktopTakebackMu.Lock()
			if m.desktopTakebackDone == done {
				m.desktopTakebackCancel = nil
				m.desktopTakebackTTY = nil
				m.desktopTakebackDone = nil
			}
			m.desktopTakebackMu.Unlock()
			close(done)
			return
		}
		m.desktopTakebackMu.Lock()
		if m.desktopTakebackDone == done {
			m.desktopTakebackState = oldState
		}
		m.desktopTakebackMu.Unlock()
		defer func() {
			if !restored {
				_ = term.Restore(fd, oldState)
			}
			_ = tty.Close()
			m.desktopTakebackMu.Lock()
			if m.desktopTakebackDone == done {
				m.desktopTakebackCancel = nil
				m.desktopTakebackTTY = nil
				m.desktopTakebackDone = nil
				m.desktopTakebackState = nil
			}
			m.desktopTakebackMu.Unlock()
			close(done)
		}()

		buf := make([]byte, 16)
		for {
			select {
			case <-m.stopCh:
				return
			case <-cancel:
				return
			default:
			}

			// Poll so we can honor cancel without racing a blocking read.
			pollRes, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 50)
			if err != nil {
				if err == unix.EINTR {
					continue
				}
				return
			}
			if pollRes == 0 {
				continue
			}

			n, err := tty.Read(buf)
			if err != nil {
				return
			}
			if n <= 0 {
				continue
			}

			// Ctrl+L (FF) is our default "take back control" hotkey.
			// Ignore other keys so users don't accidentally steal control while
			// watching remote logs.
			pressed := false
			for _, b := range buf[:n] {
				if b == 0x0c {
					pressed = true
					break
				}
			}
			if !pressed {
				continue
			}

			// Restore the terminal before switching to local mode so the interactive
			// Claude TUI starts with a sane tty state (canonical mode, enter works).
			_ = term.Restore(fd, oldState)
			restored = true
			m.desktopTakebackMu.Lock()
			if m.desktopTakebackState == oldState {
				m.desktopTakebackState = nil
			}
			m.desktopTakebackMu.Unlock()

			// Queue the switch so we don't race other inbound state mutations.
			keys := append([]byte(nil), buf[:n]...)
			_ = m.enqueueInbound(func() {
				if m.GetMode() != ModeRemote {
					return
				}
				if err := m.SwitchToLocal(); err != nil {
					return
				}
				// Best-effort: forward the captured keystrokes into the local PTY so
				// the first key isn't lost during the takeback.
				if m.claudeProcess != nil {
					_ = m.claudeProcess.SendInput(string(keys))
				}
			})
			return
		}
	}()
}

func (m *Manager) stopDesktopTakebackWatcher() {
	m.desktopTakebackMu.Lock()
	cancel := m.desktopTakebackCancel
	done := m.desktopTakebackDone
	tty := m.desktopTakebackTTY
	state := m.desktopTakebackState
	m.desktopTakebackCancel = nil
	m.desktopTakebackTTY = nil
	m.desktopTakebackDone = nil
	m.desktopTakebackState = nil
	m.desktopTakebackMu.Unlock()
	if cancel != nil {
		func() {
			defer func() { recover() }()
			close(cancel)
		}()
	}
	// Restore terminal state synchronously if we have it; this avoids leaving the
	// user's terminal in raw mode if we immediately restart the local Claude TUI.
	if tty != nil && state != nil {
		_ = term.Restore(int(tty.Fd()), state)
	}
	// Don't close /dev/tty here; the watcher goroutine owns restoring terminal state
	// and closing the fd. Closing early can race term.Restore() and leave the user's
	// terminal in a broken state for the local PTY/TUI.
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

// GetMode returns the current operation mode
func (m *Manager) GetMode() Mode {
	m.modeMu.RLock()
	defer m.modeMu.RUnlock()
	return m.mode
}

// SwitchToRemote switches to remote mode (mobile app control)
func (m *Manager) SwitchToRemote() error {
	m.modeMu.Lock()
	alreadyRemote := m.mode == ModeRemote
	bridgeAlreadyRunning := m.remoteBridge != nil
	if alreadyRemote && bridgeAlreadyRunning {
		m.modeMu.Unlock()
		return nil
	}
	m.mode = ModeRemote
	m.modeMu.Unlock()

	log.Println("Switching to remote mode...")

	// Stop local-mode goroutines (stdin piping, thinking/session watchers, etc.)
	m.endLocalRun()

	// Kill the interactive Claude process
	if m.claudeProcess != nil {
		m.claudeProcess.Kill()
		m.claudeProcess = nil
	}

	// Stop session scanner
	if m.sessionScanner != nil {
		m.sessionScanner.Stop()
		m.sessionScanner = nil
	}

	// Start remote bridge
	bridge, err := claude.NewRemoteBridge(m.workDir, m.claudeSessionID, m.debug)
	if err != nil {
		m.modeMu.Lock()
		m.mode = ModeLocal
		m.modeMu.Unlock()
		return fmt.Errorf("failed to create remote bridge: %w", err)
	}

	// Set up message handler
	bridge.SetMessageHandler(m.handleRemoteMessage)

	// Set up permission handler
	bridge.SetPermissionHandler(m.handleRemotePermission)

	if err := bridge.Start(); err != nil {
		m.modeMu.Lock()
		m.mode = ModeLocal
		m.modeMu.Unlock()
		return fmt.Errorf("failed to start remote bridge: %w", err)
	}

	m.remoteBridge = bridge

	// Update state to show we're in remote mode
	m.stateMu.Lock()
	m.state.ControlledByUser = false
	m.stateMu.Unlock()
	go m.updateState()

	writeRemoteBlankLine()
	writeRemoteLine("---")
	writeRemoteLine("Remote mode active (phone controls this session).")
	writeRemoteLine("Press Ctrl+L to take back control on desktop.")
	writeRemoteLine("---")
	writeRemoteBlankLine()

	// While in remote mode, allow the desktop to take control back with a hotkey.
	m.startDesktopTakebackWatcher()

	log.Println("Remote mode active")
	return nil
}

func (m *Manager) handleRemotePermission(requestID string, toolName string, input json.RawMessage) (*claude.PermissionResponse, error) {
	// IMPORTANT: do not run this on the inbound queue.
	//
	// Permission requests block waiting for a mobile response. If we block the inbound
	// queue, we can deadlock because permission responses are delivered via queued
	// inbound RPC handlers.
	if input == nil {
		input = json.RawMessage("null")
	} else {
		// Detach from caller-owned bytes to avoid concurrent mutation.
		input = append(json.RawMessage(nil), input...)
	}
	return m.handlePermissionRequest(requestID, toolName, input)
}

// SwitchToLocal switches to local mode (terminal control)
func (m *Manager) SwitchToLocal() error {
	// Stop the takeback watcher before we start piping stdin into the PTY.
	m.stopDesktopTakebackWatcher()

	m.modeMu.Lock()
	alreadyLocal := m.mode == ModeLocal
	localAlreadyRunning := m.claudeProcess != nil
	if alreadyLocal && localAlreadyRunning {
		m.modeMu.Unlock()
		return nil
	}
	m.mode = ModeLocal
	m.modeMu.Unlock()

	log.Println("Switching to local mode...")

	// Kill the remote bridge
	if m.remoteBridge != nil {
		m.remoteBridge.Kill()
		m.remoteBridge = nil
	}

	// Start Claude process with fd 3 tracking
	claudeProc, err := claude.NewProcess(m.workDir, m.debug)
	if err != nil {
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		return fmt.Errorf("failed to create claude process: %w", err)
	}
	// IMPORTANT: don't publish `m.claudeProcess` until after Start() succeeds.
	//
	// The main session loop concurrently calls `Manager.Wait()`, and if it sees a
	// non-nil process before it's started, it can call `Process.Wait()` and get
	// "process not started", which tears down the session immediately.
	if err := claudeProc.Start(); err != nil {
		m.modeMu.Lock()
		m.mode = ModeRemote
		m.modeMu.Unlock()
		return fmt.Errorf("failed to start claude: %w", err)
	}
	m.claudeProcess = claudeProc

	// Start session ID detection handler
	localCancel := m.beginLocalRun()
	go m.handleSessionIDDetection(localCancel, claudeProc)

	// Start thinking state handler
	go m.handleThinkingState(localCancel, claudeProc)

	// Update state to show we're in local mode
	m.stateMu.Lock()
	m.state.ControlledByUser = true
	m.stateMu.Unlock()
	go m.updateState()

	log.Println("Local mode active")
	return nil
}

// SendUserMessage sends a user message to Claude (remote mode only)
func (m *Manager) SendUserMessage(content string, meta map[string]interface{}) error {
	m.modeMu.RLock()
	mode := m.mode
	bridge := m.remoteBridge
	m.modeMu.RUnlock()

	if mode != ModeRemote {
		return fmt.Errorf("not in remote mode")
	}

	if bridge == nil {
		return fmt.Errorf("remote bridge not running")
	}

	// Remote runner is about to start processing a turn.
	_ = m.enqueueInbound(func() {
		if m.GetMode() != ModeRemote {
			return
		}
		// Show what the user sent (matches the "you>" transcript style).
		userText := normalizeRemoteInputText(content)
		if userText != "" {
			writeRemoteBlankLine()
			for _, l := range wrapWithPrefix(userText, "you> ", remoteTermWidth()) {
				writeRemoteLine(l)
			}
		}
		if !m.thinking {
			m.setThinking(true)
			writeRemoteLine("remote> [thinking]")
		}
	})

	return bridge.SendUserMessage(content, meta)
}

// AbortRemote aborts the current remote query
func (m *Manager) AbortRemote() error {
	m.modeMu.RLock()
	bridge := m.remoteBridge
	m.modeMu.RUnlock()

	if bridge == nil {
		return fmt.Errorf("remote bridge not running")
	}

	return bridge.Abort()
}

// handleRemoteMessage processes messages from the remote bridge
func (m *Manager) handleRemoteMessage(msg *claude.RemoteMessage) error {
	if msg == nil {
		return nil
	}

	// Detach from caller-owned struct/slices to avoid concurrent mutation issues.
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	var copyMsg claude.RemoteMessage
	if err := json.Unmarshal(payload, &copyMsg); err != nil {
		return err
	}

	return m.runInboundErr(func() error { return m.handleRemoteMessageInbound(&copyMsg) })
}

func (m *Manager) handleRemoteMessageInbound(msg *claude.RemoteMessage) error {
	if msg == nil {
		return nil
	}

	if m.debug {
		log.Printf("Remote message: type=%s", msg.Type)
	}

	// Track thinking state from result messages (end of turn).
	if msg.Type == "result" {
		if m.thinking {
			m.setThinking(false)
			writeRemoteLine("remote> [idle]")
		}
	}

	// Track session ID from system init
	if msg.Type == "system" && msg.SessionID != "" {
		m.claudeSessionID = msg.SessionID
	}

	m.renderRemoteMessage(msg)

	// Forward to server
	if m.wsClient != nil && m.wsClient.IsConnected() {
		payload := m.buildRawRecordFromRemote(msg)
		if payload == nil {
			return nil
		}

		data, err := json.Marshal(payload)
		if err != nil {
			if m.debug {
				log.Printf("Failed to marshal remote payload: %v", err)
			}
			return nil
		}

		encrypted, err := m.encrypt(data)
		if err != nil {
			if m.debug {
				log.Printf("Failed to encrypt remote message: %v", err)
			}
			return nil
		}

		m.wsClient.EmitMessage(wire.OutboundMessagePayload{
			SID:     m.sessionID,
			Message: encrypted,
		})
	}

	// Send usage report if present
	if len(msg.Usage) > 0 && m.wsClient != nil && m.wsClient.IsConnected() {
		var usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		}
		if err := json.Unmarshal(msg.Usage, &usage); err == nil {
			total := usage.InputTokens + usage.OutputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
			_ = m.wsClient.EmitRaw("usage-report", wire.UsageReportPayload{
				Key:       "claude-session",
				SessionID: m.sessionID,
				Tokens: wire.UsageReportTokens{
					Total:         total,
					Input:         usage.InputTokens,
					Output:        usage.OutputTokens,
					CacheCreation: usage.CacheCreationInputTokens,
					CacheRead:     usage.CacheReadInputTokens,
				},
				Cost: wire.UsageReportCost{
					Total:  0,
					Input:  0,
					Output: 0,
				},
			})
		}
	}

	return nil
}

func (m *Manager) renderRemoteMessage(msg *claude.RemoteMessage) {
	if m.GetMode() != ModeRemote {
		return
	}

	// Some bridge messages arrive as Claude SDK `message` events (role=assistant/user)
	// instead of already-wrapped raw-record payloads. Convert those to raw records
	// and render them so the desktop sees tool_use and assistant text.
	switch msg.Type {
	case "message", "assistant", "user":
		m.renderRemoteBridgeTranscript(msg)
		return
	}

	switch msg.Type {
	case "system":
		// system/init contains the remote Claude session id.
		if msg.Subtype == "init" && msg.SessionID != "" {
			writeRemoteLine("remote> session=" + msg.SessionID)
		}
	case "control_request":
		// Permission requests should be approved on the phone UI.
		var req claude.PermissionRequest
		if len(msg.Request) == 0 || json.Unmarshal(msg.Request, &req) != nil {
			writeRemoteLine("remote> [permission] request received (approve on phone)")
			return
		}
		writeRemoteLine("remote> [permission] tool=" + req.ToolName + " (approve on phone)")
		if pretty := formatJSONPretty(anyJSON(req.Input)); pretty != "" {
			for _, l := range strings.Split(pretty, "\n") {
				writeRemoteLine("          " + l)
			}
		}
	case "raw":
		if len(msg.Message) == 0 {
			return
		}
		printRawRecord(msg.Message, remoteTermWidth())
	case "result":
		// End of turn. Keep the assistant's textual response under "claude>" from
		// streamed message events; treat "result" as a status line only.
		writeRemoteBlankLine()
		writeRemoteLine("remote> [done]")
	case "aborted":
		writeRemoteLine("remote> [aborted]")
	case "error":
		if msg.Error != "" {
			writeRemoteLine("remote> error: " + msg.Error)
		}
	}
}

func (m *Manager) renderRemoteBridgeTranscript(msg *claude.RemoteMessage) {
	if msg == nil {
		return
	}
	payload := m.buildRawRecordFromRemote(msg)
	if payload == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	printRawRecord(data, remoteTermWidth())
}

func anyJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func printRawRecord(raw json.RawMessage, width int) {
	rec, ok, err := wire.TryParseAgentOutputRecord([]byte(raw))
	if err != nil || !ok || rec == nil {
		return
	}
	msgRole := rec.Content.Data.Message.Role
	blocks := rec.Content.Data.Message.Content
	if msgRole == "" || len(blocks) == 0 {
		return
	}

	prefix := "you> "
	if msgRole == "assistant" {
		prefix = "claude> "
		// Improve readability: keep assistant replies visually separated from status lines.
		writeRemoteBlankLine()
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				for _, l := range wrapWithPrefix(block.Text, prefix, width) {
					writeRemoteLine(l)
				}
			}
		case "tool_use":
			name, _ := block.Fields["name"].(string)
			id, _ := block.Fields["id"].(string)
			line := prefix + "[tool] " + name
			if id != "" {
				line += " " + id
			}
			writeRemoteLine(line)

			// Desktop-only: print full tool input in a readable form.
			if pretty := formatJSONPretty(block.Fields["input"]); pretty != "" {
				for _, l := range strings.Split(pretty, "\n") {
					writeRemoteLine("        " + l)
				}
			}
		case "tool_result":
			writeRemoteLine(prefix + "[tool_result]")
			if pretty := formatJSONPretty(block.Fields["content"]); pretty != "" {
				for _, l := range strings.Split(pretty, "\n") {
					writeRemoteLine("        " + l)
				}
			} else if summary := formatToolResult(block.Fields["content"]); summary != "" {
				for _, l := range wrapWithPrefix(summary, "        ", width) {
					writeRemoteLine(l)
				}
			}
		}
	}
}

func formatToolInput(input any) string {
	if input == nil {
		return ""
	}
	if inputMap, ok := input.(map[string]any); ok {
		if cmd, _ := inputMap["command"].(string); cmd != "" {
			return cmd
		}
		if query, _ := inputMap["query"].(string); query != "" {
			return query
		}
		if url, _ := inputMap["url"].(string); url != "" {
			return url
		}
	}
	blob, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	s := string(blob)
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func formatToolResult(content any) string {
	switch v := content.(type) {
	case string:
		if len(v) > 160 {
			return v[:160] + "..."
		}
		return v
	case []any:
		if len(v) == 0 {
			return ""
		}
		if text, ok := v[0].(map[string]any); ok {
			if t, _ := text["type"].(string); t == "text" {
				if val, _ := text["text"].(string); val != "" {
					if len(val) > 160 {
						return val[:160] + "..."
					}
					return val
				}
			}
		}
	}
	return ""
}

func formatJSONPretty(v any) string {
	if v == nil {
		return ""
	}
	blob, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	// Avoid dumping huge payloads into the terminal.
	const maxBytes = 8 * 1024
	if len(blob) > maxBytes {
		blob = append(blob[:maxBytes], []byte("\n…")...)
	}
	return string(blob)
}

// buildRawRecordFromRemote converts bridge RemoteMessage into the raw record
// format expected by the mobile app (matches RawRecordSchema on client).
func (m *Manager) buildRawRecordFromRemote(msg *claude.RemoteMessage) any {
	type rawRecordProbe struct {
		Role    string `json:"role"`
		Content struct {
			Type string `json:"type"`
		} `json:"content"`
	}

	type legacySDKMessage struct {
		Type    string              `json:"type"`
		Role    string              `json:"role"`
		Model   string              `json:"model,omitempty"`
		Content []wire.ContentBlock `json:"content"`
		Usage   any                 `json:"usage,omitempty"`
		ID      string              `json:"id,omitempty"`
	}

	// If bridge already sent structured payload, prefer that
	if len(msg.Message) > 0 {
		raw := json.RawMessage(msg.Message)

		var legacy legacySDKMessage
		if err := json.Unmarshal(raw, &legacy); err == nil &&
			legacy.Type == "message" &&
			(legacy.Role == "assistant" || legacy.Role == "user") &&
			len(legacy.Content) > 0 {
			model := legacy.Model
			if legacy.Role == "assistant" && model == "" {
				model = "unknown"
			}

			outType := legacy.Role
			uuid := legacy.ID
			if uuid == "" {
				uuid = types.NewCUID()
			}

			return wire.AgentOutputRecord{
				Role: "agent",
				Content: wire.AgentOutputContent{
					Type: "output",
					Data: wire.AgentOutputData{
						Type:             outType,
						IsSidechain:      false,
						IsCompactSummary: false,
						IsMeta:           false,
						UUID:             uuid,
						ParentUUID:       nil,
						Message: wire.AgentMessage{
							Role:    legacy.Role,
							Model:   model,
							Content: legacy.Content,
							Usage:   legacy.Usage,
						},
					},
				},
			}
		}

		var probe rawRecordProbe
		if err := json.Unmarshal(raw, &probe); err == nil &&
			(probe.Role == "agent" || probe.Role == "user") &&
			probe.Content.Type != "" {
			return raw
		}

		if msg.Type == "raw" {
			return nil
		}
	}

	switch msg.Type {
	case "message":
		role := msg.Role
		contentBlocks, err := wire.DecodeContentBlocks(msg.Content)
		if err != nil {
			return nil
		}
		if role == "" || len(contentBlocks) == 0 {
			return nil
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}

		if role == "assistant" {
			message := wire.AgentMessage{
				Role:    "assistant",
				Model:   model,
				Content: contentBlocks,
			}
			if len(msg.Usage) > 0 {
				var usage interface{}
				if err := json.Unmarshal(msg.Usage, &usage); err == nil && usage != nil {
					message.Usage = usage
				}
			}

			data := wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             types.NewCUID(),
				ParentUUID:       nil,
				Message:          message,
			}
			if msg.ParentToolUseID != "" {
				data.ParentToolUseID = msg.ParentToolUseID
			}

			return wire.AgentOutputRecord{
				Role:    "agent",
				Content: wire.AgentOutputContent{Type: "output", Data: data},
			}
		}

		if role == "user" {
			return wire.AgentOutputRecord{
				Role: "agent",
				Content: wire.AgentOutputContent{
					Type: "output",
					Data: wire.AgentOutputData{
						Type:             "user",
						IsSidechain:      false,
						IsCompactSummary: false,
						IsMeta:           false,
						UUID:             types.NewCUID(),
						ParentUUID:       nil,
						Message: wire.AgentMessage{
							Role:    "user",
							Content: contentBlocks,
						},
					},
				},
			}
		}

		return nil
	case "result":
		// Ignore result events to avoid duplicate assistant messages.
		return nil
	case "assistant":
		// Build minimal assistant message with a single text chunk
		uuid := types.NewCUID()

		text := ""
		switch v := msg.Content.(type) {
		case string:
			text = v
		default:
			blocks, err := wire.DecodeContentBlocks(v)
			if err == nil {
				for _, block := range blocks {
					if block.Type == "text" && block.Text != "" {
						text = block.Text
						break
					}
				}
			}
		}
		if text == "" {
			if msg.Result != "" {
				text = msg.Result
			} else {
				return nil
			}
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}

		message := wire.AgentMessage{
			Role:  "assistant",
			Model: model,
			Content: []wire.ContentBlock{
				{Type: "text", Text: text},
			},
		}
		if len(msg.Usage) > 0 {
			var usage interface{}
			if err := json.Unmarshal(msg.Usage, &usage); err == nil && usage != nil {
				message.Usage = usage
			}
		}

		return wire.AgentOutputRecord{
			Role: "agent",
			Content: wire.AgentOutputContent{
				Type: "output",
				Data: wire.AgentOutputData{
					Type:             "assistant",
					IsSidechain:      false,
					IsCompactSummary: false,
					IsMeta:           false,
					UUID:             uuid,
					ParentUUID:       nil,
					Message:          message,
				},
			},
		}
	case "user":
		switch v := msg.Content.(type) {
		case nil:
			return nil
		case string:
			if v == "" {
				return nil
			}
		default:
			if fmt.Sprint(v) == "" {
				return nil
			}
		}
		return wire.UserTextRecord{
			Role: "user",
			Content: struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
				Type: "text",
				Text: fmt.Sprint(msg.Content),
			},
		}
	default:
		// Ignore unsupported types (system, control, etc.)
		return nil
	}
}

func (m *Manager) sendFakeAgentResponse(userText string) {
	if m.wsClient == nil || !m.wsClient.IsConnected() {
		return
	}

	reply := fmt.Sprintf("fake-agent: %s", userText)
	uuid := types.NewCUID()

	payload := wire.AgentOutputRecord{
		Role: "agent",
		Content: wire.AgentOutputContent{
			Type: "output",
			Data: wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             uuid,
				ParentUUID:       nil,
				Message: wire.AgentMessage{
					Role:  "assistant",
					Model: "fake-agent",
					Content: []wire.ContentBlock{
						{Type: "text", Text: reply},
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		if m.debug {
			log.Printf("Fake agent marshal error: %v", err)
		}
		return
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		if m.debug {
			log.Printf("Fake agent encrypt error: %v", err)
		}
		return
	}

	m.wsClient.EmitMessage(wire.OutboundMessagePayload{
		SID:     m.sessionID,
		Message: encrypted,
	})
}
