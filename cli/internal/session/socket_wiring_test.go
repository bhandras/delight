package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/stretchr/testify/require"
)

// fakeSocket captures lifecycle callbacks so tests can trigger them.
type fakeSocket struct {
	onConnect    func()
	onDisconnect func(string)
}

// OnConnect stores the provided callback.
func (f *fakeSocket) OnConnect(fn func()) { f.onConnect = fn }

// OnDisconnect stores the provided callback.
func (f *fakeSocket) OnDisconnect(fn func(reason string)) { f.onDisconnect = fn }

// noopRuntime ignores effects; used for wiring tests where we only care about
// inputs being enqueued.
type noopRuntime struct{}

// HandleEffects implements actor.Runtime.
func (noopRuntime) HandleEffects(context.Context, []framework.Effect, func(framework.Input)) {}

// Stop implements actor.Runtime.
func (noopRuntime) Stop() {}

// waitForInputType blocks until the actor emits an input whose type string
// contains typeSubstr, or fails the test after a timeout.
func waitForInputType(t *testing.T, inCh <-chan framework.Input, typeSubstr string) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case in := <-inCh:
			if strings.Contains(fmt.Sprintf("%T", in), typeSubstr) {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for input type containing %q", typeSubstr)
		}
	}
}

func TestWireSessionActorToSocket_EnqueuesLifecycleEvents(t *testing.T) {
	t.Parallel()

	inCh := make(chan framework.Input, 8)
	hooks := framework.Hooks[int]{
		OnInput: func(input framework.Input) {
			select {
			case inCh <- input:
			default:
			}
		},
	}
	a := framework.New(0, func(state int, input framework.Input) (int, []framework.Effect) {
		_ = input
		return state, nil
	}, noopRuntime{}, framework.WithHooks(hooks))
	a.Start()
	defer a.Stop()

	sock := &fakeSocket{}
	wireSessionActorToSocket(a, sock)
	require.NotNil(t, sock.onConnect)
	require.NotNil(t, sock.onDisconnect)

	sock.onConnect()
	waitForInputType(t, inCh, "evWSConnected")

	sock.onDisconnect("bye")
	waitForInputType(t, inCh, "evWSDisconnected")
}

func TestWireSessionActorToTerminalSocket_EnqueuesLifecycleEvents(t *testing.T) {
	t.Parallel()

	inCh := make(chan framework.Input, 8)
	hooks := framework.Hooks[int]{
		OnInput: func(input framework.Input) {
			select {
			case inCh <- input:
			default:
			}
		},
	}
	a := framework.New(0, func(state int, input framework.Input) (int, []framework.Effect) {
		_ = input
		return state, nil
	}, noopRuntime{}, framework.WithHooks(hooks))
	a.Start()
	defer a.Stop()

	sock := &fakeSocket{}
	wireSessionActorToTerminalSocket(a, sock)
	require.NotNil(t, sock.onConnect)
	require.NotNil(t, sock.onDisconnect)

	sock.onConnect()
	waitForInputType(t, inCh, "evTerminalConnected")

	sock.onDisconnect("bye")
	waitForInputType(t, inCh, "evTerminalDisconnected")
}
