package session

import (
	"encoding/json"
	"testing"

	framework "github.com/bhandras/delight/cli/internal/actor"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

type fakeUpdater struct {
	calls int
}

// UpdateState implements sessionactor.StateUpdater.
func (f *fakeUpdater) UpdateState(string, string, int64) (int64, error) {
	f.calls++
	return int64(f.calls), nil
}

type fakeEmitter struct{}

// EmitEphemeral implements sessionactor.SocketEmitter.
func (fakeEmitter) EmitEphemeral(any) error { return nil }

// EmitMessage implements sessionactor.SocketEmitter.
func (fakeEmitter) EmitMessage(any) error   { return nil }

// EmitRaw implements sessionactor.SocketEmitter.
func (fakeEmitter) EmitRaw(string, any) error {
	return nil
}

func TestManager_SetAgentConfig_WaitsForPersist(t *testing.T) {
	t.Parallel()

	updater := &fakeUpdater{}
	emitter := fakeEmitter{}

	rt := sessionactor.NewRuntime(t.TempDir(), false).
		WithSessionID("s1").
		WithStateUpdater(updater).
		WithSocketEmitter(emitter).
		WithAgent("fake").
		WithEncryptFn(func(b []byte) (string, error) { return string(b), nil })

	initialAgentState := types.AgentState{
		AgentType:         "fake",
		ControlledByUser:  false,
		Requests:          map[string]types.AgentPendingRequest{},
		CompletedRequests: map[string]types.AgentCompletedRequest{},
	}
	raw, err := json.Marshal(initialAgentState)
	require.NoError(t, err)

	initial := sessionactor.State{
		SessionID:         "s1",
		FSM:               sessionactor.StateRemoteRunning,
		Mode:              sessionactor.ModeRemote,
		AgentState:        initialAgentState,
		AgentStateJSON:    string(raw),
		AgentStateVersion: 0,
		WSConnected:       true,
	}

	a := framework.New(initial, sessionactor.Reduce, rt)
	a.Start()
	defer a.Stop()

	m := &Manager{
		sessionID:    "s1",
		sessionActor: a,
		stopCh:       make(chan struct{}),
	}

	err = m.SetAgentConfig("m1", "default", "high")
	require.NoError(t, err)

	state := a.State()
	require.Equal(t, "m1", state.AgentState.Model)
	require.Equal(t, "default", state.AgentState.PermissionMode)
	require.Equal(t, "high", state.AgentState.ReasoningEffort)
	require.GreaterOrEqual(t, updater.calls, 1)
}
