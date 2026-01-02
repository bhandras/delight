//go:build darwin || linux

package codexengine

import (
	"os/exec"
	"testing"
	"time"
)

func TestStopLocalCmd_KillsProcessGroup(t *testing.T) {
	t.Parallel()

	// Spawn a shell that starts a background child and then sleeps.
	// If we only kill the parent, the child can remain running.
	cmd := exec.Command("sh", "-c", "sleep 100 & sleep 100")
	configureLocalCmdProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	stopLocalCmd(cmd)

	// Wait should return quickly after termination.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatalf("expected cmd to be stopped")
	}
}
