package claudeengine

import (
	"context"
	"encoding/json"
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
	}
	if resp.Behavior != "deny" {
		t.Fatalf("expected deny, got %q", resp.Behavior)
	}
	if resp.Message == "" {
		t.Fatalf("expected message for deny on ctx error")
	}
}
