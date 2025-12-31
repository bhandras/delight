// Package actortest provides test helpers for the actor framework.
package actortest

import (
	"context"
	"sync"

	"github.com/bhandras/delight/cli/internal/actor"
)

// FakeRuntime is a Runtime implementation for unit tests.
//
// It records effects passed to HandleEffects and optionally can be configured to
// synchronously emit follow-up inputs.
type FakeRuntime struct {
	mu sync.Mutex

	effects []actor.Effect

	// EmitFn, when non-nil, is invoked for each effect during HandleEffects.
	// Tests can use it to synthesize follow-up events.
	EmitFn func(ctx context.Context, eff actor.Effect, emit func(actor.Input))
}

// HandleEffects implements actor.Runtime.
func (r *FakeRuntime) HandleEffects(ctx context.Context, effects []actor.Effect, emit func(actor.Input)) {
	r.mu.Lock()
	r.effects = append(r.effects, effects...)
	emitFn := r.EmitFn
	r.mu.Unlock()

	if emitFn != nil {
		for _, eff := range effects {
			emitFn(ctx, eff, emit)
		}
	}
}

// Stop implements actor.Runtime.
func (r *FakeRuntime) Stop() {}

// Effects returns a snapshot of recorded effects.
func (r *FakeRuntime) Effects() []actor.Effect {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]actor.Effect, len(r.effects))
	copy(out, r.effects)
	return out
}

// Reset clears recorded effects.
func (r *FakeRuntime) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.effects = nil
}

