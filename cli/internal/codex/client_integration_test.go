package codex

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestClientCapturesRolloutPath verifies that Codex MCP responses include a rollout path
// and that the client captures it for later tailing.
//
// This is an integration test because it spawns the external `codex` binary.
func TestClientCapturesRolloutPath(t *testing.T) {
	if os.Getenv("CODEX_INTEGRATION_TEST") != "1" {
		t.Skip("set CODEX_INTEGRATION_TEST=1 to enable")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not found in PATH")
	}

	workDir := t.TempDir()
	client := NewClient(workDir, true)
	require.NoError(t, client.Start())
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	_, err := client.StartSession(ctx, SessionConfig{
		Prompt:         "Reply with just OK.",
		ApprovalPolicy: "never",
		Sandbox:        "read-only",
		Cwd:            workDir,
	})
	require.NoError(t, err)

	require.NotEmpty(t, client.SessionID())
	require.NotEmpty(t, client.RolloutPath())
}

