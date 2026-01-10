//go:build !darwin && !linux

package codexengine

import "os/exec"

// configureLocalCmdProcessGroup is a no-op on unsupported platforms.
func configureLocalCmdProcessGroup(cmd *exec.Cmd) {
	_ = cmd
}

// stopLocalCmd kills the parent process on unsupported platforms.
func stopLocalCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
