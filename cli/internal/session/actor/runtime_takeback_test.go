package actor

import (
	"context"
	"os"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func TestRuntime_StartDesktopTakebackWatcher_EmitsTakeback(t *testing.T) {
	// This test stubs all tty/termutil dependencies so we can exercise the core
	// watcher loop deterministically without touching the developer's terminal.
	//
	// IMPORTANT: do not run in parallel because it mutates package-level vars.

	origIsTerminal := takebackIsTerminal
	origOpenTTY := takebackOpenTTY
	origMakeRaw := takebackMakeRaw
	origRestore := takebackRestore
	origPoll := takebackPoll
	origEnableISIG := takebackEnableISIG
	origEnsureForeground := takebackEnsureForeground
	origWriteLine := takebackWriteLine
	origNow := takebackNow

	defer func() {
		takebackIsTerminal = origIsTerminal
		takebackOpenTTY = origOpenTTY
		takebackMakeRaw = origMakeRaw
		takebackRestore = origRestore
		takebackPoll = origPoll
		takebackEnableISIG = origEnableISIG
		takebackEnsureForeground = origEnsureForeground
		takebackWriteLine = origWriteLine
		takebackNow = origNow
	}()

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = writeEnd.Close() }()

	takebackIsTerminal = func(int) bool { return true }
	takebackOpenTTY = func() (*os.File, error) { return readEnd, nil }
	takebackMakeRaw = func(int) (*term.State, error) { return new(term.State), nil }
	takebackRestore = func(int, *term.State) error { return nil }
	takebackPoll = func([]unix.PollFd, int) (int, error) { return 1, nil }
	takebackEnableISIG = func(int) {}
	takebackEnsureForeground = func() {}
	takebackWriteLine = func(string) {}
	takebackNow = func() time.Time { return time.Unix(0, 0) }

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentCodex))

	emitCh := make(chan framework.Input, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rt.startDesktopTakebackWatcher(ctx, func(in framework.Input) {
		select {
		case emitCh <- in:
		default:
		}
	})

	// Two spaces should trigger takeback.
	_, _ = writeEnd.Write([]byte("  "))

	select {
	case got := <-emitCh:
		if _, ok := got.(evDesktopTakeback); !ok {
			t.Fatalf("got=%T, want evDesktopTakeback", got)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for takeback event: %v", ctx.Err())
	}
}

func TestRuntime_StartDesktopTakebackWatcher_CtrlCTriggersShutdown(t *testing.T) {
	// This test stubs all tty/termutil dependencies so we can exercise the core
	// watcher loop deterministically without touching the developer's terminal.
	//
	// IMPORTANT: do not run in parallel because it mutates package-level vars.

	origIsTerminal := takebackIsTerminal
	origOpenTTY := takebackOpenTTY
	origMakeRaw := takebackMakeRaw
	origRestore := takebackRestore
	origPoll := takebackPoll
	origEnableISIG := takebackEnableISIG
	origEnsureForeground := takebackEnsureForeground
	origWriteLine := takebackWriteLine
	origNow := takebackNow

	defer func() {
		takebackIsTerminal = origIsTerminal
		takebackOpenTTY = origOpenTTY
		takebackMakeRaw = origMakeRaw
		takebackRestore = origRestore
		takebackPoll = origPoll
		takebackEnableISIG = origEnableISIG
		takebackEnsureForeground = origEnsureForeground
		takebackWriteLine = origWriteLine
		takebackNow = origNow
	}()

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = writeEnd.Close() }()

	takebackIsTerminal = func(int) bool { return true }
	takebackOpenTTY = func() (*os.File, error) { return readEnd, nil }
	takebackMakeRaw = func(int) (*term.State, error) { return new(term.State), nil }
	takebackRestore = func(int, *term.State) error { return nil }
	takebackPoll = func([]unix.PollFd, int) (int, error) { return 1, nil }
	takebackEnableISIG = func(int) {}
	takebackEnsureForeground = func() {}
	takebackWriteLine = func(string) {}
	takebackNow = func() time.Time { return time.Unix(0, 0) }

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentCodex))

	emitCh := make(chan framework.Input, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rt.startDesktopTakebackWatcher(ctx, func(in framework.Input) {
		select {
		case emitCh <- in:
		default:
		}
	})

	// Ctrl+C byte should trigger a shutdown command.
	_, _ = writeEnd.Write([]byte{takebackCtrlCByte})

	select {
	case got := <-emitCh:
		if _, ok := got.(cmdShutdown); !ok {
			t.Fatalf("got=%T, want cmdShutdown", got)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for shutdown command: %v", ctx.Err())
	}
}
