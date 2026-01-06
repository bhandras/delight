package session

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManager_ScheduleShutdown_ExitsOnce(t *testing.T) {
	// Do not run in parallel: mutates package-level test seams.

	origSleep := managerSleep
	origExit := managerExit
	defer func() {
		managerSleep = origSleep
		managerExit = origExit
	}()

	managerSleep = func(time.Duration) {}

	var exitCalls atomic.Int32
	exitCh := make(chan int, 10)
	managerExit = func(code int) {
		exitCalls.Add(1)
		select {
		case exitCh <- code:
		default:
		}
	}

	m := &Manager{stopCh: make(chan struct{})}

	m.scheduleShutdown()
	m.scheduleShutdown()

	select {
	case code := <-exitCh:
		require.Equal(t, 0, code)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for exit")
	}

	// Give any unexpected additional goroutines a moment to run.
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), exitCalls.Load())
}

func TestManager_ForceExitAfter_InvokesExit(t *testing.T) {
	// Do not run in parallel: mutates package-level test seams.

	origSleep := managerSleep
	origExit := managerExit
	defer func() {
		managerSleep = origSleep
		managerExit = origExit
	}()

	managerSleep = func(time.Duration) {}

	exitCh := make(chan int, 1)
	managerExit = func(code int) {
		select {
		case exitCh <- code:
		default:
		}
	}

	m := &Manager{}
	m.forceExitAfter(5 * time.Second)

	select {
	case code := <-exitCh:
		require.Equal(t, 0, code)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for exit")
	}
}
