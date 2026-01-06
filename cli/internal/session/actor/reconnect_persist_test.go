package actor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

type flakyStateUpdater struct {
	mu sync.Mutex

	online bool

	calls    int
	success  int
	lastErr  error
	lastJSON string
}

// UpdateState implements StateUpdater and allows tests to simulate offline
// behavior by toggling f.online.
func (f *flakyStateUpdater) UpdateState(sessionID string, agentState string, expectedVersion int64) (int64, error) {
	_ = sessionID
	_ = expectedVersion

	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	f.lastJSON = agentState
	if !f.online {
		f.lastErr = errors.New("offline")
		return 0, f.lastErr
	}
	f.success++
	f.lastErr = nil
	return int64(f.calls), nil
}

// Snapshot returns a consistent view of the updater's state for assertions.
func (f *flakyStateUpdater) Snapshot() (calls int, success int, lastJSON string, lastErr error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.success, f.lastJSON, f.lastErr
}

// newActorWithUpdater constructs a SessionActor instance wired to the provided
// StateUpdater, returning an actor and a cleanup function.
func newActorWithUpdater(t *testing.T, updater StateUpdater, emitter *fakeSocketEmitter) (*framework.Actor[State], func()) {
	t.Helper()

	if emitter == nil {
		emitter = newFakeSocketEmitter()
	}

	restoreStdout := silenceStdout(t)

	workDir := t.TempDir()
	rt := NewRuntime(workDir, true).
		WithSessionID(testSessionID).
		WithStateUpdater(updater).
		WithSocketEmitter(emitter).
		WithAgent(string(agentengine.AgentFake)).
		WithEncryptFn(encryptPassthrough)

	initialAgentState := types.AgentState{
		AgentType:         string(agentengine.AgentFake),
		ControlledByUser:  true,
		Requests:          make(map[string]types.AgentPendingRequest),
		CompletedRequests: make(map[string]types.AgentCompletedRequest),
	}
	rawState, err := json.Marshal(initialAgentState)
	require.NoError(t, err)

	initial := State{
		SessionID:             testSessionID,
		FSM:                   StateClosed,
		Mode:                  ModeLocal,
		AgentState:            initialAgentState,
		AgentStateJSON:        string(rawState),
		AgentStateVersion:     0,
		PersistRetryRemaining: 0,
	}

	a := framework.New(initial, Reduce, rt, framework.WithMailboxSize[State](4096))
	a.Start()

	cleanup := func() {
		a.Stop()
		select {
		case <-a.Done():
		case <-time.After(testWaitTimeout):
			t.Fatal("timeout waiting for actor to stop")
		}
		restoreStdout()
	}
	return a, cleanup
}

func TestMockPhone_ReconnectTriggersAgentStateRepersist(t *testing.T) {
	updater := &flakyStateUpdater{online: true}
	emitter := newFakeSocketEmitter()
	actorLoop, cleanup := newActorWithUpdater(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	// Ensure the initial persist from mode switch succeeds so we can isolate the
	// subsequent offline failure and reconnect recovery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, success, _, _ := updater.Snapshot()
		if success >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	initialCalls, initialSuccess, _, _ := updater.Snapshot()
	require.GreaterOrEqual(t, initialSuccess, 1)

	// Simulate websocket offline.
	updater.mu.Lock()
	updater.online = false
	updater.mu.Unlock()
	require.True(t, actorLoop.Enqueue(WSDisconnected("offline")))

	decisionCh := make(chan PermissionDecision, 1)
	require.True(t, actorLoop.Enqueue(AwaitPermission(
		"req-1",
		"tool.test",
		[]byte(`{"x":1}`),
		testNowMs,
		decisionCh,
	)))

	// Wait for a persist attempt while offline.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls, _, _, _ := updater.Snapshot()
		if calls > initialCalls {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls, success, _, lastErr := updater.Snapshot()
	require.Greater(t, calls, initialCalls)
	require.Equal(t, initialSuccess, success)
	require.Error(t, lastErr)

	// Reconnect and ensure we re-persist.
	updater.mu.Lock()
	updater.online = true
	updater.mu.Unlock()
	require.True(t, actorLoop.Enqueue(WSConnected()))

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls, success, _, _ := updater.Snapshot()
		if calls >= initialCalls+2 && success >= initialSuccess+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls, success, _, lastErr = updater.Snapshot()
	t.Fatalf("expected repersist after reconnect; calls=%d success=%d err=%v", calls, success, lastErr)
}

func TestMockPhone_RuntimeAwaitPermission_ContextCancel(t *testing.T) {
	// Cover the ctx cancellation path for AwaitPermission with a configured runtime.
	updater := &flakyStateUpdater{online: true}
	emitter := newFakeSocketEmitter()
	actorLoop, rt, cleanup := newSessionActorForTest(t, nil, emitter)
	defer cleanup()

	rt.WithStateUpdater(updater)

	requireSwitchMode(t, actorLoop, ModeRemote)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := rt.AwaitPermission(ctx, "req-cancel", "tool.test", json.RawMessage(`{}`), testNowMs)
	require.Error(t, err)
}
