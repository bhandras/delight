package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewProcessIncludesModelAndPermissionModeArgs(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	launcher := filepath.Clean(filepath.Join(cwd, "..", "..", "scripts", "claude_launcher.cjs"))
	t.Setenv("DELIGHT_CLAUDE_LAUNCHER_PATH", launcher)

	proc, err := NewProcess(ProcessOptions{
		WorkDir:        ".",
		ResumeToken:    "sess-1",
		Model:          "sonnet",
		PermissionMode: "plan",
		Debug:          false,
	})
	if err != nil {
		t.Fatalf("NewProcess returned error: %v", err)
	}

	args := strings.Join(proc.cmd.Args, " ")
	if !strings.Contains(args, " --resume sess-1 ") {
		t.Fatalf("expected --resume in args, got: %s", args)
	}
	if !strings.Contains(args, " --model sonnet ") {
		t.Fatalf("expected --model in args, got: %s", args)
	}
	if !strings.Contains(args, "--permission-mode plan") {
		t.Fatalf("expected --permission-mode in args, got: %s", args)
	}
}
