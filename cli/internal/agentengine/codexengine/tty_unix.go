//go:build darwin || linux

package codexengine

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	ttyPath = "/dev/tty"
)

// acquireTTYForeground makes cmd's process group the foreground process group
// for the controlling tty (best-effort) and returns a restore closure.
//
// This is necessary because we start Codex local mode in its own process group
// (Setpgid=true) so we can kill the full process tree on stop. If we don't also
// move the process group to the foreground, interactive reads can trigger
// SIGTTIN and stop the process ("T" state in ps).
func acquireTTYForeground(cmd *exec.Cmd) func() {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	tty, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		return nil
	}

	fd := int(tty.Fd())
	prev, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		_ = tty.Close()
		return nil
	}

	pgid, err := unix.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = tty.Close()
		return nil
	}

	withIgnoredJobControlSignals(func() {
		_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgid)
	})

	return func() {
		withIgnoredJobControlSignals(func() {
			_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, prev)
		})
		_ = tty.Close()
	}
}

// withIgnoredJobControlSignals runs fn while ignoring SIGTTOU/SIGTTIN.
//
// Foreground process group changes (tcsetpgrp) can trigger SIGTTOU when invoked
// by a background process group. If that stops Delight, the user's terminal can
// be left in a broken raw state. Ignoring these signals ensures we always
// restore the terminal and keep the session loop responsive.
func withIgnoredJobControlSignals(fn func()) {
	if fn == nil {
		return
	}
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	defer signal.Reset(syscall.SIGTTOU, syscall.SIGTTIN)
	fn()
}
