// Package actor provides a small actor-style event loop scaffold that supports
// pure state reducers and declarative side-effects.
//
// The core idea is:
//   - A single goroutine ("the actor loop") owns all mutable state.
//   - A pure reducer transforms state given an input and returns effects.
//   - A runtime interprets effects asynchronously and emits events back.
//
// This pattern enables deterministic testing of the reducer, eliminates shared
// mutable state races, and makes multi-step orchestration explicit.
package actor

import (
	"context"
	"fmt"
	"sync"
)

// Input is an item delivered to an actor mailbox.
//
// Inputs can be events (observations from the runtime) or commands (requests
// from callers). This package does not require distinct interfaces for events
// vs commands; it only requires that inputs are serializable through the actor
// loop.
type Input interface {
	isActorInput()
}

// Effect is a declarative side-effect produced by a reducer.
//
// Effects are data, not execution. The Runtime is responsible for interpreting
// effects and emitting resulting events back to the actor mailbox.
type Effect interface {
	isActorEffect()
}

// ReducerFunc is a pure state transition function.
//
// Reducers must be side-effect free:
//   - no I/O
//   - no goroutine spawning
//   - no time.Now / random IDs (inject via inputs instead)
//
// Reducers are expected to be deterministic for a given (state, input).
type ReducerFunc[S any] func(state S, input Input) (next S, effects []Effect)

// Runtime interprets effects and emits follow-up inputs back to the actor.
//
// Implementations must not mutate actor state directly. Instead, they should
// emit events back through the provided emitter.
type Runtime interface {
	// HandleEffects executes effects. It should return quickly; long-running or
	// blocking work must run asynchronously. Implementations must stop emitting
	// once the context is canceled.
	HandleEffects(ctx context.Context, effects []Effect, emit func(Input))

	// Stop requests that the runtime stop any background work. It may be called
	// multiple times.
	Stop()
}

// Hooks provide optional observability into an actor's execution.
type Hooks[S any] struct {
	// OnInput is called after an input is dequeued, before reducing.
	OnInput func(input Input)
	// OnTransition is called after reducing, when state changes are applied.
	OnTransition func(prev S, next S, input Input)
	// OnEffects is called after reducing, before effects are handed to Runtime.
	OnEffects func(effects []Effect)
	// OnPanic is called when the loop panics. If nil, panics propagate to crash.
	OnPanic func(recovered any)
}

// Actor runs a single-threaded event loop that owns state of type S.
type Actor[S any] struct {
	reduce  ReducerFunc[S]
	runtime Runtime
	hooks   Hooks[S]

	mu     sync.Mutex
	state  S
	inbox  chan Input
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// New creates a new actor with initial state, reducer, and runtime.
func New[S any](initial S, reducer ReducerFunc[S], runtime Runtime, opts ...Option[S]) *Actor[S] {
	ctx, cancel := context.WithCancel(context.Background())
	a := &Actor[S]{
		reduce:  reducer,
		runtime: runtime,
		state:   initial,
		inbox:   make(chan Input, 256),
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Option configures an Actor.
type Option[S any] func(*Actor[S])

// WithHooks attaches hooks for observability.
func WithHooks[S any](hooks Hooks[S]) Option[S] {
	return func(a *Actor[S]) { a.hooks = hooks }
}

// WithMailboxSize sets the actor mailbox buffer size.
func WithMailboxSize[S any](n int) Option[S] {
	return func(a *Actor[S]) {
		if n <= 0 {
			return
		}
		a.inbox = make(chan Input, n)
	}
}

// Start launches the actor loop in its own goroutine.
//
// Start is idempotent; calling Start multiple times has no effect.
func (a *Actor[S]) Start() {
	a.once.Do(func() { go a.loop() })
}

// Stop cancels the actor context and stops the runtime.
//
// Stop is safe to call multiple times.
func (a *Actor[S]) Stop() {
	a.cancel()
	if a.runtime != nil {
		a.runtime.Stop()
	}
}

// Done returns a channel that closes when the actor loop exits.
func (a *Actor[S]) Done() <-chan struct{} { return a.done }

// Enqueue delivers an input to the actor mailbox.
//
// If the actor is stopped, Enqueue returns false.
func (a *Actor[S]) Enqueue(input Input) bool {
	if input == nil {
		return false
	}
	select {
	case <-a.ctx.Done():
		return false
	default:
	}
	select {
	case a.inbox <- input:
		return true
	default:
		// Best-effort: drop if mailbox is full. Callers that need backpressure
		// should use a larger mailbox or explicit flow control.
		return false
	}
}

// State returns a snapshot of the current actor state.
//
// This is intended for observability/testing. Production code should prefer to
// derive behavior from reducer outputs rather than reading state concurrently.
func (a *Actor[S]) State() S {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// loop runs the actor event loop.
func (a *Actor[S]) loop() {
	defer close(a.done)
	defer func() {
		if r := recover(); r != nil {
			if a.hooks.OnPanic != nil {
				a.hooks.OnPanic(r)
				return
			}
			panic(r)
		}
	}()

	emit := func(in Input) {
		_ = a.Enqueue(in)
	}

	for {
		select {
		case <-a.ctx.Done():
			return
		case in := <-a.inbox:
			if in == nil {
				continue
			}
			if a.hooks.OnInput != nil {
				a.hooks.OnInput(in)
			}

			a.mu.Lock()
			prev := a.state
			a.mu.Unlock()

			next, effects := a.reduce(prev, in)

			a.mu.Lock()
			a.state = next
			a.mu.Unlock()

			if a.hooks.OnTransition != nil {
				a.hooks.OnTransition(prev, next, in)
			}
			if len(effects) > 0 && a.hooks.OnEffects != nil {
				a.hooks.OnEffects(effects)
			}

			if a.runtime != nil && len(effects) > 0 {
				a.runtime.HandleEffects(a.ctx, effects, emit)
			}
		}
	}
}

// ErrStopped is returned by helpers when the actor has been stopped.
var ErrStopped = fmt.Errorf("actor stopped")

