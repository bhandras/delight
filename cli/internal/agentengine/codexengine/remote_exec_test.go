package codexengine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSandboxPolicyMapsPermissionModes(t *testing.T) {
	cases := []struct {
		permissionMode string
		want           string
	}{
		{"", "workspace-write"},
		{"default", "workspace-write"},
		{"safe-yolo", "workspace-write"},
		{"read-only", "read-only"},
		{"yolo", "danger-full-access"},
		{"unknown", "workspace-write"},
	}

	for _, tc := range cases {
		got := sandboxPolicy(normalizePermissionMode(tc.permissionMode))
		if got != tc.want {
			t.Fatalf("sandboxPolicy(%q)=%q, want %q", tc.permissionMode, got, tc.want)
		}
	}
}

func TestBuildCodexExecCommandIncludesNonInteractiveFlags(t *testing.T) {
	cmd, stdout, stderr, err := buildCodexExecCommand(context.Background(), codexExecTurnSpec{
		WorkDir:         "/tmp",
		Prompt:          "hello",
		Model:           "gpt-5.2-codex",
		ReasoningEffort: "high",
		Sandbox:         "read-only",
	})
	if err != nil {
		t.Fatalf("buildCodexExecCommand returned error: %v", err)
	}
	_ = stdout.Close()
	_ = stderr.Close()

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, " -a never ") {
		t.Fatalf("expected -a never in args, got: %s", args)
	}
	if !strings.Contains(args, " exec --skip-git-repo-check ") {
		t.Fatalf("expected exec --skip-git-repo-check in args, got: %s", args)
	}
	if !strings.Contains(args, " -s read-only ") {
		t.Fatalf("expected -s read-only in args, got: %s", args)
	}
	if !strings.Contains(args, " -m gpt-5.2-codex ") {
		t.Fatalf("expected -m in args, got: %s", args)
	}
	if !strings.Contains(args, `model_reasoning_effort="high"`) {
		t.Fatalf("expected model_reasoning_effort override in args, got: %s", args)
	}
	if !strings.Contains(args, " exec ") || !strings.HasSuffix(args, " --json") {
		t.Fatalf("expected exec ... --json, got: %s", args)
	}
}

func TestBuildCodexExecCommandResumeShape(t *testing.T) {
	cmd, stdout, stderr, err := buildCodexExecCommand(context.Background(), codexExecTurnSpec{
		WorkDir:     "/tmp",
		Prompt:      "ping",
		Sandbox:     "workspace-write",
		ResumeToken: "abc",
	})
	if err != nil {
		t.Fatalf("buildCodexExecCommand returned error: %v", err)
	}
	_ = stdout.Close()
	_ = stderr.Close()

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, " exec --skip-git-repo-check ping resume abc --json") {
		t.Fatalf("expected resume subcommand, got: %s", args)
	}
}

func TestCaptureCodexExecStderrCapturesAndTruncates(t *testing.T) {
	input := strings.Repeat("x", 64) + "\n"
	got := captureCodexExecStderr(strings.NewReader(input), 16)
	if got.Text == "" {
		t.Fatalf("expected captured text, got empty")
	}
	if len(got.Text) > 16 {
		t.Fatalf("expected captured text <= 16 bytes, got %d", len(got.Text))
	}
	if !got.Truncated {
		t.Fatalf("expected truncated=true")
	}
}

func TestFormatCodexExecExitErrorIncludesStderr(t *testing.T) {
	waitErr := errors.New("exit status 1")
	msg := formatCodexExecExitError(waitErr, codexExecStderrCapture{
		Text:      "unknown flag: --whatever",
		Truncated: false,
	})
	if !strings.Contains(msg, "Codex exec failed") {
		t.Fatalf("expected base error message, got: %s", msg)
	}
	if !strings.Contains(msg, "stderr:") {
		t.Fatalf("expected stderr label, got: %s", msg)
	}
	if !strings.Contains(msg, "unknown flag") {
		t.Fatalf("expected stderr content, got: %s", msg)
	}
}
