package actor

import (
	"os"

	"golang.org/x/term"
)

const (
	// ttyModeResetSequence disables a handful of terminal modes that TUIs often
	// enable but do not always reliably disable on abnormal exit.
	//
	// Notably, Codex local mode can enable the Kitty keyboard protocol; if it is
	// left enabled, Ctrl+C no longer maps to ETX/SIGINT and remote mode appears
	// unresponsive (you'll see `...u` escape sequences printed instead).
	//
	// We include terminal reset sequences (DECSTR + RIS) because emulator-level
	// keyboard protocols are not controlled by termios and must be reset
	// explicitly.
	ttyModeResetSequence = "" +
		"\x1b[!p" + // DECSTR (soft reset)
		"\x1bc" + // RIS (hard reset)
		"\x1b[0m" + // reset attributes
		"\x1b[?25h" + // show cursor
		"\x1b[?2004l" + // bracketed paste off
		"\x1b[>0u" + // kitty keyboard protocol off (best-effort)
		"\x1b[>1;0m" + // modifyOtherKeys off (xterm)
		"\x1b[?1000l" + // mouse reporting off
		"\x1b[?1002l" + // mouse button-event off
		"\x1b[?1003l" + // mouse any-event off
		"\x1b[?1006l" // SGR mouse mode off
)

// resetTTYModes best-effort disables terminal modes that can break Ctrl+C and
// input handling after switching away from a full-screen TUI.
func resetTTYModes() {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return
	}

	// Prefer writing to /dev/tty so we reach the controlling terminal even if
	// stdout is redirected.
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err == nil {
		_, _ = tty.WriteString(ttyModeResetSequence)
		_ = tty.Close()
		return
	}
	_, _ = os.Stdout.WriteString(ttyModeResetSequence)
}
