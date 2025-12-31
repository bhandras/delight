package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
)

const (
	// remoteBannerLine1 is printed when remote mode becomes active.
	remoteBannerLine1 = "Remote mode active (phone controls Claude)."
	// remoteBannerLine2 is printed as the takeback UX hint.
	remoteBannerLine2 = "Tip: press space twice to take back control on desktop."
	// localBannerLine is printed when local mode becomes active while the local TUI isn't running.
	localBannerLine = "Local mode active (desktop controls Claude)."

	// remoteUnknownModel is used when the remote runner doesn't provide a model name.
	remoteUnknownModel = "unknown"

	// claudePermissionAllow indicates an approval response to a permission request.
	claudePermissionAllow = "allow"
	// claudePermissionDeny indicates a rejection response to a permission request.
	claudePermissionDeny = "deny"
)

// startRemote starts the remote bridge process and wires its message handlers.
func (r *Runtime) startRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	workDir := eff.WorkDir
	if workDir == "" {
		workDir = r.workDir
	}
	bridge, err := claude.NewRemoteBridge(workDir, eff.Resume, r.debug)
	if err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	bridge.SetMessageHandler(func(msg *claude.RemoteMessage) error {
		if msg == nil {
			return nil
		}
		// Detach from bridge-owned buffers.
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		var copyMsg claude.RemoteMessage
		if err := json.Unmarshal(raw, &copyMsg); err != nil {
			return err
		}

		nowMs := time.Now().UnixMilli()

		if copyMsg.Type == "control_request" {
			var req claude.PermissionRequest
			if len(copyMsg.Request) > 0 && json.Unmarshal(copyMsg.Request, &req) == nil && req.ToolName != "" {
				emit(evPermissionRequested{
					RequestID: copyMsg.RequestID,
					ToolName:  req.ToolName,
					Input:     req.Input,
					NowMs:     nowMs,
				})
			} else {
				emit(evPermissionRequested{
					RequestID: copyMsg.RequestID,
					ToolName:  "unknown",
					Input:     json.RawMessage("null"),
					NowMs:     nowMs,
				})
			}
			return nil
		}

		plaintext, ok := buildRawRecordBytesFromRemote(&copyMsg)
		if !ok {
			return nil
		}

		// Best-effort desktop UI while in remote mode: print assistant output to stdout
		// so the local terminal reflects what is happening even though the interactive
		// Claude TUI is not running.
		r.printRemoteOutputIfApplicable(plaintext)

		r.mu.Lock()
		encryptFn := r.encryptFn
		r.mu.Unlock()
		if encryptFn == nil {
			return nil
		}
		ciphertext, err := encryptFn(plaintext)
		if err != nil {
			return nil
		}
		emit(evOutboundMessageReady{
			Gen:        eff.Gen,
			LocalID:    "",
			Ciphertext: ciphertext,
			NowMs:      nowMs,
		})
		return nil
	})

	if err := bridge.Start(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	r.printRemoteBanner()

	r.mu.Lock()
	if r.remoteBridge != nil {
		r.remoteBridge.Kill()
	}
	r.remoteBridge = bridge
	r.remoteGen = eff.Gen
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeRemote})

	go func(gen int64, b *claude.RemoteBridge) {
		err := b.Wait()
		emit(evRunnerExited{Gen: gen, Mode: ModeRemote, Err: err})
	}(eff.Gen, bridge)
}

// stopRemote kills the remote bridge for the matching generation.
func (r *Runtime) stopRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	if r.remoteBridge == nil {
		r.mu.Unlock()
		return
	}
	if r.remoteGen != 0 && eff.Gen != 0 && eff.Gen != r.remoteGen {
		r.mu.Unlock()
		return
	}
	r.remoteBridge.Kill()
	r.remoteBridge = nil
	r.remoteGen = 0
	r.mu.Unlock()
	r.printLocalBanner()
}

// remoteSend sends a user message to the remote bridge and echoes it to stdout
// for better desktop parity.
func (r *Runtime) remoteSend(eff effRemoteSend) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	// The Node bridge does not emit a raw-record "user" event for inputs we send
	// into Claude via stdin. To keep parity with the mobile transcript and make
	// remote mode readable on desktop, echo the user message here.
	r.printRemoteUserInputIfApplicable(eff.Text)

	_ = bridge.SendUserMessage(eff.Text, eff.Meta)
}

// remoteAbort aborts the current remote run.
func (r *Runtime) remoteAbort(eff effRemoteAbort) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	_ = bridge.Abort()
}

// remotePermissionDecision forwards a permission decision to the remote bridge.
func (r *Runtime) remotePermissionDecision(eff effRemotePermissionDecision) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	behavior := claudePermissionDeny
	if eff.Allow {
		behavior = claudePermissionAllow
	}
	resp := &claude.PermissionResponse{
		Behavior: behavior,
		Message:  eff.Message,
	}
	if eff.Allow && len(eff.UpdatedInput) > 0 {
		resp.UpdatedInput = eff.UpdatedInput
	}
	_ = bridge.SendPermissionResponse(eff.RequestID, resp)
}

// printRemoteBanner prints a banner for remote mode if the local TUI is not active.
func (r *Runtime) printRemoteBanner() {
	r.mu.Lock()
	localActive := r.localProc != nil
	r.mu.Unlock()
	if localActive {
		return
	}
	fmt.Fprintln(os.Stdout, remoteBannerLine1)
	fmt.Fprintln(os.Stdout, remoteBannerLine2)
}

// printLocalBanner prints a banner for local mode if the local TUI is not active.
func (r *Runtime) printLocalBanner() {
	// Only print if the interactive TUI isn't active; otherwise we can corrupt
	// the user's terminal.
	r.mu.Lock()
	localActive := r.localProc != nil
	r.mu.Unlock()
	if localActive {
		return
	}
	fmt.Fprintln(os.Stdout, localBannerLine)
}

// printRemoteOutputIfApplicable renders remote runner output in a compact
// [user]/[agent]/[tool] style when the interactive local TUI is not active.
func (r *Runtime) printRemoteOutputIfApplicable(plaintext []byte) {
	if len(plaintext) == 0 {
		return
	}

	r.mu.Lock()
	localActive := r.localProc != nil
	r.mu.Unlock()
	if localActive {
		return
	}

	rec, ok, err := wire.TryParseAgentOutputRecord(plaintext)
	if err != nil || !ok || rec == nil {
		return
	}
	role := rec.Content.Data.Message.Role
	blocks := rec.Content.Data.Message.Content

	// printSection prints a heading and a body, skipping empty bodies.
	printSection := func(header string, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, header)
		fmt.Fprintln(os.Stdout, body)
		fmt.Fprintln(os.Stdout, "")
	}

	// printJSONSection pretty-prints a JSON-ish payload for tool logs.
	printJSONSection := func(header string, payload any) {
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, header)
		if payload == nil {
			fmt.Fprintln(os.Stdout, "{}")
			fmt.Fprintln(os.Stdout, "")
			return
		}
		pretty, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stdout, fmt.Sprintf("%v", payload))
			fmt.Fprintln(os.Stdout, "")
			return
		}
		fmt.Fprintln(os.Stdout, strings.TrimSpace(string(pretty)))
		fmt.Fprintln(os.Stdout, "")
	}

	// extractTextBlocks concatenates "text" blocks to mirror the mobile view.
	extractTextBlocks := func(blocks []wire.ContentBlock) string {
		var parts []string
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, strings.TrimSpace(block.Text))
			}
		}
		return strings.Join(parts, "\n\n")
	}

	switch role {
	case "user":
		text := extractTextBlocks(blocks)
		if strings.TrimSpace(text) == "" {
			return
		}
		printSection("[user]", text)
	case "assistant":
		// Thinking blocks (if present).
		for _, block := range blocks {
			if block.Type != "thinking" && block.Type != "reasoning" {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text == "" {
				if v, ok := block.Fields["text"].(string); ok {
					text = strings.TrimSpace(v)
				}
			}
			if text != "" {
				printSection("[thinking]", text)
			}
		}

		// Tool blocks.
		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				name, _ := block.Fields["name"].(string)
				id, _ := block.Fields["id"].(string)
				input := block.Fields["input"]
				header := "[tool]"
				if name != "" && id != "" {
					header = fmt.Sprintf("[tool] %s (%s)", name, id)
				} else if name != "" {
					header = fmt.Sprintf("[tool] %s", name)
				} else if id != "" {
					header = fmt.Sprintf("[tool] (%s)", id)
				}
				printJSONSection(header, input)
			case "tool_result":
				toolUseID, _ := block.Fields["tool_use_id"].(string)
				content := block.Fields["content"]
				header := "[tool-result]"
				if toolUseID != "" {
					header = fmt.Sprintf("[tool-result] (%s)", toolUseID)
				}
				printJSONSection(header, content)
			}
		}

		// Assistant reply text blocks.
		text := extractTextBlocks(blocks)
		if strings.TrimSpace(text) != "" {
			printSection("[agent]", text)
		}
	default:
		return
	}
}

// printRemoteUserInputIfApplicable echoes a user input line in remote mode when
// the local TUI is not active.
func (r *Runtime) printRemoteUserInputIfApplicable(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	r.mu.Lock()
	localActive := r.localProc != nil
	r.mu.Unlock()
	if localActive {
		return
	}

	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "[user]")
	fmt.Fprintln(os.Stdout, text)
	fmt.Fprintln(os.Stdout, "")
}

// buildRawRecordBytesFromRemote maps remote bridge messages into wire records.
func buildRawRecordBytesFromRemote(msg *claude.RemoteMessage) ([]byte, bool) {
	if msg == nil {
		return nil, false
	}

	// Prefer already-structured payloads.
	if len(msg.Message) > 0 && msg.Type != "raw" {
		raw := json.RawMessage(msg.Message)
		// If it already looks like a wire record, forward as-is.
		var probe struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && probe.Role != "" && probe.Content.Type != "" {
			return []byte(raw), true
		}
	}

	switch msg.Type {
	case "raw":
		if len(msg.Message) == 0 {
			return nil, false
		}
		return []byte(msg.Message), true
	case "message":
		role := msg.Role
		contentBlocks, err := wire.DecodeContentBlocks(msg.Content)
		if err != nil {
			return nil, false
		}
		if role == "" || len(contentBlocks) == 0 {
			return nil, false
		}

		model := msg.Model
		if model == "" {
			model = remoteUnknownModel
		}
		outType := role
		uuid := types.NewCUID()

		rec := wire.AgentOutputRecord{
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
						Role:    role,
						Model:   model,
						Content: contentBlocks,
					},
				},
			},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, false
		}
		return data, true
	case "assistant":
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
		if text == "" && msg.Result != "" {
			text = msg.Result
		}
		if text == "" {
			return nil, false
		}
		model := msg.Model
		if model == "" {
			model = remoteUnknownModel
		}
		rec := wire.AgentOutputRecord{
			Role: "agent",
			Content: wire.AgentOutputContent{
				Type: "output",
				Data: wire.AgentOutputData{
					Type:             "assistant",
					IsSidechain:      false,
					IsCompactSummary: false,
					IsMeta:           false,
					UUID:             types.NewCUID(),
					ParentUUID:       nil,
					Message: wire.AgentMessage{
						Role:  "assistant",
						Model: model,
						Content: []wire.ContentBlock{
							{Type: "text", Text: text},
						},
					},
				},
			},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, false
		}
		return data, true
	default:
		return nil, false
	}
}

