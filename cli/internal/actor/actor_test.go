package actor_test

import (
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/actor/actortest"
)

type testEvent struct {
	actor.InputBase
	n int
}

type testEffect struct {
	actor.EffectBase
	n int
}

func TestActorProcessesInputsSequentially(t *testing.T) {
	t.Parallel()

	rt := &actortest.FakeRuntime{}

	reducer := func(state int, input actor.Input) (int, []actor.Effect) {
		ev, ok := input.(testEvent)
		if !ok {
			return state, nil
		}
		next := state + ev.n
		return next, []actor.Effect{testEffect{n: ev.n}}
	}

	a := actor.New[int](0, reducer, rt)
	a.Start()
	defer a.Stop()

	for i := 1; i <= 5; i++ {
		if !a.Enqueue(testEvent{n: i}) {
			t.Fatalf("failed to enqueue %d", i)
		}
	}

	// Poll for state convergence (actor is async).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.State() == 15 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.State() != 15 {
		t.Fatalf("state=%d, want 15", a.State())
	}

	effects := rt.Effects()
	if len(effects) != 5 {
		t.Fatalf("effects=%d, want 5", len(effects))
	}
}
