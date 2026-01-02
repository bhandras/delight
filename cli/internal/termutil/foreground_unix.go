//go:build darwin || linux

package termutil

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	foregroundTTYPath = "/dev/tty"
)

const (
	// ForegroundRetryTimeout bounds how long we retry foreground restoration when
	// switching between local TUIs and remote mode.
	ForegroundRetryTimeout = 1 * time.Second

	// ForegroundRetryInterval bounds how often we retry tcsetpgrp.
	ForegroundRetryInterval = 50 * time.Millisecond
)

// EnsureTTYForegroundSelf best-effort makes the current process group the
// foreground process group for the controlling tty.
//
// After switching between local full-screen TUIs (notably Codex) and remote
// mode, the foreground process group can be left pointing at an exited or
// background process group. When that happens, the CLI stops receiving tty
// input (including Ctrl+C/space), so remote-mode shutdown appears "stuck".
func EnsureTTYForegroundSelf() {
	tty, err := os.OpenFile(foregroundTTYPath, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer func() { _ = tty.Close() }()

	fd := int(tty.Fd())
	pgid := syscall.Getpgrp()
	if pgid <= 0 {
		return
	}

	deadline := time.Now().Add(ForegroundRetryTimeout)
	for {
		current, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		if err == nil && current == pgid {
			return
		}
		withIgnoredJobControlSignals(func() {
			_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgid)
		})
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(ForegroundRetryInterval)
	}
}

// withIgnoredJobControlSignals runs fn while ignoring SIGTTOU/SIGTTIN.
//
// Foreground process group changes (tcsetpgrp) can trigger SIGTTOU when invoked
// by a background process group. If that stops the CLI, the user's terminal can
// be left in a broken raw state.
func withIgnoredJobControlSignals(fn func()) {
	if fn == nil {
		return
	}
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	defer signal.Reset(syscall.SIGTTOU, syscall.SIGTTIN)
	fn()
}
