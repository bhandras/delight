package codexengine

import (
	"context"
	"strings"
	"testing"

	"github.com/bhandras/delight/cli/internal/agentengine"
)

// TestApplyConfigDoesNotClearRemoteResumeToken ensures ApplyConfig treats an
// empty model as "engine default" so resume state isn't lost after startup.
func TestApplyConfigDoesNotClearRemoteResumeToken(t *testing.T) {
	engine := New("/tmp", nil, false)
	resume := "resume-123"

	if err := engine.Start(context.Background(), agentengine.EngineStartSpec{
		Mode:        agentengine.ModeRemote,
		ResumeToken: resume,
		Config:      agentengine.AgentConfig{},
	}); err != nil {
		t.Fatalf("Start(remote) returned error: %v", err)
	}

	engine.mu.Lock()
	if engine.remoteSessionActive != true || engine.remoteResumeToken != resume {
		engine.mu.Unlock()
		t.Fatalf("expected remote session active with resume token %q", resume)
	}
	if engine.remoteModel != defaultRemoteModel {
		got := engine.remoteModel
		engine.mu.Unlock()
		t.Fatalf("expected remote model %q, got %q", defaultRemoteModel, got)
	}
	if engine.remoteReasoningEffort != defaultRemoteReasoningEffort {
		got := engine.remoteReasoningEffort
		engine.mu.Unlock()
		t.Fatalf("expected default reasoning effort %q, got %q", defaultRemoteReasoningEffort, got)
	}
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
