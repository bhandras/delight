package claudeengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/shared/wire"
)

type fakePermissionRequester struct {
	allow   bool
	message string
}

func (f fakePermissionRequester) AwaitPermission(ctx context.Context, requestID string, toolName string, input json.RawMessage, nowMs int64) (agentengine.PermissionDecision, error) {
	_ = ctx
	_ = requestID
	_ = toolName
	_ = input
	_ = nowMs
	return agentengine.PermissionDecision{Allow: f.allow, Message: f.message}, nil
}

type blockingRequester struct{}

func (blockingRequester) AwaitPermission(ctx context.Context, requestID string, toolName string, input json.RawMessage, nowMs int64) (agentengine.PermissionDecision, error) {
	_ = requestID
	_ = toolName
	_ = input
	_ = nowMs
	<-ctx.Done()
	return agentengine.PermissionDecision{}, ctx.Err()
}

func TestBuildRawRecordBytesFromRemote_Message(t *testing.T) {
	msg := &claude.RemoteMessage{
		Type: "message",
		Role: "assistant",
		Content: []wire.ContentBlock{
			{Type: "text", Text: "hello"},
		},
		Model: "test-model",
		Meta:  map[string]any{"k": "v"},
	}

	raw, ok := buildRawRecordBytesFromRemote(msg)
	if !ok {
		t.Fatalf("expected ok=true")
	}

	var rec wire.AgentOutputRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Role != "agent" {
		t.Fatalf("unexpected role: %q", rec.Role)
	}
	if rec.Content.Type != "output" {
		t.Fatalf("unexpected content.type: %q", rec.Content.Type)
	}
	if rec.Content.Data.Message.Role != "assistant" {
		t.Fatalf("unexpected message.role: %q", rec.Content.Data.Message.Role)
	}
}

func TestBuildRawRecordBytesFromRemote_Raw(t *testing.T) {
	rawJSON := `{"role":"agent","content":{"type":"output","data":{"type":"assistant","uuid":"x","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}}}`
	msg := &claude.RemoteMessage{
		Type:    "raw",
		Message: json.RawMessage(rawJSON),
	}
	raw, ok := buildRawRecordBytesFromRemote(msg)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if string(raw) != rawJSON {
		t.Fatalf("expected raw passthrough")
	}
}

func TestHandleRemotePermissionRequest_AllowsEchoInput(t *testing.T) {
	e := New(".", fakePermissionRequester{allow: true, message: "ignored"}, false)
	e.mu.Lock()
	e.remoteCtx = context.Background()
	e.mu.Unlock()

	input := json.RawMessage(`{"x":1}`)
	resp, err := e.handleRemotePermissionRequest("req-1", "tool", input)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response")
		return
	}
	if resp.Behavior != "allow" {
		t.Fatalf("expected allow, got %q", resp.Behavior)
	}
	if resp.Message != "" {
		t.Fatalf("expected empty message for allow, got %q", resp.Message)
	}
	if string(resp.UpdatedInput) != string(input) {
		t.Fatalf("expected updatedInput to echo original input, got %s", string(resp.UpdatedInput))
	}
}

func TestHandleRemotePermissionRequest_DeniesOnTimeoutCtx(t *testing.T) {
	e := New(".", blockingRequester{}, false)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	e.mu.Lock()
	e.remoteCtx = timeoutCtx
	e.mu.Unlock()

	resp, err := e.handleRemotePermissionRequest("req-1", "tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response")
		return
	}
	if resp.Behavior != "deny" {
		t.Fatalf("expected deny, got %q", resp.Behavior)
	}
	if resp.Message == "" {
		t.Fatalf("expected message for deny on ctx error")
	}
}

func TestCanonicalClaudeModelMapsAliases(t *testing.T) {
	if got := canonicalClaudeModel("haiku"); got != claudeModelHaiku {
		t.Fatalf("canonicalClaudeModel(haiku)=%q, want %q", got, claudeModelHaiku)
	}
	if got := canonicalClaudeModel("sonnet"); got != claudeModelSonnet {
		t.Fatalf("canonicalClaudeModel(sonnet)=%q, want %q", got, claudeModelSonnet)
	}
	if got := canonicalClaudeModel("opus"); got != claudeModelOpus {
		t.Fatalf("canonicalClaudeModel(opus)=%q, want %q", got, claudeModelOpus)
	}
	if got := canonicalClaudeModel("default"); got != "default" {
		t.Fatalf("canonicalClaudeModel(default)=%q, want %q", got, "default")
	}
}

func TestHandleRemoteBridgeMessageEmitsSessionIdentified(t *testing.T) {
	e := New(".", nil, false)
	if err := e.handleRemoteBridgeMessage(&claude.RemoteMessage{
		Type:      "system",
		Subtype:   "init",
		SessionID: "sess-1",
	}); err != nil {
		t.Fatalf("handleRemoteBridgeMessage returned error: %v", err)
	}

	select {
	case ev := <-e.Events():
		identified, ok := ev.(agentengine.EvSessionIdentified)
		if !ok {
			t.Fatalf("expected EvSessionIdentified, got %T", ev)
		}
		if identified.Mode != agentengine.ModeRemote {
			t.Fatalf("expected remote mode, got %q", identified.Mode)
		}
		if identified.ResumeToken != "sess-1" {
			t.Fatalf("expected resume token %q, got %q", "sess-1", identified.ResumeToken)
		}
	default:
		t.Fatalf("expected session identified event")
	}
}

func TestHandleRemoteBridgeMessageDedupesSessionIdentified(t *testing.T) {
	e := New(".", nil, false)
	e.mu.Lock()
	e.remoteSessionID = "sess-1"
	e.mu.Unlock()

	if err := e.handleRemoteBridgeMessage(&claude.RemoteMessage{
		Type:      "system",
		Subtype:   "init",
		SessionID: "sess-1",
	}); err != nil {
		t.Fatalf("handleRemoteBridgeMessage returned error: %v", err)
	}

	select {
	case ev := <-e.Events():
		t.Fatalf("expected no event, got %T", ev)
	default:
	}
}

func TestEmitRemoteUIEventsFromRaw_DoesNotClearThinkingOnAssistantText(t *testing.T) {
	e := New(".", nil, false)
	e.startRemoteTurn()
	e.setRemoteWorking(true, time.Now().UnixMilli())

	before := time.Now().UnixMilli()
	raw := []byte(`{"role":"agent","content":{"type":"output","data":{"type":"assistant","uuid":"x","message":{"role":"assistant","model":"test","content":[{"type":"text","text":"hello"}]}}}}`)
	e.emitRemoteUIEventsFromRaw(raw, before)

	e.mu.Lock()
	gotWorking := e.remoteWorking
	e.mu.Unlock()
	if !gotWorking {
		t.Fatalf("expected remoteWorking to remain true after assistant text")
	}
}

func TestHandleRemoteBridgeMessage_ResultClearsThinking(t *testing.T) {
	e := New(".", nil, false)
	e.startRemoteTurn()
	e.setRemoteWorking(true, time.Now().UnixMilli())

	if err := e.handleRemoteBridgeMessage(&claude.RemoteMessage{
		Type:   "result",
		Result: "ok",
	}); err != nil {
		t.Fatalf("handleRemoteBridgeMessage returned error: %v", err)
	}

	e.mu.Lock()
	gotWorking := e.remoteWorking
	e.mu.Unlock()
	if gotWorking {
		t.Fatalf("expected remoteWorking=false after result")
	}
}

func TestEmitRemoteUIEventsFromRaw_EmitsThinkingAsReasoning(t *testing.T) {
	e := New(".", nil, false)
	e.startRemoteTurn()

	// Collect emitted events in a separate goroutine.
	var events []agentengine.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case ev, ok := <-e.Events():
				if !ok {
					return
				}
				events = append(events, ev)
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()

	// Raw record with a thinking block containing thinking text.
	raw := []byte(`{"role":"agent","content":{"type":"output","data":{"type":"assistant","uuid":"x","message":{"role":"assistant","model":"test","content":[{"type":"thinking","thinking":"I need to analyze this problem carefully."}]}}}}`)
	e.emitRemoteUIEventsFromRaw(raw, time.Now().UnixMilli())

	// Wait for event collection to complete.
	<-done

	// Look for reasoning UI event.
	var foundReasoning bool
	for _, ev := range events {
		if uiEv, ok := ev.(agentengine.EvUIEvent); ok {
			if uiEv.Kind == agentengine.UIEventReasoning {
				foundReasoning = true
				if uiEv.BriefMarkdown == "" {
					t.Errorf("expected non-empty BriefMarkdown")
				}
				if uiEv.FullMarkdown == "" {
					t.Errorf("expected non-empty FullMarkdown")
				}
			}
		}
	}

	if !foundReasoning {
		t.Fatalf("expected reasoning UI event to be emitted for thinking block")
	}
}

func TestRenderThinkingMarkdown_BasicText(t *testing.T) {
	brief, full := renderThinkingMarkdown("I need to analyze this problem.")
	if brief != "I need to analyze this problem." {
		t.Errorf("unexpected brief: %q", brief)
	}
	if full != "Reasoning\n\nI need to analyze this problem." {
		t.Errorf("unexpected full: %q", full)
	}
}

func TestRenderThinkingMarkdown_TruncatesLongBrief(t *testing.T) {
	// Create a string that is definitely over 160 characters
	longText := "This is a very long line of text that exceeds one hundred and sixty characters and should definitely be truncated to fit in the brief markdown field properly with extra words added here."
	if len(longText) <= 160 {
		t.Fatalf("test setup error: longText should be >160 chars, got %d", len(longText))
	}
	brief, _ := renderThinkingMarkdown(longText)
	if len(brief) > 160 {
		t.Errorf("brief should be truncated to 160 chars, got %d", len(brief))
	}
	if brief[len(brief)-3:] != "..." {
		t.Errorf("truncated brief should end with '...', got %q", brief)
	}
}

func TestRenderThinkingMarkdown_SkipsReasoningOnlyHeading(t *testing.T) {
	brief, full := renderThinkingMarkdown("Reasoning")
	if brief != "" || full != "" {
		t.Errorf("expected empty result for 'Reasoning' only, got brief=%q full=%q", brief, full)
	}

	brief, full = renderThinkingMarkdown("# Reasoning")
	if brief != "" || full != "" {
		t.Errorf("expected empty result for '# Reasoning' only, got brief=%q full=%q", brief, full)
	}
}

func TestRenderThinkingMarkdown_UsesSecondLineWhenFirstIsHeading(t *testing.T) {
	text := "Reasoning\n\nActual thinking content here."
	brief, full := renderThinkingMarkdown(text)
	if brief != "Actual thinking content here." {
		t.Errorf("expected brief to be second line content, got %q", brief)
	}
	if full == "" {
		t.Errorf("expected non-empty full markdown")
	}
}

func TestBuildRawRecordBytesFromRemote_AssistantPreservesAllBlocks(t *testing.T) {
	// When an assistant message has tool_use and thinking blocks alongside
	// text, all blocks must be preserved in the output record.
	msg := &claude.RemoteMessage{
		Type: "assistant",
		Content: []any{
			map[string]any{"type": "text", "text": "Let me check."},
			map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "ls"}},
			map[string]any{"type": "thinking", "thinking": "I should read the file."},
		},
		Model: "test-model",
	}

	raw, ok := buildRawRecordBytesFromRemote(msg)
	if !ok {
		t.Fatalf("expected ok=true")
	}

	var rec wire.AgentOutputRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	blocks := rec.Content.Data.Message.Content
	if len(blocks) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("block 0: expected type 'text', got %q", blocks[0].Type)
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("block 1: expected type 'tool_use', got %q", blocks[1].Type)
	}
	if blocks[2].Type != "thinking" {
		t.Errorf("block 2: expected type 'thinking', got %q", blocks[2].Type)
	}
}

func TestRenderPatchMarkdown_Edit(t *testing.T) {
	input := map[string]any{
		"file_path":  "src/main.go",
		"old_string": "hello",
		"new_string": "world",
	}
	brief, full, ok := renderPatchMarkdown("edit", input)
	if !ok {
		t.Fatalf("expected ok=true for edit tool")
	}
	if brief != "Patch: `src/main.go`" {
		t.Errorf("unexpected brief: %q", brief)
	}
	if !strings.Contains(full, "```diff") {
		t.Errorf("expected ```diff fence in full, got %q", full)
	}
	if !strings.Contains(full, "-hello") {
		t.Errorf("expected -hello in diff, got %q", full)
	}
	if !strings.Contains(full, "+world") {
		t.Errorf("expected +world in diff, got %q", full)
	}
}

func TestRenderPatchMarkdown_EditMultiline(t *testing.T) {
	input := map[string]any{
		"file_path":  "foo.txt",
		"old_string": "line1\nline2",
		"new_string": "line1\nchanged",
	}
	brief, full, ok := renderPatchMarkdown("edit", input)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !strings.HasPrefix(brief, "Patch") {
		t.Errorf("brief should start with Patch, got %q", brief)
	}
	if !strings.Contains(full, "-line1\n-line2") {
		t.Errorf("expected old lines prefixed with -, got %q", full)
	}
	if !strings.Contains(full, "+line1\n+changed") {
		t.Errorf("expected new lines prefixed with +, got %q", full)
	}
}

func TestRenderPatchMarkdown_Write(t *testing.T) {
	input := map[string]any{
		"file_path": "new_file.txt",
		"content":   "hello\nworld",
	}
	brief, full, ok := renderPatchMarkdown("write", input)
	if !ok {
		t.Fatalf("expected ok=true for write tool")
	}
	if brief != "Patch: `new_file.txt`" {
		t.Errorf("unexpected brief: %q", brief)
	}
	if !strings.Contains(full, "+hello") {
		t.Errorf("expected +hello in diff, got %q", full)
	}
}

func TestRenderPatchMarkdown_NonEditTool(t *testing.T) {
	_, _, ok := renderPatchMarkdown("bash", map[string]any{"command": "ls"})
	if ok {
		t.Fatalf("expected ok=false for non-edit tool")
	}
}

func TestRenderPatchMarkdown_MissingFilePath(t *testing.T) {
	input := map[string]any{
		"old_string": "hello",
		"new_string": "world",
	}
	_, _, ok := renderPatchMarkdown("edit", input)
	if ok {
		t.Fatalf("expected ok=false when file_path is missing")
	}
}

func TestRenderToolMarkdown_EditDelegatesToPatch(t *testing.T) {
	input := map[string]any{
		"file_path":  "test.go",
		"old_string": "foo",
		"new_string": "bar",
	}
	brief, full := renderToolMarkdown("Edit", input, nil, agentengine.UIEventStatusRunning)
	if !strings.HasPrefix(brief, "Patch") {
		t.Errorf("expected brief to start with 'Patch', got %q", brief)
	}
	if !strings.Contains(full, "```diff") {
		t.Errorf("expected diff fence in full, got %q", full)
	}
}
