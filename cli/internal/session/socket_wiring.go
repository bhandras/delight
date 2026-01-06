package session

import (
	framework "github.com/bhandras/delight/cli/internal/actor"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
)

// socketLifecycle is the subset of socket clients used for lifecycle wiring.
type socketLifecycle interface {
	OnConnect(fn func())
	OnDisconnect(fn func(reason string))
}

// actorEnqueuer is the minimal Actor API we need to enqueue lifecycle events.
type actorEnqueuer interface {
	Enqueue(input framework.Input) bool
}

// wireSessionActorToSocket registers socket lifecycle callbacks that enqueue
// the corresponding session actor inputs.
func wireSessionActorToSocket(a actorEnqueuer, sock socketLifecycle) {
	if a == nil || sock == nil {
		return
	}

	sock.OnConnect(func() {
		_ = a.Enqueue(sessionactor.WSConnected())
	})
	sock.OnDisconnect(func(reason string) {
		_ = a.Enqueue(sessionactor.WSDisconnected(reason))
	})
}

// wireSessionActorToTerminalSocket registers terminal socket lifecycle callbacks
// that enqueue the corresponding session actor inputs.
func wireSessionActorToTerminalSocket(a actorEnqueuer, sock socketLifecycle) {
	if a == nil || sock == nil {
		return
	}

	sock.OnConnect(func() {
		_ = a.Enqueue(sessionactor.TerminalConnected())
	})
	sock.OnDisconnect(func(reason string) {
		_ = a.Enqueue(sessionactor.TerminalDisconnected(reason))
	})
}
