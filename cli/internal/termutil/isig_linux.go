//go:build linux

package termutil

import "golang.org/x/sys/unix"

// EnableISIG best-effort re-enables ISIG on the controlling tty.
//
// term.MakeRaw disables ISIG, which makes Ctrl+C stop generating SIGINT and
// instead deliver an ETX byte to reads. Remote mode depends on Ctrl+C for
// reliable exit, so we restore ISIG while still keeping canonical mode off.
func EnableISIG(fd int) {
	if fd < 0 {
		return
	}
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil || termios == nil {
		return
	}
	termios.Lflag |= unix.ISIG
	_ = unix.IoctlSetTermios(fd, unix.TCSETS, termios)
}
