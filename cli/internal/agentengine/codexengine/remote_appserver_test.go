package codexengine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
)

// TestSandboxPolicyObjectUsesCamelCaseModes ensures the remote app-server
// sandboxPolicy payload uses the camelCase discriminants expected by codex-cli
// releases (e.g. 0.80.0).
func TestSandboxPolicyObjectUsesCamelCaseModes(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		policy := sandboxPolicyObject("read-only", "/tmp")
		if got := policy["type"]; got != "readOnly" {
			t.Fatalf("expected type readOnly, got %#v", got)
		}
	})

	t.Run("yolo", func(t *testing.T) {
		policy := sandboxPolicyObject("yolo", "/tmp")
		if got := policy["type"]; got != "dangerFullAccess" {
			t.Fatalf("expected type dangerFullAccess, got %#v", got)
		}
	})

	t.Run("default", func(t *testing.T) {
		workDir := "/work"
		policy := sandboxPolicyObject("default", workDir)
		if got := policy["type"]; got != "workspaceWrite" {
			t.Fatalf("expected type workspaceWrite, got %#v", got)
		}

		roots, ok := policy["writableRoots"].([]string)
		if !ok {
			t.Fatalf("expected writableRoots []string, got %T", policy["writableRoots"])
		}
		if len(roots) != 1 || roots[0] != workDir {
			t.Fatalf("expected writableRoots [%q], got %#v", workDir, roots)
		}
		if got := policy["networkAccess"]; got != true {
			t.Fatalf("expected networkAccess true, got %#v", got)
		}
	})
}

// TestSandboxPolicyObjectKebabCaseModes ensures we can build a kebab-case
// sandboxPolicy payload for older/newer app-server schema variants.
func TestSandboxPolicyObjectKebabCaseModes(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		policy := sandboxPolicyObjectKebabCase("read-only", "/tmp")
		if got := policy["type"]; got != "read-only" {
			t.Fatalf("expected type read-only, got %#v", got)
		}
	})

	t.Run("yolo", func(t *testing.T) {
		policy := sandboxPolicyObjectKebabCase("yolo", "/tmp")
		if got := policy["type"]; got != "danger-full-access" {
			t.Fatalf("expected type danger-full-access, got %#v", got)
		}
	})

	t.Run("default", func(t *testing.T) {
		workDir := "/work"
		policy := sandboxPolicyObjectKebabCase("default", workDir)
		if got := policy["type"]; got != "workspace-write" {
			t.Fatalf("expected type workspace-write, got %#v", got)
		}
	})
}

// TestRemoteAgentMessageDeltaWithoutItemStarted ensures we still surface assistant
// output even if an agentMessage delta arrives before item/started (or if
// item/started was dropped).
func TestRemoteAgentMessageDeltaWithoutItemStarted(t *testing.T) {
	engine := New("/tmp", nil, false)

	deltaParams, err := json.Marshal(map[string]any{
		"itemId": "it_1",
		"delta":  "hello",
	})
	if err != nil {
		t.Fatalf("Marshal delta params: %v", err)
	}
	engine.handleAgentMessageDelta(deltaParams)

	engine.mu.Lock()
	workingAfterDelta := engine.remoteWorking
	engine.mu.Unlock()
	if workingAfterDelta {
		t.Fatalf("expected remoteWorking=false without turn/started")
	}

	itemCompletedParams, err := json.Marshal(map[string]any{
		"item": map[string]any{
			"id":   "it_1",
			"type": "agentMessage",
		},
	})
	if err != nil {
		t.Fatalf("Marshal item/completed params: %v", err)
	}
	engine.handleItemCompleted(itemCompletedParams)

	engine.mu.Lock()
	workingAfterCompleted := engine.remoteWorking
	engine.mu.Unlock()
	if workingAfterCompleted {
		t.Fatalf("expected remoteWorking=false without turn/completed")
	}

	var out agentengine.EvOutboundRecord
	foundOutbound := false
	deadline := time.Now().Add(3 * time.Second)
	for !foundOutbound && time.Now().Before(deadline) {
		ev := readEngineEvent(t, engine, 3*time.Second)
		if ev == nil {
			continue
		}
		if record, ok := ev.(agentengine.EvOutboundRecord); ok {
			out = record
			foundOutbound = true
			break
		}
	}
	if !foundOutbound {
		t.Fatalf("expected EvOutboundRecord after agentMessage completion")
	}
	if out.Mode != agentengine.ModeRemote {
		t.Fatalf("expected remote mode, got %q", out.Mode)
	}
	if len(out.Payload) == 0 {
		t.Fatalf("expected non-empty payload")
	}
}

func TestTurnCompletionClearsThinkingImmediately(t *testing.T) {
	engine := New("/tmp", nil, false)

	turnStartedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"id": "turn_1"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/started params: %v", err)
	}
	engine.handleTurnStarted(turnStartedParams)

	turnCompletedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "ok"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/completed params: %v", err)
	}
	engine.handleTurnCompleted(turnCompletedParams)

	engine.mu.Lock()
	workingAfterTurnCompleted := engine.remoteWorking
	activeAfterTurnCompleted := engine.remoteActiveTurnID
	engine.mu.Unlock()
	if workingAfterTurnCompleted {
		t.Fatalf("expected remoteWorking=false after turn/completed")
	}
	if strings.TrimSpace(activeAfterTurnCompleted) != "" {
		t.Fatalf("expected remoteActiveTurnID cleared after turn/completed, got %q", activeAfterTurnCompleted)
	}
}

func TestTurnCompletionClearsThinkingEvenWithInFlightItems(t *testing.T) {
	engine := New("/tmp", nil, false)

	turnStartedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"id": "turn_1"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/started params: %v", err)
	}
	engine.handleTurnStarted(turnStartedParams)

	itemStartedParams, err := json.Marshal(map[string]any{
		"item": map[string]any{
			"id":      "it_1",
			"type":    "commandExecution",
			"command": "echo hi",
		},
	})
	if err != nil {
		t.Fatalf("Marshal item/started params: %v", err)
	}
	engine.handleItemStarted(itemStartedParams)

	turnCompletedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"id": "turn_1", "status": "ok"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/completed params: %v", err)
	}
	engine.handleTurnCompleted(turnCompletedParams)

	engine.mu.Lock()
	workingAfterTurnCompleted := engine.remoteWorking
	activeTurnID := strings.TrimSpace(engine.remoteActiveTurnID)
	engine.mu.Unlock()
	if workingAfterTurnCompleted {
		t.Fatalf("expected remoteWorking=false after turn/completed")
	}
	if activeTurnID != "" {
		t.Fatalf("expected remoteActiveTurnID cleared after turn/completed, got %q", activeTurnID)
	}
}

func TestTurnCompletedWithoutIDDoesNotClearBusy(t *testing.T) {
	engine := New("/tmp", nil, false)

	turnStartedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"id": "turn_1"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/started params: %v", err)
	}
	engine.handleTurnStarted(turnStartedParams)

	engine.mu.Lock()
	if !engine.remoteWorking {
		engine.mu.Unlock()
		t.Fatalf("expected remoteWorking=true after turn start")
	}
	engine.mu.Unlock()

	// This simulates a schema mismatch where we receive a turn/completed
	// notification without a parsable turn id.
	turnCompletedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"status": "ok"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/completed params: %v", err)
	}
	engine.handleTurnCompleted(turnCompletedParams)

	engine.mu.Lock()
	workingAfter := engine.remoteWorking
	activeAfter := engine.remoteActiveTurnID
	engine.mu.Unlock()
	if !workingAfter {
		t.Fatalf("expected remoteWorking to remain true after malformed turn/completed")
	}
	if strings.TrimSpace(activeAfter) == "" {
		t.Fatalf("expected remoteActiveTurnID retained after malformed turn/completed")
	}
}

func TestTurnIDParseAcceptsFlatTurnID(t *testing.T) {
	engine := New("/tmp", nil, false)

	turnStartedParams, err := json.Marshal(map[string]any{
		"turnId": "turn_1",
	})
	if err != nil {
		t.Fatalf("Marshal turn/started params: %v", err)
	}
	engine.handleTurnStarted(turnStartedParams)

	engine.mu.Lock()
	working := engine.remoteWorking
	active := engine.remoteActiveTurnID
	engine.mu.Unlock()
	if !working {
		t.Fatalf("expected remoteWorking=true after turn start")
	}
	if strings.TrimSpace(active) != "turn_1" {
		t.Fatalf("expected remoteActiveTurnID turn_1, got %q", active)
	}
}

func TestTurnStartResultParseAcceptsFlatTurnID(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"turnId": "turn_1",
	})
	if err != nil {
		t.Fatalf("Marshal turn/start result: %v", err)
	}
	if got := parseTurnIDFromResult(raw); got != "turn_1" {
		t.Fatalf("expected parseTurnIDFromResult turn_1, got %q", got)
	}
}

func TestTurnStartedClosesWaitChannel(t *testing.T) {
	engine := New("/tmp", nil, false)

	engine.mu.Lock()
	ch := engine.remoteTurnStartedCh
	engine.mu.Unlock()
	if ch == nil {
		t.Fatalf("expected remoteTurnStartedCh to be initialized")
	}

	turnStartedParams, err := json.Marshal(map[string]any{
		"turn": map[string]any{"id": "turn_1"},
	})
	if err != nil {
		t.Fatalf("Marshal turn/started params: %v", err)
	}
	engine.handleTurnStarted(turnStartedParams)

	select {
	case <-ch:
		// ok
	default:
		t.Fatalf("expected turn/started to close wait channel")
	}
}

func TestRenderFileChangeItemIncludesDiff(t *testing.T) {
	item := map[string]any{
		"type": "fileChange",
		"changes": []any{
			map[string]any{
				"path": "/tmp/hello.txt",
				"kind": map[string]any{"type": "add"},
				"diff": "hello\n",
			},
		},
	}

	brief, full := renderFileChangeItem(item)
	if brief != "Patch: 1 file" {
		t.Fatalf("expected brief Patch: 1 file, got %q", brief)
	}
	if !strings.Contains(full, "```diff") {
		t.Fatalf("expected diff code block, got: %q", full)
	}
	if !strings.Contains(full, "hello") {
		t.Fatalf("expected diff content, got: %q", full)
	}
}

func TestRenderToolItemCommandExecutionShowsFullCommandInBrief(t *testing.T) {
	item := map[string]any{
		"type":             "commandExecution",
		"command":          "echo hello && echo world",
		"aggregatedOutput": "hello\nworld\n",
	}

	brief, full := renderToolItem(item)
	if !strings.Contains(brief, "Tool: shell") {
		t.Fatalf("expected brief to include tool label, got %q", brief)
	}
	if strings.Contains(brief, "Output:") {
		t.Fatalf("expected brief to omit output, got %q", brief)
	}
	if !strings.Contains(brief, "```sh") || !strings.Contains(brief, "echo hello && echo world") {
		t.Fatalf("expected brief to include full command, got %q", brief)
	}
	if !strings.Contains(full, "Output:") {
		t.Fatalf("expected full to include output section, got %q", full)
	}
	if !strings.Contains(full, "hello") || !strings.Contains(full, "world") {
		t.Fatalf("expected full to include output content, got %q", full)
	}
}

func TestRenderReasoningItemUsesSummaryTextForBrief(t *testing.T) {
	item := map[string]any{
		"type":    "reasoning",
		"summary": []any{"First line", "Second line"},
	}

	brief, full := renderReasoningItem(item)
	if brief != "First line" {
		t.Fatalf("expected brief to be first summary line, got %q", brief)
	}
	if strings.TrimSpace(full) != "Reasoning\n\nFirst line\nSecond line" {
		t.Fatalf("unexpected full reasoning markdown: %q", full)
	}
}

func TestRenderToolItemCommandExecutionSplitsOutput(t *testing.T) {
	item := map[string]any{
		"type":             "commandExecution",
		"command":          "echo hi\nls",
		"aggregatedOutput": "hi\n",
	}

	brief, full := renderToolItem(item)
	if strings.Contains(brief, "Output:") {
		t.Fatalf("expected brief to omit output, got: %q", brief)
	}
	if !strings.Contains(brief, "```sh") || !strings.Contains(brief, "echo hi\nls") {
		t.Fatalf("expected command block in brief, got: %q", brief)
	}
	if !strings.Contains(full, "Output:") {
		t.Fatalf("expected full to include output, got: %q", full)
	}
}

func TestRenderReasoningItemSkipsHeadingOnly(t *testing.T) {
	t.Run("heading only", func(t *testing.T) {
		item := map[string]any{
			"summary": []any{"Reasoning"},
		}
		brief, full := renderReasoningItem(item)
		if brief != "" || full != "" {
			t.Fatalf("expected empty markdown, got brief=%q full=%q", brief, full)
		}
	})

	t.Run("heading then summary", func(t *testing.T) {
		item := map[string]any{
			"summary": []any{"Reasoning", "Check invariants"},
		}
		brief, full := renderReasoningItem(item)
		if brief == "" || full == "" {
			t.Fatalf("expected non-empty markdown, got brief=%q full=%q", brief, full)
		}
		if brief == "Reasoning" {
			t.Fatalf("expected brief to contain summary, got: %q", brief)
		}
		if !strings.Contains(full, "Check invariants") {
			t.Fatalf("expected full to include summary, got: %q", full)
		}
	})
}
