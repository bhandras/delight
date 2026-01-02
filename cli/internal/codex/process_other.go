//go:build !darwin && !linux

package codex

import "os/exec"

func configureCodexMCPProcess(cmd *exec.Cmd) {}

func stopCodexMCPProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
