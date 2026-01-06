package actor

import (
	"encoding/json"
	"testing"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestReduceSwitchMode_EmitsStartEffects(t *testing.T) {
	t.Parallel()

	initial := State{FSM: StateLocalRunning, Mode: ModeLocal}
	reply := make(chan error, 1)

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeRemote, Reply: reply})
	require.Equal(t, StateRemoteStarting, next.FSM)
	require.Equal(t, ModeRemote, next.Mode)
	require.Equal(t, int64(1), next.RunnerGen)
	require.NotEmpty(t, effects)
	// Sanity: expect a start-remote effect.
	found := false
	for _, eff := range effects {
		if _, ok := eff.(effStartRemoteRunner); ok {
			found = true
		}
	}
	require.True(t, found, "expected effStartRemoteRunner in effects: %+v", effects)
}

func TestReduceRunnerReady_CompletesSwitchReply(t *testing.T) {
	t.Parallel()

	reply := make(chan error, 1)
	state := State{
		FSM:                StateRemoteStarting,
		Mode:               ModeRemote,
		RunnerGen:          7,
		PendingSwitchReply: reply,
	}

	next, effects := Reduce(state, evRunnerReady{Gen: 7, Mode: ModeRemote})
	require.NotEmpty(t, effects)

	require.Equal(t, StateRemoteRunning, next.FSM)
	found := false
	for _, eff := range effects {
		if done, ok := eff.(effCompleteReply); ok && done.Reply == reply {
			require.NoError(t, done.Err)
			found = true
		}
	}
	require.True(t, found, "expected effCompleteReply")
}

func TestReduceRemoteSend_GatedToRemoteRunning(t *testing.T) {
	t.Parallel()

	state := State{FSM: StateLocalRunning, Mode: ModeLocal, RunnerGen: 1}
	reply := make(chan error, 1)

	next, effects := Reduce(state, cmdRemoteSend{Text: "hi", Reply: reply})
	require.Equal(t, StateLocalRunning, next.FSM)
	require.NotEmpty(t, effects)
	found := false
	for _, eff := range effects {
		if done, ok := eff.(effCompleteReply); ok && done.Reply == reply {
			require.Error(t, done.Err)
			found = true
		}
	}
	require.True(t, found, "expected effCompleteReply")
}

func TestReduceAgentState_VersionMismatchRetriesOnce(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:                   StateRemoteStarting,
		Mode:                  ModeRemote,
		RunnerGen:             1,
		AgentStateJSON:        `{"controlledByUser":false}`,
		AgentStateVersion:     1,
		PersistRetryRemaining: 1,
	}

	next, effects := Reduce(state, EvAgentStateVersionMismatch{ServerVersion: 5})
	require.Equal(t, int64(5), next.AgentStateVersion)
	require.Equal(t, 0, next.PersistRetryRemaining)
	require.Len(t, effects, 1)
	if eff, ok := effects[0].(effPersistAgentState); !ok {
		t.Fatalf("effect type=%T, want effPersistAgentState", effects[0])
	} else if eff.ExpectedVersion != 5 {
		t.Fatalf("ExpectedVersion=%d, want 5", eff.ExpectedVersion)
	}
}

func TestReducePersistAgentState_EmitsPersistEffect(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:               StateLocalRunning,
		Mode:              ModeLocal,
		AgentStateVersion: 3,
	}

	next, effects := Reduce(state, cmdPersistAgentState{AgentStateJSON: `{"controlledByUser":true}`})
	require.NotEmpty(t, next.AgentStateJSON)
	require.Equal(t, 1, next.PersistRetryRemaining)
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
	require.True(t, foundCancel, "expected effCancelTimer, got: %+v", effects)
	require.True(t, foundStart, "expected effStartTimer, got: %+v", effects)
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
	require.Equal(t, StateRemoteStarting, next.FSM)
	require.Equal(t, ModeRemote, next.Mode)
	require.False(t, next.AgentState.ControlledByUser)

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
	require.True(t, foundStop, "expected effStopLocalRunner, got: %+v", effects)
	require.True(t, foundStart, "expected effStartRemoteRunner, got: %+v", effects)
	require.Equal(t, int64(0), stopLocal.Gen)
	require.Equal(t, int64(1), startRemote.Gen)
}

func TestReduceSwitchMode_RemoteToLocal_EffectSequence(t *testing.T) {
	t.Parallel()

	initial := State{
		FSM:       StateRemoteRunning,
		Mode:      ModeRemote,
		RunnerGen: 3,
		AgentState: types.AgentState{
			ControlledByUser: false,
		},
	}
	reply := make(chan error, 1)

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeLocal, Reply: reply})
	require.Equal(t, StateLocalStarting, next.FSM)
	require.Equal(t, ModeLocal, next.Mode)
	require.True(t, next.AgentState.ControlledByUser)

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
	require.True(t, foundStop, "expected effStopRemoteRunner, got: %+v", effects)
	require.True(t, foundStart, "expected effStartLocalRunner, got: %+v", effects)
	require.Equal(t, int64(3), stopRemote.Gen)
	require.Equal(t, int64(4), startLocal.Gen)
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

	_, ok := next.AgentState.Requests["r1"]
	require.True(t, ok)
	foundEphemeral := false
	for _, eff := range effects {
		if _, ok := eff.(effEmitEphemeral); ok {
			foundEphemeral = true
		}
	}
	require.True(t, foundEphemeral, "expected effEmitEphemeral, got: %+v", effects)
}

func TestReducePermissionDecision_RemovesDurableAndPersists(t *testing.T) {
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
	_, ok := next.AgentState.Requests["r1"]
	require.False(t, ok)
	_, ok = next.AgentState.CompletedRequests["r1"]
	require.True(t, ok)
	require.NotEmpty(t, effects, "expected persistence-related effects")
	found := false
	for _, eff := range effects {
		if done, ok := eff.(effCompleteReply); ok && done.Reply == reply {
			require.NoError(t, done.Err)
			found = true
		}
	}
	require.True(t, found, "expected effCompleteReply")
}

func TestReducePermissionAwait_StoresPromiseAndEmitsEphemeral(t *testing.T) {
	t.Parallel()

	decisionCh := make(chan PermissionDecision, 1)
	ack := make(chan struct{}, 1)
	state := State{
		SessionID: "s1",
		FSM:       StateLocalRunning,
		Mode:      ModeLocal,
		AgentState: types.AgentState{
			ControlledByUser: true,
			Requests:         map[string]types.AgentPendingRequest{},
		},
	}

	next, effects := Reduce(state, cmdPermissionAwait{
		RequestID: "r1",
		ToolName:  "acp.await",
		Input:     json.RawMessage(`{"foo":"bar"}`),
		NowMs:     100,
		Reply:     decisionCh,
		Ack:       ack,
	})

	_, ok := next.AgentState.Requests["r1"]
	require.True(t, ok)
	require.NotNil(t, next.PendingPermissionPromises)
	require.NotNil(t, next.PendingPermissionPromises["r1"])

	select {
	case <-ack:
	default:
		require.Fail(t, "expected ack to be signaled")
	}

	foundEphemeral := false
	for _, eff := range effects {
		if _, ok := eff.(effEmitEphemeral); ok {
			foundEphemeral = true
		}
	}
	require.True(t, foundEphemeral, "expected effEmitEphemeral, got: %+v", effects)
}

func TestReducePermissionAwait_DedupesEphemeral(t *testing.T) {
	t.Parallel()

	decisionCh := make(chan PermissionDecision, 1)
	state := State{
		SessionID: "s1",
		FSM:       StateLocalRunning,
		Mode:      ModeLocal,
		AgentState: types.AgentState{
			ControlledByUser: true,
			Requests: map[string]types.AgentPendingRequest{
				"r1": {ToolName: "acp.await", Input: "{}", CreatedAt: 1},
			},
		},
	}

	next, effects := Reduce(state, cmdPermissionAwait{
		RequestID: "r1",
		ToolName:  "acp.await",
		Input:     json.RawMessage(`{"foo":"bar"}`),
		NowMs:     100,
		Reply:     decisionCh,
	})
	require.NotNil(t, next.PendingPermissionPromises)
	require.NotNil(t, next.PendingPermissionPromises["r1"])

	// Even for duplicates, we re-emit the permission-request ephemeral so the
	// phone UI can recover if it missed the original prompt.
	foundEphemeral := false
	for _, eff := range effects {
		if _, ok := eff.(effEmitEphemeral); ok {
			foundEphemeral = true
		}
	}
	require.True(t, foundEphemeral, "expected effEmitEphemeral for duplicate request")
}

func TestReduceWSConnected_SchedulesPersist(t *testing.T) {
	t.Parallel()

	state := State{
		SessionID:   "s1",
		FSM:         StateRemoteRunning,
		Mode:        ModeRemote,
		WSConnected: false,
		AgentState: types.AgentState{
			ControlledByUser: false,
			Requests: map[string]types.AgentPendingRequest{
				"r1": {ToolName: "CodexApplyPatch", Input: "{}", CreatedAt: 123},
			},
		},
	}
	state = refreshAgentStateJSON(state)

	next, effects := Reduce(state, evWSConnected{})
	require.True(t, next.WSConnected)

	foundTimer := false
	for _, eff := range effects {
		if _, ok := eff.(effStartTimer); ok {
			foundTimer = true
		}
	}
	require.True(t, foundTimer, "expected debounced persist timer, got: %+v", effects)
}

func TestReducePermissionDecision_CompletesAwaitPromiseWithoutRemoteEffect(t *testing.T) {
	t.Parallel()

	decisionCh := make(chan PermissionDecision, 1)
	state := State{
		SessionID: "s1",
		FSM:       StateLocalRunning,
		Mode:      ModeLocal,
		AgentState: types.AgentState{
			ControlledByUser: true,
			Requests: map[string]types.AgentPendingRequest{
				"r1": {ToolName: "acp.await", Input: `{"foo":"bar"}`, CreatedAt: 1},
			},
		},
		PendingPermissionPromises: map[string]chan PermissionDecision{
			"r1": decisionCh,
		},
	}

	next, effects := Reduce(state, cmdPermissionDecision{
		RequestID: "r1",
		Allow:     true,
		Message:   "approved",
		NowMs:     200,
	})

	select {
	case got := <-decisionCh:
		require.True(t, got.Allow)
		require.Equal(t, "approved", got.Message)
	default:
		require.Fail(t, "expected decision to be delivered to promise channel")
	}

	if next.PendingPermissionPromises != nil {
		if _, ok := next.PendingPermissionPromises["r1"]; ok {
			require.Fail(t, "expected promise to be removed after decision")
		}
	}

	_ = effects
}

func TestReduceSetControlledByUser_SchedulesPersist(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:  StateClosed,
		Mode: ModeLocal,
		AgentState: types.AgentState{
			ControlledByUser: true,
		},
	}

	next, effects := Reduce(state, cmdSetControlledByUser{ControlledByUser: false, NowMs: 1})
	require.False(t, next.AgentState.ControlledByUser)
	require.NotEmpty(t, effects)
	foundTimer := false
	for _, eff := range effects {
		if _, ok := eff.(effStartTimer); ok {
			foundTimer = true
		}
	}
	require.True(t, foundTimer, "expected effStartTimer, got: %+v", effects)
}

func TestReduceRunnerReady_RemoteStartsTakebackWatcher(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:       StateRemoteStarting,
		Mode:      ModeRemote,
		RunnerGen: 3,
	}

	_, effects := Reduce(state, evRunnerReady{Gen: 3, Mode: ModeRemote})
	found := false
	for _, eff := range effects {
		if _, ok := eff.(effStartDesktopTakebackWatcher); ok {
			found = true
		}
	}
	require.True(t, found, "expected effStartDesktopTakebackWatcher, got: %+v", effects)
}

func TestReduceInboundUserMessage_LocalRunning_SwitchesAndBuffers(t *testing.T) {
	t.Parallel()

	nowMs := int64(1_000)
	state := State{
		FSM:       StateLocalRunning,
		Mode:      ModeLocal,
		RunnerGen: 10,
		AgentState: types.AgentState{
			ControlledByUser: true,
		},
	}

	next, effects := Reduce(state, cmdInboundUserMessage{
		Text:    "hello from phone",
		Meta:    map[string]any{"origin": "mobile"},
		LocalID: "l1",
		NowMs:   nowMs,
	})

	require.Equal(t, StateRemoteStarting, next.FSM)
	require.Equal(t, ModeRemote, next.Mode)
	require.False(t, next.AgentState.ControlledByUser)
	require.Len(t, next.PendingRemoteSends, 1)
	require.Equal(t, "hello from phone", next.PendingRemoteSends[0].text)
	require.Equal(t, "l1", next.PendingRemoteSends[0].localID)
	require.Equal(t, nowMs, next.PendingRemoteSends[0].nowMs)

	var foundStopLocal, foundStartRemote bool
	for _, eff := range effects {
		switch eff.(type) {
		case effStopLocalRunner:
			foundStopLocal = true
		case effStartRemoteRunner:
			foundStartRemote = true
		}
	}
	require.True(t, foundStopLocal, "expected effStopLocalRunner in effects: %+v", effects)
	require.True(t, foundStartRemote, "expected effStartRemoteRunner in effects: %+v", effects)
}

func TestReduceRunnerReady_RemoteStarting_FlushesPendingRemoteSends(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:       StateRemoteStarting,
		Mode:      ModeRemote,
		RunnerGen: 5,
		PendingRemoteSends: []pendingRemoteSend{
			{text: "m1", meta: map[string]any{"a": 1}, localID: "l1", nowMs: 10},
			{text: "m2", meta: map[string]any{"a": 2}, localID: "l2", nowMs: 20},
		},
	}

	next, effects := Reduce(state, evRunnerReady{Gen: 5, Mode: ModeRemote})
	require.Equal(t, StateRemoteRunning, next.FSM)
	require.Empty(t, next.PendingRemoteSends)

	var got []effRemoteSend
	for _, eff := range effects {
		if s, ok := eff.(effRemoteSend); ok {
			got = append(got, s)
		}
	}
	require.Len(t, got, 2)
	require.Equal(t, int64(5), got[0].Gen)
	require.Equal(t, "m1", got[0].Text)
	require.Equal(t, "l1", got[0].LocalID)
	require.Equal(t, int64(5), got[1].Gen)
	require.Equal(t, "m2", got[1].Text)
	require.Equal(t, "l2", got[1].LocalID)
}

func TestReduceRunnerExited_RemoteStarting_WithPending_FallsBackToLocalAndReclaimsControl(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:       StateRemoteStarting,
		Mode:      ModeRemote,
		RunnerGen: 7,
		AgentState: types.AgentState{
			ControlledByUser: false,
		},
		PendingRemoteSends: []pendingRemoteSend{
			{text: "hello", nowMs: 123},
		},
	}

	next, effects := Reduce(state, evRunnerExited{Gen: 7, Mode: ModeRemote, Err: assertErr("boom")})
	require.Equal(t, StateLocalRunning, next.FSM)
	require.Equal(t, ModeLocal, next.Mode)
	require.True(t, next.AgentState.ControlledByUser, "expected control to revert to desktop on remote-start failure")
	require.Empty(t, next.PendingRemoteSends)

	var foundLocalSend bool
	for _, eff := range effects {
		if s, ok := eff.(effLocalSendLine); ok {
			foundLocalSend = true
			require.Equal(t, int64(7), s.Gen)
			require.Equal(t, "hello", s.Text)
		}
	}
	require.True(t, foundLocalSend, "expected effLocalSendLine fallback effects: %+v", effects)
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func TestReduceDebouncedPersistence_CoalescesToLatest(t *testing.T) {
	t.Parallel()

	state := State{
		FSM:               StateLocalRunning,
		Mode:              ModeLocal,
		AgentStateVersion: 7,
	}

	state, _ = Reduce(state, cmdPersistAgentState{AgentStateJSON: `{"a":1}`})
	state, _ = Reduce(state, cmdPersistAgentState{AgentStateJSON: `{"a":2}`})

	require.Equal(t, `{"a":2}`, state.AgentStateJSON)

	next, effects := Reduce(state, evTimerFired{Name: persistDebounceTimerName, NowMs: 1})
	_ = next
	foundPersist := false
	for _, eff := range effects {
		if p, ok := eff.(effPersistAgentState); ok {
			foundPersist = true
			require.Equal(t, `{"a":2}`, p.AgentStateJSON)
			require.Equal(t, int64(7), p.ExpectedVersion)
		}
	}
	require.True(t, foundPersist, "expected effPersistAgentState on timer fired, got: %+v", effects)
}

func TestStepHelper(t *testing.T) {
	t.Parallel()

	reducer := func(s int, _ actor.Input) (int, []actor.Effect) { return s + 1, nil }
	next, _ := actor.Step(41, testEvent{n: 1}, func(state int, input actor.Input) (int, []actor.Effect) {
		return reducer(state, input)
	})
	require.Equal(t, 42, next)
}

type testEvent struct {
	actor.InputBase
	n int
}
