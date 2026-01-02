//go:build !darwin && !linux

package codexengine

import "os/exec"

// acquireTTYForeground is a no-op on unsupported platforms.
func acquireTTYForeground(cmd *exec.Cmd) func() {
	_ = cmd
	return nil
}
