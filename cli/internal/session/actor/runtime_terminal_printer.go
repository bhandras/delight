package actor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/shared/wire"
	"golang.org/x/term"
)

const (
	remoteBannerLine1 = "Remote mode active (phone controls the session)."
	remoteBannerLine2 = "Tip: press space twice to take back control on desktop."
	localBannerLine   = "Local mode active (desktop controls the session)."
)

const (
	// crlf is used for terminal output because some terminals treat LF as
	// "move down but keep column" unless preceded by CR.
	crlf = "\r\n"
)

const (
	ansiClearScreen = "\x1b[2J"
	ansiCursorHome  = "\x1b[H"
	ansiReset       = "\x1b[0m"
)

func writeLine(s string) {
	// Always reset to column 0 before printing.
	_, _ = os.Stdout.WriteString("\r" + s + crlf)
}

func writeBlankLine() {
	_, _ = os.Stdout.WriteString(crlf)
}

func writeLines(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	for _, line := range strings.Split(s, "\n") {
		writeLine(line)
	}
}

func (r *Runtime) clearScreenIfApplicable() {
	if r == nil {
		return
	}
	// Never attempt terminal control sequences if stdout isn't a TTY.
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}

	// Never clear while the local interactive TUI is active; it owns the screen.
	r.mu.Lock()
	localActive := r.engineLocalInteractive
	r.mu.Unlock()
	if localActive {
		return
	}

	_, _ = os.Stdout.WriteString("\r" + ansiReset + ansiClearScreen + ansiCursorHome)
}

func (r *Runtime) printRemoteBannerIfApplicable() {
	r.mu.Lock()
	localActive := r.engineLocalInteractive
	r.mu.Unlock()
	if localActive {
		return
	}
	writeLine(remoteBannerLine1)
	writeLine(remoteBannerLine2)
}

func (r *Runtime) printLocalBannerIfApplicable() {
	r.mu.Lock()
	localActive := r.engineLocalInteractive
	r.mu.Unlock()
	if localActive {
		return
	}
	writeLine(localBannerLine)
}

func (r *Runtime) printRemoteUserInputIfApplicable(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	r.mu.Lock()
	localActive := r.engineLocalInteractive
	r.mu.Unlock()
	if localActive {
		return
	}

	writeBlankLine()
	writeLine("[user]")
	writeLines(text)
	writeBlankLine()
}

func (r *Runtime) printRemoteRecordIfApplicable(plaintext []byte) {
	if len(plaintext) == 0 {
		return
	}

	r.mu.Lock()
	localActive := r.engineLocalInteractive
	r.mu.Unlock()
	if localActive {
		return
	}

	// 1) Output record (Claude-like content blocks).
	if rec, ok, err := wire.TryParseAgentOutputRecord(plaintext); err == nil && ok && rec != nil {
		r.printAgentOutputRecord(rec)
		return
	}

	// 2) User text record (used by some engines).
	var userText wire.UserTextRecord
	if err := json.Unmarshal(plaintext, &userText); err == nil {
		if userText.Role == "user" && userText.Content.Type == "text" && strings.TrimSpace(userText.Content.Text) != "" {
			r.printSection("[user]", userText.Content.Text)
			return
		}
	}
}

func (r *Runtime) printRemoteUIEventIfApplicable(ev agentengine.EvUIEvent, atMs int64) {
	if r == nil {
		return
	}

	r.mu.Lock()
	localActive := r.engineLocalInteractive
	r.mu.Unlock()
	if localActive {
		return
	}

	body := strings.TrimSpace(ev.BriefMarkdown)
	if body == "" {
		body = strings.TrimSpace(ev.FullMarkdown)
	}
	if body == "" {
		return
	}

	header := "[event]"
	switch ev.Kind {
	case agentengine.UIEventThinking:
		header = "[thinking]"
	case agentengine.UIEventTool:
		header = "[tool]"
	}
	r.printSection(header, body)
}

func (r *Runtime) printAgentOutputRecord(rec *wire.AgentOutputRecord) {
	if rec == nil {
		return
	}
	role := rec.Content.Data.Message.Role
	blocks := rec.Content.Data.Message.Content

	switch role {
	case "user":
		text := extractTextBlocks(blocks)
		if strings.TrimSpace(text) == "" {
			return
		}
		r.printSection("[user]", text)
	case "assistant":
		// Assistant reply text blocks.
		text := extractTextBlocks(blocks)
		if strings.TrimSpace(text) != "" {
			r.printSection("[agent]", text)
		}
	default:
		return
	}
}

func (r *Runtime) printSection(header string, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	writeBlankLine()
	writeLine(header)
	writeLines(body)
	writeBlankLine()
}

func (r *Runtime) printJSONSection(header string, payload any) {
	writeBlankLine()
	writeLine(header)
	if payload == nil {
		writeLine("{}")
		writeBlankLine()
		return
	}
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		// Fall back to printing the raw payload.
		writeLines(strings.TrimSpace(fmt.Sprintf("%v", payload)))
		writeBlankLine()
		return
	}
	writeLines(strings.TrimSpace(string(pretty)))
	writeBlankLine()
}

func extractTextBlocks(blocks []wire.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}
