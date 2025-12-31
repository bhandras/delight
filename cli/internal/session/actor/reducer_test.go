package actor

import (
	"testing"

	"github.com/bhandras/delight/cli/internal/actor"
)

func TestReduceSwitchMode_EmitsStartEffects(t *testing.T) {
	t.Parallel()

	initial := State{FSM: StateLocalRunning, Mode: ModeLocal}
	reply := make(chan error, 1)

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeRemote, Reply: reply})
	if next.FSM != StateRemoteStarting || next.Mode != ModeRemote {
		t.Fatalf("state=%v/%v, want %v/%v", next.FSM, next.Mode, StateRemoteStarting, ModeRemote)
	}
	if next.RunnerGen != 1 {
		t.Fatalf("RunnerGen=%d, want 1", next.RunnerGen)
	}
	if len(effects) == 0 {
		t.Fatalf("expected effects")
	}
	// Sanity: expect a start-remote effect.
	found := false
	for _, eff := range effects {
		if _, ok := eff.(effStartRemoteRunner); ok {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected effStartRemoteRunner in effects: %+v", effects)
	}
}

func TestReduceRunnerReady_CompletesSwitchReply(t *testing.T) {
	t.Parallel()

	reply := make(chan error, 1)
	state := State{
		FSM:               StateRemoteStarting,
		Mode:              ModeRemote,
		RunnerGen:         7,
		PendingSwitchReply: reply,
	}

	next, effects := Reduce(state, evRunnerReady{Gen: 7, Mode: ModeRemote})
	_ = effects

	if next.FSM != StateRemoteRunning {
		t.Fatalf("FSM=%v, want %v", next.FSM, StateRemoteRunning)
	}
	select {
	case err := <-reply:
		if err != nil {
			t.Fatalf("reply err=%v, want nil", err)
		}
	default:
		t.Fatalf("expected reply to be completed")
	}
}

func TestReduceRemoteSend_GatedToRemoteRunning(t *testing.T) {
	t.Parallel()

	state := State{FSM: StateLocalRunning, Mode: ModeLocal, RunnerGen: 1}
	reply := make(chan error, 1)

	next, effects := Reduce(state, cmdRemoteSend{Text: "hi", Reply: reply})
	if next.FSM != StateLocalRunning {
		t.Fatalf("FSM=%v, want unchanged", next.FSM)
	}
	if len(effects) != 0 {
		t.Fatalf("effects=%d, want 0", len(effects))
	}
	select {
	case err := <-reply:
		if err == nil {
			t.Fatalf("expected error")
		}
	default:
		t.Fatalf("expected reply")
	}
}

func TestStepHelper(t *testing.T) {
	t.Parallel()

	reducer := func(s int, in actor.Input) (int, []actor.Effect) { return s + 1, nil }
	next, _ := actor.Step(41, testEvent{n: 1}, func(state int, input actor.Input) (int, []actor.Effect) {
		return reducer(state, input)
	})
	if next != 42 {
		t.Fatalf("next=%d, want 42", next)
	}
}

type testEvent struct {
	actor.InputBase
	n int
}

