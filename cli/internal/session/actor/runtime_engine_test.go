package actor

import (
	"context"
	"testing"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/stretchr/testify/require"
)

func TestRuntime_StartStopRemote_UsesExistingEngine(t *testing.T) {
	restore := captureStdout(t)
	defer restore()

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentFake))
	engine := &stubInjectEngine{}

	rt.mu.Lock()
	rt.engine = engine
	rt.engineType = agentengine.AgentFake
	rt.mu.Unlock()

	rt.startEngineRemote(context.Background(), effStartRemoteRunner{Gen: 1, WorkDir: "", Resume: ""}, func(framework.Input) {})

	rt.mu.Lock()
	require.True(t, rt.engineRemoteActive)
	require.Equal(t, int64(1), rt.engineRemoteGen)
	rt.mu.Unlock()

	// startEngineRemote should stop local first.
	require.Contains(t, engine.stopped, agentengine.ModeLocal)
	require.Len(t, engine.started, 1)
	require.Equal(t, agentengine.ModeRemote, engine.started[0].Mode)

	rt.stopEngineRemote(effStopRemoteRunner{Gen: 1, Silent: true})
	require.Contains(t, engine.stopped, agentengine.ModeRemote)
}

func TestRuntime_StartStopLocal_UsesExistingEngine(t *testing.T) {
	restore := captureStdout(t)
	defer restore()

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentFake))
	engine := &stubInjectEngine{}

	rt.mu.Lock()
	rt.engine = engine
	rt.engineType = agentengine.AgentFake
	rt.mu.Unlock()

	rt.startEngineLocal(context.Background(), effStartLocalRunner{Gen: 2, WorkDir: "", Resume: ""}, func(framework.Input) {})

	rt.mu.Lock()
	require.True(t, rt.engineLocalActive)
	require.Equal(t, int64(2), rt.engineLocalGen)
	rt.mu.Unlock()

	// startEngineLocal should stop remote first.
	require.Contains(t, engine.stopped, agentengine.ModeRemote)
	require.Len(t, engine.started, 1)
	require.Equal(t, agentengine.ModeLocal, engine.started[0].Mode)

	rt.stopEngineLocal(effStopLocalRunner{Gen: 2})
	require.Contains(t, engine.stopped, agentengine.ModeLocal)
}

func TestRuntime_EnsureEngine_ReplacesMismatchedEngine(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentFake))
	old := &stubInjectEngine{}

	rt.mu.Lock()
	rt.engine = old
	rt.engineType = agentengine.AgentCodex
	rt.mu.Unlock()

	got := rt.ensureEngine(context.Background(), func(framework.Input) {})
	require.NotNil(t, got)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.True(t, old.closed)
	require.Equal(t, agentengine.AgentFake, rt.engineType)
	require.NotNil(t, rt.engine)
}
