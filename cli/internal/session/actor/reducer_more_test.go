package actor

import (
	"errors"
	"testing"

	"github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

// TestReduceOutboundMessageReady_SuppressesRecentlyInjectedInput ensures that
// user messages that originated from remote/mobile injection are not echoed
// back to the server.
func TestReduceOutboundMessageReady_SuppressesRecentlyInjectedInput(t *testing.T) {
	t.Parallel()

	state := State{
		SessionID: "s1",
		RunnerGen: 1,
		RecentRemoteInputs: []remoteInputRecord{{
			text: "hello",
			atMs: 100,
		}},
	}

	next, effects := Reduce(state, evOutboundMessageReady{
		Gen:                1,
		Ciphertext:         "ct",
		UserTextNormalized: "hello",
		NowMs:              110,
	})

	require.Equal(t, state.RunnerGen, next.RunnerGen)
	require.Empty(t, effects)
}

// TestReducePersistAgentStateImmediate_EmitsPersistEffect exercises the
// immediate persist path used by UI "Apply" actions.
func TestReducePersistAgentStateImmediate_EmitsPersistEffect(t *testing.T) {
	t.Parallel()

	state := State{AgentStateVersion: 7}
	next, effects := Reduce(state, cmdPersistAgentStateImmediate{AgentStateJSON: `{"x":1}`})

	require.Equal(t, `{"x":1}`, next.AgentStateJSON)
	require.True(t, next.PersistInFlight)
	require.False(t, next.PersistDebounceTimerArmed)

	require.Len(t, effects, 2)
	_, okCancel := effects[0].(effCancelTimer)
	_, okPersist := effects[1].(effPersistAgentState)
	require.True(t, okCancel)
	require.True(t, okPersist)
}

// TestReduceWaitForAgentStatePersist_NotifiesWaiter ensures waiters are held
// until persistence completes and are then notified.
func TestReduceWaitForAgentStatePersist_NotifiesWaiter(t *testing.T) {
	t.Parallel()

	waiter := make(chan error, 1)
	state := State{
		SessionID:         "s1",
		AgentStateJSON:    `{"controlledByUser":false}`,
		AgentStateVersion: 2,
	}

	next, effects := Reduce(state, cmdWaitForAgentStatePersist{Reply: waiter})
	require.True(t, next.PersistInFlight)
	require.Len(t, next.PersistWaiters, 1)
	require.Len(t, effects, 2)

	// Simulate a persist ack and ensure the waiter would be completed.
	next2, effects := Reduce(next, EvAgentStatePersisted{NewVersion: 3})
	require.Equal(t, int64(3), next2.AgentStateVersion)
	found := false
	for _, eff := range effects {
		if done, ok := eff.(effCompleteReply); ok && done.Reply == waiter {
			require.NoError(t, done.Err)
			found = true
		}
	}
	require.True(t, found, "expected effCompleteReply for waiter")
}

// TestReduceAgentStatePersistFailed_ArmsDebounceWhenOnline ensures that
// persistence failures re-arm a debounced retry when the websocket is online.
func TestReduceAgentStatePersistFailed_ArmsDebounceWhenOnline(t *testing.T) {
	t.Parallel()

	state := State{
		SessionID:   "s1",
		WSConnected: true,
	}

	next, effects := Reduce(state, EvAgentStatePersistFailed{Err: errors.New("boom")})
	require.True(t, next.PersistDebounceTimerArmed)

	foundStart := false
	foundCancel := false
	for _, eff := range effects {
		switch eff.(type) {
		case effStartTimer:
			foundStart = true
		case effCancelTimer:
			foundCancel = true
		}
	}
	require.True(t, foundCancel)
	require.True(t, foundStart)
}

// TestReduceEventInputs_NoOp ensures inbound observability-only event payloads
// do not mutate state in phase 1/2 of the session actor rollout.
func TestReduceEventInputs_NoOp(t *testing.T) {
	t.Parallel()

	state := State{SessionID: "s1", RunnerGen: 1}
	next, effects := Reduce(state, evSessionUpdate{Data: map[string]any{"x": 1}})
	require.Equal(t, state, next)
	require.Empty(t, effects)

	next, effects = Reduce(state, evMessageUpdate{Data: map[string]any{"y": 2}})
	require.Equal(t, state, next)
	require.Empty(t, effects)

	next, effects = Reduce(state, evEphemeral{Data: map[string]any{"z": 3}})
	require.Equal(t, state, next)
	require.Empty(t, effects)
}

// TestReducePermissionAwait_AutoApprove ensures yolo/safe-yolo modes bypass UI
// prompts but still persist durable completion and signal synchronous callers
// after state is applied.
func TestReducePermissionAwait_AutoApprove(t *testing.T) {
	t.Parallel()

	decisionCh := make(chan PermissionDecision, 1)
	ack := make(chan struct{}, 1)
	state := State{
		SessionID: "s1",
		FSM:       StateRemoteRunning,
		Mode:      ModeRemote,
		AgentState: types.AgentState{
			ControlledByUser:  false,
			PermissionMode:    "yolo",
			Requests:          map[string]types.AgentPendingRequest{},
			CompletedRequests: map[string]types.AgentCompletedRequest{},
		},
	}
	state = refreshAgentStateJSON(state)

	next, effects := Reduce(state, cmdPermissionAwait{
		RequestID: "r1",
		ToolName:  "tool.test",
		Input:     []byte(`{"x":1}`),
		NowMs:     10,
		Reply:     decisionCh,
		Ack:       ack,
	})

	_, ok := next.AgentState.CompletedRequests["r1"]
	require.True(t, ok)

	foundDecision := false
	foundAck := false
	for _, eff := range effects {
		switch e := eff.(type) {
		case effCompletePermissionDecision:
			if e.Reply == decisionCh {
				require.True(t, e.Decision.Allow)
				foundDecision = true
			}
		case effSignalAck:
			if e.Ack == ack {
				foundAck = true
			}
		}
	}
	require.True(t, foundDecision, "expected effCompletePermissionDecision")
	require.True(t, foundAck, "expected effSignalAck")
}

// TestSessionEffect_InterfaceCoverage ensures effect structs satisfy the
// session actor marker interface in a way that is exercised by tests.
func TestSessionEffect_InterfaceCoverage(t *testing.T) {
	t.Parallel()

	var effects []Effect
	effects = append(effects,
		effStartLocalRunner{},
		effStopLocalRunner{},
		effStartRemoteRunner{},
		effStopRemoteRunner{},
		effApplyEngineConfig{},
		effQueryAgentEngineSettings{},
		effRemoteSend{},
		effRemoteAbort{},
		effLocalSendLine{},
		effPersistAgentState{},
		effEmitEphemeral{},
		effEmitMessage{},
		effStartTimer{},
		effCancelTimer{},
		effCompleteReply{},
		effCompletePermissionDecision{},
		effSignalAck{},
		effStartDesktopTakebackWatcher{},
		effStopDesktopTakebackWatcher{},
	)

	for _, eff := range effects {
		eff.isSessionEffect()
	}

	// Also cover the embedded actor.Effect interface composition.
	var _ actor.Effect = effStartLocalRunner{}
}
