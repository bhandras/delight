package codexengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
)

// codexExecEvent is a JSONL event emitted by `codex exec --json`.
type codexExecEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     *codexExecItem  `json:"item"`
	Usage    json.RawMessage `json:"usage"`
}

// codexExecItem is an item envelope in the `codex exec --json` stream.
type codexExecItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	Status           string `json:"status"`
}

// runCodexExecTurn runs a single `codex exec` (or `codex exec resume`) turn and
// streams JSON events into agentengine events.
func (e *Engine) runCodexExecTurn(ctx context.Context, spec codexExecTurnSpec) error {
	if e == nil {
		return fmt.Errorf("codex engine is nil")
	}

	cmd, stdout, stderr, err := buildCodexExecCommand(ctx, spec)
	if err != nil {
		return err
	}

	e.setRemoteThinking(true, time.Now().UnixMilli())
	defer e.setRemoteThinking(false, time.Now().UnixMilli())

	if e.debug {
		logger.Debugf("codex exec: %s", strings.Join(cmd.Args, " "))
	}

	e.mu.Lock()
	e.remoteExecCmd = cmd
	e.remoteExecDone = make(chan error, 1)
	done := e.remoteExecDone
	e.mu.Unlock()

	if err := cmd.Start(); err != nil {
		e.mu.Lock()
		if e.remoteExecCmd == cmd {
			e.remoteExecCmd = nil
			e.remoteExecDone = nil
		}
		e.mu.Unlock()
		return err
	}

	go func() { done <- cmd.Wait() }()

	// Read both streams; JSON events are on stdout, but we still want to drain
	// stderr so the child cannot block on a full pipe.
	stdoutCh := make(chan error, 1)
	stderrCh := make(chan struct{})
	go func() {
		stdoutCh <- e.consumeCodexExecJSON(ctx, stdout)
	}()
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
		close(stderrCh)
	}()

	var stdoutErr error
	select {
	case <-ctx.Done():
		// Best-effort: kill the child process group so the runtime doesn't wedge.
		e.stopRemoteExec(cmd)
		stdoutErr = ctx.Err()
	case stdoutErr = <-stdoutCh:
		// Wait for the process exit below.
	}

	waitErr := <-done
	<-stderrCh

	e.mu.Lock()
	if e.remoteExecCmd == cmd {
		e.remoteExecCmd = nil
		e.remoteExecDone = nil
	}
	e.mu.Unlock()

	if stdoutErr != nil {
		return stdoutErr
	}
	// Exit errors are surfaced as assistant-visible failures rather than hard
	// stopping the engine: the runtime can remain responsive and the user can
	// retry.
	if waitErr != nil {
		e.emitRemoteAssistantError(fmt.Sprintf("Codex exec failed: %v", waitErr))
		return nil
	}
	return nil
}

// consumeCodexExecJSON reads JSONL events from stdout and emits engine events.
func (e *Engine) consumeCodexExecJSON(ctx context.Context, r io.Reader) error {
	reader := bufio.NewReader(r)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			// `codex exec --json` is a short-lived process. When it exits (or is
			// aborted), the stdout pipe can close while we're blocked in ReadBytes.
			// Treat this as a normal end-of-stream so remote mode doesn't surface
			// spurious "file already closed" errors to the user.
			if errors.Is(err, io.EOF) || isPipeClosedError(err) {
				return nil
			}
			return err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var ev codexExecEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		e.handleCodexExecEvent(ev)
	}
}

func isPipeClosedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "use of closed file")
}

// handleCodexExecEvent translates a single codex exec JSON event into common
// agentengine events.
func (e *Engine) handleCodexExecEvent(ev codexExecEvent) {
	switch ev.Type {
	case "thread.started":
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			e.mu.Lock()
			e.remoteResumeToken = threadID
			e.remoteSessionActive = true
			e.mu.Unlock()
			e.tryEmit(agentengine.EvSessionIdentified{Mode: agentengine.ModeRemote, ResumeToken: threadID})
		}

	case "turn.started":
		e.startRemoteTurn()

	case "item.started":
		if ev.Item == nil {
			return
		}
		e.emitCodexExecItem(*ev.Item, agentengine.UIEventPhaseStart)

	case "item.completed":
		if ev.Item == nil {
			return
		}
		e.emitCodexExecItem(*ev.Item, agentengine.UIEventPhaseEnd)
		if ev.Item.Type == "agent_message" && strings.TrimSpace(ev.Item.Text) != "" {
			e.emitRemoteAssistantText(strings.TrimSpace(ev.Item.Text))
		}
	}
}

// emitCodexExecItem emits an EvUIEvent for the provided exec item.
func (e *Engine) emitCodexExecItem(item codexExecItem, phase agentengine.UIEventPhase) {
	eventID := strings.TrimSpace(item.ID)
	if eventID == "" {
		eventID = types.NewCUID()
	}
	atMs := time.Now().UnixMilli()

	switch item.Type {
	case "reasoning":
		text := strings.TrimSpace(item.Text)
		if text == "" {
			return
		}
		e.tryEmit(agentengine.EvUIEvent{
			Mode:          agentengine.ModeRemote,
			EventID:       eventID,
			Kind:          agentengine.UIEventThinking,
			Phase:         phase,
			Status:        agentengine.UIEventStatusOK,
			BriefMarkdown: firstMarkdownLine(text),
			FullMarkdown:  text,
			AtMs:          atMs,
		})

	case "command_execution":
		command := strings.TrimSpace(item.Command)
		output := strings.TrimSpace(item.AggregatedOutput)
		status := agentengine.UIEventStatusRunning
		if phase == agentengine.UIEventPhaseEnd {
			if item.ExitCode != nil && *item.ExitCode == 0 {
				status = agentengine.UIEventStatusOK
			} else if item.ExitCode != nil {
				status = agentengine.UIEventStatusError
			} else {
				status = agentengine.UIEventStatusOK
			}
		}

		full := fmt.Sprintf("Tool: shell\n\n`%s`", command)
		if output != "" {
			full = fmt.Sprintf("%s\n\nOutput:\n\n```\n%s\n```", full, output)
		}
		e.tryEmit(agentengine.EvUIEvent{
			Mode:          agentengine.ModeRemote,
			EventID:       eventID,
			Kind:          agentengine.UIEventTool,
			Phase:         phase,
			Status:        status,
			BriefMarkdown: fmt.Sprintf("Tool: `%s`", truncateOneLine(command, 64)),
			FullMarkdown:  full,
			AtMs:          atMs,
		})

	default:
		// Ignore other item types for now (file_change etc.) because agent_message
		// already reflects applied changes and the phone transcript stays readable.
	}
}

// buildCodexExecCommand builds a `codex exec --json` command for a single turn.
func buildCodexExecCommand(ctx context.Context, spec codexExecTurnSpec) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	args := []string{}

	sandbox := strings.TrimSpace(spec.Sandbox)
	if sandbox != "" {
		args = append(args, "-s", sandbox)
	}
	// Remote mode must be non-interactive. Codex exec does not provide a stable,
	// machine-readable approval callback channel (unlike MCP), so we rely on
	// sandboxing for safety.
	args = append(args, "-a", "never")

	if model := strings.TrimSpace(spec.Model); model != "" {
		args = append(args, "-m", model)
	}
	if effort := strings.TrimSpace(spec.ReasoningEffort); effort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", effort))
	}
	if cwd := strings.TrimSpace(spec.WorkDir); cwd != "" {
		args = append(args, "-C", cwd)
	}

	args = append(args, "exec")

	prompt := strings.TrimSpace(spec.Prompt)
	if prompt == "" {
		return nil, nil, nil, fmt.Errorf("prompt is required")
	}

	resumeToken := strings.TrimSpace(spec.ResumeToken)
	if resumeToken != "" {
		args = append(args, "resume", resumeToken, prompt, "--json")
	} else {
		args = append(args, prompt, "--json")
	}

	cmd := exec.CommandContext(ctx, codexBinary, args...)
	if runtime.GOOS != "windows" {
		configureLocalCmdProcessGroup(cmd)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	return cmd, stdout, stderr, nil
}

// codexExecTurnSpec contains the inputs needed to run a single exec turn.
type codexExecTurnSpec struct {
	WorkDir         string
	Prompt          string
	Model           string
	ReasoningEffort string
	Sandbox         string
	ResumeToken     string
}

// stopRemoteExec best-effort kills a codex exec process tree.
func (e *Engine) stopRemoteExec(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
		return
	}
	stopLocalCmd(cmd)
}

// emitRemoteAssistantText emits an assistant message record for remote mode.
func (e *Engine) emitRemoteAssistantText(text string) {
	raw, err := marshalAssistantTextRecord(text, "unknown")
	if err != nil {
		return
	}
	e.tryEmit(agentengine.EvOutboundRecord{
		Mode:    agentengine.ModeRemote,
		LocalID: types.NewCUID(),
		Payload: raw,
		AtMs:    time.Now().UnixMilli(),
	})
}

// firstMarkdownLine returns a short summary line for markdown content.
func firstMarkdownLine(md string) string {
	md = strings.TrimSpace(md)
	if md == "" {
		return ""
	}
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line
	}
	return ""
}

// truncateOneLine returns a single-line string truncated to maxRunes.
func truncateOneLine(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes-1]) + "…"
}
