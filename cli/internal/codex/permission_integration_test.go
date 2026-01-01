package codex

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// permissionIntegrationTimeout bounds how long we wait for Codex to execute a command.
	permissionIntegrationTimeout = 90 * time.Second
)

// TestPermissionIntegration_AutoApproveExec validates that MCP elicitation/request
// permission prompts can be approved and allow an exec tool-call to complete.
//
// This is an integration test: it requires a working `codex` binary on PATH and
// access to an OpenAI account. It is gated by CODEX_PERMISSION_INTEGRATION_TEST=1.
func TestPermissionIntegration_AutoApproveExec(t *testing.T) {
	if os.Getenv("CODEX_PERMISSION_INTEGRATION_TEST") != "1" {
		t.Skip("set CODEX_PERMISSION_INTEGRATION_TEST=1 to run")
	}

	workDir := t.TempDir()

	client := NewClient(workDir, true)
	defer func() {
		_ = client.Close()
	}()

	permissionCalled := make(chan struct{}, 1)
	client.SetPermissionHandler(func(requestID string, toolName string, input map[string]interface{}) (*PermissionDecision, error) {
		select {
		case permissionCalled <- struct{}{}:
		default:
		}
		return &PermissionDecision{
			Decision: "approved",
			Message:  "",
		}, nil
	})

	execCompleted := make(chan map[string]interface{}, 1)
	client.SetEventHandler(func(event map[string]interface{}) {
		if event == nil {
			return
		}
		typ, _ := event["type"].(string)
		if typ == "exec_command_end" {
			select {
			case execCompleted <- event:
			default:
			}
		}
	})

	require.NoError(t, client.Start())

	ctx, cancel := context.WithTimeout(context.Background(), permissionIntegrationTimeout)
	defer cancel()

	_, err := client.StartSession(ctx, SessionConfig{
		Prompt:         "Use the terminal tool to run: echo CODEX_PERMISSION_TEST",
		ApprovalPolicy: "untrusted",
		Sandbox:        "workspace-write",
		Cwd:            workDir,
	})
	require.NoError(t, err)

	select {
	case <-permissionCalled:
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for permission request")
	}

	select {
	case <-execCompleted:
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for exec_command_end")
	}
}
