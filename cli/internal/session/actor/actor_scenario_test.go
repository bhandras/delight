package actor

import (
	"context"
	"sync"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
)

// scenarioRuntime is a deterministic runtime for scenario tests.
//
// It simulates runner lifecycle by immediately emitting RunnerReady for start
// effects, and records send effects for assertions.
type scenarioRuntime struct {
	mu sync.Mutex

	startedRemote []int64
	startedLocal  []int64
	sentRemote    []string
	aborts        int
}

func (r *scenarioRuntime) HandleEffects(ctx context.Context, effects []framework.Effect, emit func(framework.Input)) {
	for _, eff := range effects {
		switch e := eff.(type) {
		case effStartRemoteRunner:
			r.mu.Lock()
			r.startedRemote = append(r.startedRemote, e.Gen)
			r.mu.Unlock()
			emit(evRunnerReady{Gen: e.Gen, Mode: ModeRemote})
		case effStartLocalRunner:
			r.mu.Lock()
			r.startedLocal = append(r.startedLocal, e.Gen)
			r.mu.Unlock()
			emit(evRunnerReady{Gen: e.Gen, Mode: ModeLocal})
		case effRemoteSend:
			r.mu.Lock()
			r.sentRemote = append(r.sentRemote, e.Text)
			r.mu.Unlock()
		case effRemoteAbort:
			r.mu.Lock()
			r.aborts++
			r.mu.Unlock()
		}
	}
}

func (r *scenarioRuntime) Stop() {}

func TestActorScenario_SwitchLoopDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	rt := &scenarioRuntime{}
	initial := State{FSM: StateLocalRunning, Mode: ModeLocal}
	a := framework.New[State](initial, Reduce, rt)
	a.Start()
	defer a.Stop()

	for i := 0; i < 50; i++ {
		toRemote := make(chan error, 1)
		if ok := a.Enqueue(cmdSwitchMode{Target: ModeRemote, Reply: toRemote}); !ok {
			t.Fatalf("enqueue switch to remote failed at iter %d", i)
		}
		select {
		case err := <-toRemote:
			if err != nil {
				t.Fatalf("switch remote err=%v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout switching to remote at iter %d", i)
		}

		toLocal := make(chan error, 1)
		if ok := a.Enqueue(cmdSwitchMode{Target: ModeLocal, Reply: toLocal}); !ok {
			t.Fatalf("enqueue switch to local failed at iter %d", i)
		}
		select {
		case err := <-toLocal:
			if err != nil {
				t.Fatalf("switch local err=%v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout switching to local at iter %d", i)
		}
	}

	final := a.State()
	if final.Mode != ModeLocal || final.FSM != StateLocalRunning {
		t.Fatalf("final=%v/%v, want %v/%v", final.FSM, final.Mode, StateLocalRunning, ModeLocal)
	}
}

func TestActorScenario_StaleRunnerExitIgnored(t *testing.T) {
	t.Parallel()

	rt := &scenarioRuntime{}
	initial := State{FSM: StateLocalRunning, Mode: ModeLocal}
	a := framework.New[State](initial, Reduce, rt)
	a.Start()
	defer a.Stop()

	toRemote := make(chan error, 1)
	_ = a.Enqueue(cmdSwitchMode{Target: ModeRemote, Reply: toRemote})
	select {
	case err := <-toRemote:
		if err != nil {
			t.Fatalf("switch remote err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout switching to remote")
	}

	// Inject a stale exit for gen 0; should have no effect.
	_ = a.Enqueue(evRunnerExited{Gen: 0, Mode: ModeRemote, Err: context.Canceled})
	time.Sleep(20 * time.Millisecond)

	if got := a.State().FSM; got != StateRemoteRunning {
		t.Fatalf("FSM=%v, want %v", got, StateRemoteRunning)
	}
}

func TestActorScenario_RemoteSendRecorded(t *testing.T) {
	t.Parallel()

	rt := &scenarioRuntime{}
	initial := State{FSM: StateLocalRunning, Mode: ModeLocal}
	a := framework.New[State](initial, Reduce, rt)
	a.Start()
	defer a.Stop()

	toRemote := make(chan error, 1)
	_ = a.Enqueue(cmdSwitchMode{Target: ModeRemote, Reply: toRemote})
	select {
	case <-toRemote:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout switching to remote")
	}

	reply := make(chan error, 1)
	_ = a.Enqueue(cmdRemoteSend{Text: "Hello", Reply: reply})
	select {
	case err := <-reply:
		// Phase 2 reducer doesn't complete send replies on success yet.
		_ = err
	case <-time.After(200 * time.Millisecond):
		// ok
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		rt.mu.Lock()
		sent := append([]string(nil), rt.sentRemote...)
		rt.mu.Unlock()
		if len(sent) == 1 && sent[0] == "Hello" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sent=%v, want [Hello]", sent)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
