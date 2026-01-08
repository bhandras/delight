//go:build darwin || linux

package codexengine

import (
	"os/exec"
	"syscall"
	"time"
)

const (
	// localStopInterruptGrace is the best-effort wait after sending SIGINT before
	// escalating to SIGTERM.
	localStopInterruptGrace = 1 * time.Second

	// localStopTerminateGrace is the best-effort wait after SIGTERM before
	// escalating to SIGKILL.
	localStopTerminateGrace = 500 * time.Millisecond

	// remoteExecStopInterruptGrace is the best-effort wait after sending SIGINT
	// to a `codex exec` process before escalating to SIGKILL. Remote exec is not
	// a full-screen TUI, so we keep this window short to honor mobile "Stop"
	// quickly while still allowing Codex to handle Ctrl+C semantics.
	remoteExecStopInterruptGrace = 200 * time.Millisecond
)

// configureLocalCmdProcessGroup starts cmd in a new process group so stopLocalCmd
// can terminate the entire Codex local process tree.
func configureLocalCmdProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// stopLocalCmd terminates the cmd process group if possible, falling back to a
// direct kill of the parent process.
func stopLocalCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = cmd.Process.Kill()
		return
	}

	// Mimic Ctrl+C first so Codex has a chance to restore terminal state.
	// If Setpgid failed, these may target the wrong group; we only do this on
	// Unix where we configured Setpgid=true.
	_ = syscall.Kill(-pid, syscall.SIGINT)

	// Do not call cmd.Wait() here. The engine owns the single waiter goroutine
	// so stop requests never risk a double-wait panic.
	go func() {
		time.Sleep(localStopInterruptGrace)

		_ = syscall.Kill(-pid, syscall.SIGTERM)

		time.Sleep(localStopTerminateGrace)

		// Escalate to SIGKILL for the whole process group.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}()
}

// stopExecCmd terminates a `codex exec` process group.
//
// This mirrors stopLocalCmd but with much shorter escalation: remote exec turns
// should stop promptly when a user presses Stop on mobile.
func stopExecCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = cmd.Process.Kill()
		return
	}

	_ = syscall.Kill(-pid, syscall.SIGINT)

	go func() {
		time.Sleep(remoteExecStopInterruptGrace)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}()
}
