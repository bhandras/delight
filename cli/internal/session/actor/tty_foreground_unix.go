//go:build darwin || linux

package actor

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
	// ttyForegroundRetryTimeout bounds how long we retry foreground restoration
	// when switching between local TUIs and remote mode.
	ttyForegroundRetryTimeout = 1 * time.Second

	// ttyForegroundRetryInterval bounds how often we retry tcsetpgrp.
	ttyForegroundRetryInterval = 50 * time.Millisecond
)

// ensureTTYForegroundSelf best-effort makes Delight's process group the
// foreground process group for the controlling tty.
//
// After switching between local full-screen TUIs (notably Codex) and remote
// mode, the foreground process group can be left pointing at an exited or
// background process group. When that happens, Delight stops receiving tty
// input (including Ctrl+C/space), so remote-mode shutdown appears "stuck".
func ensureTTYForegroundSelf() {
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

	deadline := time.Now().Add(ttyForegroundRetryTimeout)
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
		time.Sleep(ttyForegroundRetryInterval)
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
