package codexengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/codex/rollout"
)

// TestApplyConfigDoesNotClearRemoteResumeToken ensures ApplyConfig treats an
// empty model as "engine default" so resume state isn't lost after startup.
func TestApplyConfigDoesNotClearRemoteResumeToken(t *testing.T) {
	engine := New("/tmp", nil, false)
	resume := "thr_123"

	engine.mu.Lock()
	engine.remoteEnabled = true
	engine.remoteSessionActive = true
	engine.remoteResumeToken = resume
	engine.remoteThreadID = resume
	engine.remoteModel = defaultRemoteModel
	engine.remoteReasoningEffort = defaultRemoteReasoningEffort
	engine.mu.Unlock()

	if err := engine.ApplyConfig(context.Background(), agentengine.AgentConfig{
		// Intentionally leave Model empty to represent "engine default".
	}); err != nil {
		t.Fatalf("ApplyConfig returned error: %v", err)
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.remoteSessionActive != true {
		t.Fatalf("expected remote session to remain active after ApplyConfig")
	}
	if engine.remoteResumeToken != resume {
		t.Fatalf("expected resume token to remain %q, got %q", resume, engine.remoteResumeToken)
	}
	if engine.remoteThreadID != resume {
		t.Fatalf("expected thread id to remain %q, got %q", resume, engine.remoteThreadID)
	}
	if engine.remoteModel != defaultRemoteModel {
		t.Fatalf("expected model to remain %q, got %q", defaultRemoteModel, engine.remoteModel)
	}
	if engine.remoteReasoningEffort != defaultRemoteReasoningEffort {
		t.Fatalf("expected effort to remain %q, got %q", defaultRemoteReasoningEffort, engine.remoteReasoningEffort)
	}
}

func TestBuildLocalCodexCommandIncludesSelectedConfig(t *testing.T) {
	cmd := buildLocalCodexCommand("resume-1", agentengine.AgentConfig{
		Model:           "gpt-5.2-codex",
		ReasoningEffort: "high",
		PermissionMode:  "read-only",
	})

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, " -m gpt-5.2-codex ") {
		t.Fatalf("expected -m model in args, got: %s", args)
	}
	if !strings.Contains(args, `model_reasoning_effort="high"`) {
		t.Fatalf("expected effort override in args, got: %s", args)
	}
	if !strings.Contains(args, " -s read-only ") {
		t.Fatalf("expected sandbox flag in args, got: %s", args)
	}
	if !strings.Contains(args, " -a on-request ") {
		t.Fatalf("expected approval policy in args, got: %s", args)
	}
	if !strings.Contains(args, " resume resume-1") {
		t.Fatalf("expected resume subcommand in args, got: %s", args)
	}
}

func TestBuildLocalCodexCommandDefaultsToMediumEffort(t *testing.T) {
	cmd := buildLocalCodexCommand("", agentengine.AgentConfig{})
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, " -m "+defaultRemoteModel+" ") {
		t.Fatalf("expected default model in args, got: %s", args)
	}
	if !strings.Contains(args, `model_reasoning_effort="`+defaultRemoteReasoningEffort+`"`) {
		t.Fatalf("expected default effort in args, got: %s", args)
	}
	if !strings.Contains(args, " -s workspace-write ") {
		t.Fatalf("expected default sandbox in args, got: %s", args)
	}
	if !strings.Contains(args, " -a on-request ") {
		t.Fatalf("expected default approval policy in args, got: %s", args)
	}
}

func TestBuildLocalCodexCommandYoloDisablesApprovals(t *testing.T) {
	cmd := buildLocalCodexCommand("", agentengine.AgentConfig{PermissionMode: "yolo"})
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, " -s danger-full-access ") {
		t.Fatalf("expected yolo sandbox in args, got: %s", args)
	}
	if !strings.Contains(args, " -a never ") {
		t.Fatalf("expected yolo approval policy in args, got: %s", args)
	}
}

func TestHandleRolloutEventEmitsToolUIEvents(t *testing.T) {
	engine := New("/tmp", nil, false)

	engine.handleRolloutEvent(rollout.EvFunctionCall{
		CallID:    "call_1",
		Name:      "shell_command",
		Arguments: `{"command":"ls"}`,
		AtMs:      123,
	})
	ev := readEngineEvent(t, engine, 2*time.Second)
	ui, ok := ev.(agentengine.EvUIEvent)
	if !ok {
		t.Fatalf("expected EvUIEvent, got %T", ev)
	}
	if ui.Mode != agentengine.ModeLocal {
		t.Fatalf("expected local mode, got %q", ui.Mode)
	}
	if ui.Kind != agentengine.UIEventTool {
		t.Fatalf("expected tool UI event, got %q", ui.Kind)
	}
	if ui.Phase != agentengine.UIEventPhaseStart {
		t.Fatalf("expected start phase, got %q", ui.Phase)
	}
	if ui.EventID != "call_1" {
		t.Fatalf("expected event id call_1, got %q", ui.EventID)
	}
	if ui.Status != agentengine.UIEventStatusRunning {
		t.Fatalf("expected running status, got %q", ui.Status)
	}
	if !strings.Contains(ui.BriefMarkdown, "Tool: `shell_command`") {
		t.Fatalf("unexpected brief markdown: %q", ui.BriefMarkdown)
	}
	if !strings.Contains(ui.BriefMarkdown, "\n    ls") {
		t.Fatalf("expected command block in brief markdown: %q", ui.BriefMarkdown)
	}

	engine.handleRolloutEvent(rollout.EvFunctionCallOutput{
		CallID: "call_1",
		Output: "Exit code: 0\nOutput:\nhi",
		AtMs:   124,
	})
	ev = readEngineEvent(t, engine, 2*time.Second)
	ui, ok = ev.(agentengine.EvUIEvent)
	if !ok {
		t.Fatalf("expected EvUIEvent, got %T", ev)
	}
	if ui.Phase != agentengine.UIEventPhaseEnd {
		t.Fatalf("expected end phase, got %q", ui.Phase)
	}
	if ui.Status != agentengine.UIEventStatusOK {
		t.Fatalf("expected ok status, got %q", ui.Status)
	}
	if ui.EventID != "call_1" {
		t.Fatalf("expected event id call_1, got %q", ui.EventID)
	}
	if !strings.Contains(ui.BriefMarkdown, "\n    ls") {
		t.Fatalf("expected command block in brief markdown: %q", ui.BriefMarkdown)
	}
	if !strings.Contains(ui.FullMarkdown, "Output:") {
		t.Fatalf("expected full markdown output, got: %q", ui.FullMarkdown)
	}
}

func TestHandleRolloutEventEmitsReasoningUIEvents(t *testing.T) {
	engine := New("/tmp", nil, false)
	engine.handleRolloutEvent(rollout.EvReasoningSummary{
		Text: "**Plan**\n\nDo the thing.",
		AtMs: 123,
	})
	ev := readEngineEvent(t, engine, 2*time.Second)
	ui, ok := ev.(agentengine.EvUIEvent)
	if !ok {
		t.Fatalf("expected EvUIEvent, got %T", ev)
	}
	if ui.Kind != agentengine.UIEventThinking {
		t.Fatalf("expected thinking UI event, got %q", ui.Kind)
	}
	if ui.Phase != agentengine.UIEventPhaseEnd {
		t.Fatalf("expected end phase, got %q", ui.Phase)
	}
	if ui.BriefMarkdown != "**Plan**" {
		t.Fatalf("unexpected brief markdown: %q", ui.BriefMarkdown)
	}
}

func TestDiscoverLatestRolloutPathForSessionIDPrefersMatchingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, ".codex", "sessions", "2026", "01", "09")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	wantSession := "019ba209-ce56-7753-a01e-befe46f1d124"
	otherSession := "019aa185-0cb4-7572-a018-e9aaafc8ceba"

	wantPath := filepath.Join(root, "rollout-2026-01-09T10-15-10-"+wantSession+".jsonl")
	otherPath := filepath.Join(root, "rollout-2026-01-09T10-15-11-"+otherSession+".jsonl")

	if err := os.WriteFile(wantPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(want) returned error: %v", err)
	}
	if err := os.WriteFile(otherPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(other) returned error: %v", err)
	}

	// Even if another rollout is newer, session-specific discovery should ignore
	// non-matching files entirely.
	if err := os.Chtimes(wantPath, time.Unix(10, 0), time.Unix(10, 0)); err != nil {
		t.Fatalf("Chtimes(want) returned error: %v", err)
	}
	if err := os.Chtimes(otherPath, time.Unix(20, 0), time.Unix(20, 0)); err != nil {
		t.Fatalf("Chtimes(other) returned error: %v", err)
	}

	got, err := discoverLatestRolloutPathForSessionIDImpl(wantSession)
	if err != nil {
		t.Fatalf("discoverLatestRolloutPathForSessionIDImpl returned error: %v", err)
	}
	if got != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, got)
	}
}

func TestDiscoverLatestRolloutPathForSessionIDChoosesNewestMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, ".codex", "sessions", "2026", "01", "09")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	session := "019ba209-ce56-7753-a01e-befe46f1d124"
	oldPath := filepath.Join(root, "rollout-2026-01-09T10-15-10-"+session+".jsonl")
	newPath := filepath.Join(root, "rollout-2026-01-09T10-15-11-"+session+".jsonl")

	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old) returned error: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(new) returned error: %v", err)
	}
	if err := os.Chtimes(oldPath, time.Unix(10, 0), time.Unix(10, 0)); err != nil {
		t.Fatalf("Chtimes(old) returned error: %v", err)
	}
	if err := os.Chtimes(newPath, time.Unix(20, 0), time.Unix(20, 0)); err != nil {
		t.Fatalf("Chtimes(new) returned error: %v", err)
	}

	got, err := discoverLatestRolloutPathForSessionIDImpl(session)
	if err != nil {
		t.Fatalf("discoverLatestRolloutPathForSessionIDImpl returned error: %v", err)
	}
	if got != newPath {
		t.Fatalf("expected newest match %q, got %q", newPath, got)
	}
}

func TestWaitForRolloutPathForSessionIDWaitsForMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, ".codex", "sessions", "2026", "01", "09")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	session := "019ba209-ce56-7753-a01e-befe46f1d124"
	path := filepath.Join(root, "rollout-2026-01-09T10-15-10-"+session+".jsonl")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("{}\n"), 0o644)
	}()

	got, err := waitForRolloutPathForSessionID(ctx, session)
	if err != nil {
		t.Fatalf("waitForRolloutPathForSessionID returned error: %v", err)
	}
	if got != path {
		t.Fatalf("expected path %q, got %q", path, got)
	}
}

func readEngineEvent(t *testing.T, engine *Engine, timeout time.Duration) agentengine.Event {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case <-deadline.C:
		t.Fatalf("timed out waiting for engine event")
	case ev := <-engine.Events():
		return ev
	}
	return nil
}
