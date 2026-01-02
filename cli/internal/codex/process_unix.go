//go:build darwin || linux

package codex

import (
	"os/exec"
	"syscall"
)

// configureCodexMCPProcess starts cmd in a new process group so a stop can
// terminate the entire Codex MCP process tree.
func configureCodexMCPProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// stopCodexMCPProcess terminates the cmd process group if possible, falling back
// to killing the parent process.
func stopCodexMCPProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = cmd.Process.Kill()
		return
	}

	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
