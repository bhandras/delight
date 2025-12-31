package actor

import (
	"encoding/json"
	"testing"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
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

func TestReduceAgentState_VersionMismatchRetriesOnce(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:                 StateRemoteStarting,
		Mode:                ModeRemote,
		RunnerGen:            1,
		AgentStateJSON:       `{"controlledByUser":false}`,
		AgentStateVersion:    1,
		PersistRetryRemaining: 1,
	}

	next, effects := Reduce(state, EvAgentStateVersionMismatch{ServerVersion: 5})
	if next.AgentStateVersion != 5 {
		t.Fatalf("AgentStateVersion=%d, want 5", next.AgentStateVersion)
	}
	if next.PersistRetryRemaining != 0 {
		t.Fatalf("PersistRetryRemaining=%d, want 0", next.PersistRetryRemaining)
	}
	if len(effects) != 1 {
		t.Fatalf("effects=%d, want 1", len(effects))
	}
	if eff, ok := effects[0].(effPersistAgentState); !ok {
		t.Fatalf("effect type=%T, want effPersistAgentState", effects[0])
	} else if eff.ExpectedVersion != 5 {
		t.Fatalf("ExpectedVersion=%d, want 5", eff.ExpectedVersion)
	}
}

func TestReducePersistAgentState_EmitsPersistEffect(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:              StateLocalRunning,
		Mode:             ModeLocal,
		AgentStateVersion: 3,
	}

	next, effects := Reduce(state, cmdPersistAgentState{AgentStateJSON: `{"controlledByUser":true}`})
	if next.AgentStateJSON == "" {
		t.Fatalf("AgentStateJSON empty")
	}
	if next.PersistRetryRemaining != 1 {
		t.Fatalf("PersistRetryRemaining=%d, want 1", next.PersistRetryRemaining)
	}
	foundCancel := false
	foundStart := false
	for _, eff := range effects {
		switch eff.(type) {
		case effCancelTimer:
			foundCancel = true
		case effStartTimer:
			foundStart = true
		}
	}
	if !foundCancel || !foundStart {
		t.Fatalf("expected debounce timer effects, got: %+v", effects)
	}
}

func TestReduceSwitchMode_LocalToRemote_EffectSequence(t *testing.T) {
	t.Parallel()

	initial := State{
		FSM:  StateLocalRunning,
		Mode: ModeLocal,
		AgentState: types.AgentState{
			ControlledByUser: true,
		},
	}
	reply := make(chan error, 1)

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeRemote, Reply: reply})
	if next.FSM != StateRemoteStarting || next.Mode != ModeRemote {
		t.Fatalf("state=%v/%v, want %v/%v", next.FSM, next.Mode, StateRemoteStarting, ModeRemote)
	}
	if next.AgentState.ControlledByUser {
		t.Fatalf("ControlledByUser=%v, want false", next.AgentState.ControlledByUser)
	}

	var stopLocal effStopLocalRunner
	var startRemote effStartRemoteRunner
	var foundStop, foundStart bool
	for _, eff := range effects {
		switch v := eff.(type) {
		case effStopLocalRunner:
			stopLocal = v
			foundStop = true
		case effStartRemoteRunner:
			startRemote = v
			foundStart = true
		}
	}
	if !foundStop || !foundStart {
		t.Fatalf("expected stop/start effects, got: %+v", effects)
	}
	if stopLocal.Gen != 0 {
		t.Fatalf("stopLocal.Gen=%d, want 0", stopLocal.Gen)
	}
	if startRemote.Gen != 1 {
		t.Fatalf("startRemote.Gen=%d, want 1", startRemote.Gen)
	}
}

func TestReduceSwitchMode_RemoteToLocal_EffectSequence(t *testing.T) {
	t.Parallel()

	initial := State{
		FSM:  StateRemoteRunning,
		Mode: ModeRemote,
		RunnerGen: 3,
		AgentState: types.AgentState{
			ControlledByUser: false,
		},
	}
	reply := make(chan error, 1)

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeLocal, Reply: reply})
	if next.FSM != StateLocalStarting || next.Mode != ModeLocal {
		t.Fatalf("state=%v/%v, want %v/%v", next.FSM, next.Mode, StateLocalStarting, ModeLocal)
	}
	if !next.AgentState.ControlledByUser {
		t.Fatalf("ControlledByUser=%v, want true", next.AgentState.ControlledByUser)
	}

	var stopRemote effStopRemoteRunner
	var startLocal effStartLocalRunner
	var foundStop, foundStart bool
	for _, eff := range effects {
		switch v := eff.(type) {
		case effStopRemoteRunner:
			stopRemote = v
			foundStop = true
		case effStartLocalRunner:
			startLocal = v
			foundStart = true
		}
	}
	if !foundStop || !foundStart {
		t.Fatalf("expected stop/start effects, got: %+v", effects)
	}
	if stopRemote.Gen != 3 {
		t.Fatalf("stopRemote.Gen=%d, want 3", stopRemote.Gen)
	}
	if startLocal.Gen != 4 {
		t.Fatalf("startLocal.Gen=%d, want 4", startLocal.Gen)
	}
}

func TestReducePermissionRequested_AddsDurableRequest(t *testing.T) {
	t.Parallel()

	state := State{
		SessionID: "s1",
		FSM:       StateRemoteRunning,
		Mode:      ModeRemote,
		AgentState: types.AgentState{
			ControlledByUser: false,
			Requests:         map[string]types.AgentPendingRequest{},
		},
	}

	input := json.RawMessage(`{"foo":"bar"}`)
	next, effects := Reduce(state, evPermissionRequested{
		RequestID: "r1",
		ToolName:  "can_use_tool",
		Input:     input,
		NowMs:     123,
	})

	if _, ok := next.AgentState.Requests["r1"]; !ok {
		t.Fatalf("expected request to be stored")
	}
	foundEphemeral := false
	for _, eff := range effects {
		if _, ok := eff.(effEmitEphemeral); ok {
			foundEphemeral = true
		}
	}
	if !foundEphemeral {
		t.Fatalf("expected effEmitEphemeral, got: %+v", effects)
	}
}

func TestReducePermissionDecision_RemovesDurableAndEmitsDecisionEffect(t *testing.T) {
	t.Parallel()

	state := State{
		SessionID: "s1",
		FSM:       StateRemoteRunning,
		Mode:      ModeRemote,
		RunnerGen: 9,
		AgentState: types.AgentState{
			ControlledByUser: false,
			Requests: map[string]types.AgentPendingRequest{
				"r1": {ToolName: "tool", Input: "{}", CreatedAt: 1},
			},
		},
	}
	reply := make(chan error, 1)

	next, effects := Reduce(state, cmdPermissionDecision{
		RequestID: "r1",
		Allow:     true,
		Message:   "ok",
		NowMs:     456,
		Reply:     reply,
	})
	if _, ok := next.AgentState.Requests["r1"]; ok {
		t.Fatalf("expected request to be removed")
	}
	if _, ok := next.AgentState.CompletedRequests["r1"]; !ok {
		t.Fatalf("expected completed request to be stored")
	}
	foundDecision := false
	for _, eff := range effects {
		if d, ok := eff.(effRemotePermissionDecision); ok {
			foundDecision = true
			if d.Gen != 9 {
				t.Fatalf("decision gen=%d, want 9", d.Gen)
			}
		}
	}
	if !foundDecision {
		t.Fatalf("expected effRemotePermissionDecision, got: %+v", effects)
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

func TestReduceDebouncedPersistence_CoalescesToLatest(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:              StateLocalRunning,
		Mode:             ModeLocal,
		AgentStateVersion: 7,
	}

	state, _ = Reduce(state, cmdPersistAgentState{AgentStateJSON: `{"a":1}`})
	state, _ = Reduce(state, cmdPersistAgentState{AgentStateJSON: `{"a":2}`})

	if state.AgentStateJSON != `{"a":2}` {
		t.Fatalf("AgentStateJSON=%s, want latest", state.AgentStateJSON)
	}

	next, effects := Reduce(state, evTimerFired{Name: persistDebounceTimerName, NowMs: 1})
	_ = next
	foundPersist := false
	for _, eff := range effects {
		if p, ok := eff.(effPersistAgentState); ok {
			foundPersist = true
			if p.AgentStateJSON != `{"a":2}` {
				t.Fatalf("persist json=%s, want latest", p.AgentStateJSON)
			}
			if p.ExpectedVersion != 7 {
				t.Fatalf("ExpectedVersion=%d, want 7", p.ExpectedVersion)
			}
		}
	}
	if !foundPersist {
		t.Fatalf("expected effPersistAgentState on timer fired, got: %+v", effects)
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
