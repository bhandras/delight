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

	ev := readEngineEvent(t, engine, 3*time.Second)
	out, ok := ev.(agentengine.EvOutboundRecord)
	if !ok {
		t.Fatalf("expected EvOutboundRecord, got %T", ev)
	}
	if out.Mode != agentengine.ModeRemote {
		t.Fatalf("expected remote mode, got %q", out.Mode)
	}
	if len(out.Payload) == 0 {
		t.Fatalf("expected non-empty payload")
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
