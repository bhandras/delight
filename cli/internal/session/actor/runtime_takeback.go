package actor

import (
	"context"
	"os"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	// takebackTTYPath is the device path used to read raw keystrokes for desktop
	// takeback.
	takebackTTYPath = "/dev/tty"

	// takebackReadBufSize is the read buffer size used when scanning keystrokes.
	takebackReadBufSize = 16

	// takebackPollTimeoutMillis is the poll timeout used for non-blocking tty reads.
	// unix.Poll expects milliseconds.
	takebackPollTimeoutMillis = 50

	// takebackConfirmWindow is the max time between two spaces to trigger takeback.
	takebackConfirmWindow = 15 * time.Second

	// takebackShutdownWait is the max time Stop() waits for the watcher goroutine
	// to exit (best-effort).
	takebackShutdownWait = 100 * time.Millisecond

	// takebackPrompt is printed after the first space to confirm takeback.
	takebackPrompt = "Press space again to take back control on desktop."

	// takebackCtrlCByte is the ASCII ETX byte emitted by Ctrl+C in raw mode.
	//
	// Note: when we put the terminal into raw mode (term.MakeRaw), ISIG is
	// disabled, so Ctrl+C no longer generates SIGINT. We must treat it as an
	// explicit shutdown request here.
	takebackCtrlCByte = 3
)

// startDesktopTakebackWatcher starts a raw tty watcher that emits evDesktopTakeback
// after the user presses space twice.
func (r *Runtime) startDesktopTakebackWatcher(ctx context.Context, emit func(framework.Input)) {
	r.mu.Lock()
	if r.takebackCancel != nil {
		r.mu.Unlock()
		return
	}
	cancel := make(chan struct{})
	done := make(chan struct{})
	r.takebackCancel = cancel
	r.takebackDone = done
	tty, err := os.OpenFile(takebackTTYPath, os.O_RDONLY, 0)
	if err != nil {
		r.takebackCancel = nil
		r.takebackDone = nil
		r.mu.Unlock()
		return
	}
	r.takebackTTY = tty
	r.mu.Unlock()

	fd := int(tty.Fd())
	if !term.IsTerminal(fd) {
		_ = tty.Close()
		r.stopDesktopTakebackWatcher()
		return
	}

	go func() {
		defer close(done)

		restored := false
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			_ = tty.Close()
			r.mu.Lock()
			if r.takebackDone == done {
				r.takebackCancel = nil
				r.takebackDone = nil
				r.takebackTTY = nil
				r.takebackState = nil
			}
			r.mu.Unlock()
			return
		}
		r.mu.Lock()
		if r.takebackDone == done {
			r.takebackState = oldState
		}
		r.mu.Unlock()

		defer func() {
			if !restored {
				_ = term.Restore(fd, oldState)
			}
			_ = tty.Close()
			r.mu.Lock()
			if r.takebackDone == done {
				r.takebackCancel = nil
				r.takebackDone = nil
				r.takebackTTY = nil
				r.takebackState = nil
			}
			r.mu.Unlock()
		}()

		buf := make([]byte, takebackReadBufSize)
		var pendingSpace bool
		var pendingSpaceAt time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-cancel:
				return
			default:
			}

			pollRes, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, takebackPollTimeoutMillis)
			if err != nil {
				if err == unix.EINTR {
					continue
				}
				return
			}
			if pollRes == 0 {
				continue
			}

			n, err := tty.Read(buf)
			if err != nil || n <= 0 {
				return
			}

			now := time.Now()
			if pendingSpace && now.Sub(pendingSpaceAt) > takebackConfirmWindow {
				pendingSpace = false
			}

			shouldSwitch := false
			shouldShutdown := false
			for _, b := range buf[:n] {
				if b == takebackCtrlCByte {
					shouldShutdown = true
					break
				}
				if b == ' ' {
					if pendingSpace {
						shouldSwitch = true
						break
					}
					pendingSpace = true
					pendingSpaceAt = now
					// Don't print prompts while a local interactive TUI is active;
					// it would corrupt the user's screen.
					r.mu.Lock()
					localActive := r.engineLocalInteractive
					r.mu.Unlock()
					if !localActive {
						writeLine(takebackPrompt)
					}
					continue
				}
				// Intentionally do not clear pendingSpace on other bytes. Some
				// terminals (or input methods) can inject non-space bytes that would
				// make the UX unreliable. Only the confirm window controls expiry.
			}
			if !shouldSwitch {
				if !shouldShutdown {
					continue
				}
			}

			_ = term.Restore(fd, oldState)
			restored = true
			r.mu.Lock()
			if r.takebackState == oldState {
				r.takebackState = nil
			}
			r.mu.Unlock()

			// Emit asynchronously so tty restoration and watcher teardown cannot
			// deadlock on actor mailbox backpressure.
			if shouldShutdown {
				go emit(Shutdown(nil))
			} else {
				go emit(evDesktopTakeback{})
			}
			return
		}
	}()
}

// stopDesktopTakebackWatcher stops the takeback watcher and best-effort restores tty state.
func (r *Runtime) stopDesktopTakebackWatcher() {
	r.mu.Lock()
	cancel, done, tty, state := r.stopDesktopTakebackWatcherLocked()
	r.mu.Unlock()

	if cancel != nil {
		func() {
			defer func() { recover() }()
			close(cancel)
		}()
	}

	if state != nil && tty != nil {
		_ = term.Restore(int(tty.Fd()), state)
	}

	if done != nil {
		select {
		case <-done:
		case <-time.After(takebackShutdownWait):
		}
	}
}

// stopDesktopTakebackWatcherLocked clears the internal watcher fields and returns the
// values needed for cleanup without holding the runtime lock.
func (r *Runtime) stopDesktopTakebackWatcherLocked() (chan struct{}, chan struct{}, *os.File, *term.State) {
	cancel := r.takebackCancel
	done := r.takebackDone
	tty := r.takebackTTY
	state := r.takebackState
	r.takebackCancel = nil
	r.takebackDone = nil
	r.takebackTTY = nil
	r.takebackState = nil
	return cancel, done, tty, state
}
