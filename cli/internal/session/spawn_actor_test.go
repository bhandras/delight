package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/stretchr/testify/require"
)

type fakeChild struct {
	closeMu   sync.Mutex
	closeCnt  int
	waitMu    sync.Mutex
	waitCnt   int
	waitBlock chan struct{}
}

// Close implements spawnedChild.
func (f *fakeChild) Close() error {
	f.closeMu.Lock()
	f.closeCnt++
	f.closeMu.Unlock()
	return nil
}

// Wait implements spawnedChild.
func (f *fakeChild) Wait() error {
	f.waitMu.Lock()
	f.waitCnt++
	f.waitMu.Unlock()
	if f.waitBlock != nil {
		<-f.waitBlock
	}
	return nil
}

// closedCount returns the number of Close calls.
func (f *fakeChild) closedCount() int {
	f.closeMu.Lock()
	closed := f.closeCnt
	f.closeMu.Unlock()
	return closed
}

func TestReduceSpawnActor_ShutdownRejectsNewSpawn(t *testing.T) {
	t.Parallel()

	reply := make(chan spawnResult, 1)
	state := spawnActorState{Stopping: true}
	next, effects := reduceSpawnActor(state, cmdSpawnChild{Directory: "d", Reply: reply})
	require.True(t, next.Stopping)
	require.Len(t, effects, 1)
	_, ok := effects[0].(effCompleteSpawnReply)
	require.True(t, ok)
}

func TestReduceSpawnActor_SpawnAndReplyLifecycle(t *testing.T) {
	t.Parallel()

	reply := make(chan spawnResult, 1)
	state := spawnActorState{}

	next, effects := reduceSpawnActor(state, cmdSpawnChild{Directory: "/tmp", Agent: "codex", Reply: reply})
	require.Equal(t, int64(1), next.NextReq)
	require.NotNil(t, next.Pending)
	require.Contains(t, next.Pending, int64(1))
	require.Len(t, effects, 1)
	startEff, ok := effects[0].(effStartChild)
	require.True(t, ok)
	require.Equal(t, int64(1), startEff.ReqID)
	require.Equal(t, "/tmp", startEff.Directory)
	require.Equal(t, "codex", startEff.Agent)

	// Completion should delete pending and emit a reply effect.
	next2, effects2 := reduceSpawnActor(next, evChildStarted{ReqID: 1, SessionID: "s1", Directory: "/tmp"})
	require.NotContains(t, next2.Pending, int64(1))
	require.Len(t, effects2, 1)
	doneEff, ok := effects2[0].(effCompleteSpawnReply)
	require.True(t, ok)
	require.Equal(t, reply, doneEff.Reply)
	require.Equal(t, "s1", doneEff.Result.SessionID)
	require.NoError(t, doneEff.Result.Err)
}

func TestReduceSpawnActor_StopAndRestoreAndShutdownEffects(t *testing.T) {
	t.Parallel()

	reply := make(chan spawnResult, 1)
	state := spawnActorState{}

	next, effects := reduceSpawnActor(state, cmdStopChild{SessionID: "s1", Reply: reply})
	require.Equal(t, int64(1), next.NextReq)
	require.Len(t, effects, 1)
	stopEff, ok := effects[0].(effStopChild)
	require.True(t, ok)
	require.Equal(t, "s1", stopEff.SessionID)

	next, effects = reduceSpawnActor(next, cmdRestoreChildren{Reply: reply})
	require.Equal(t, int64(2), next.NextReq)
	require.Len(t, effects, 1)
	_, ok = effects[0].(effRestoreChildren)
	require.True(t, ok)

	next, effects = reduceSpawnActor(next, cmdShutdownChildren{Reply: reply})
	require.True(t, next.Stopping)
	require.Equal(t, int64(3), next.NextReq)
	require.Len(t, effects, 1)
	_, ok = effects[0].(effShutdownChildren)
	require.True(t, ok)
}

func TestReduceSpawnActor_FailedStartAndStopReply(t *testing.T) {
	t.Parallel()

	reply := make(chan spawnResult, 1)
	state := spawnActorState{
		Pending: map[int64]chan spawnResult{7: reply},
	}

	next, effects := reduceSpawnActor(state, evChildStartFailed{ReqID: 7, Err: context.Canceled})
	require.NotContains(t, next.Pending, int64(7))
	require.Len(t, effects, 1)
	doneEff, ok := effects[0].(effCompleteSpawnReply)
	require.True(t, ok)
	require.Error(t, doneEff.Result.Err)

	state = spawnActorState{
		Pending: map[int64]chan spawnResult{8: reply},
	}
	next, effects = reduceSpawnActor(state, evChildStopped{ReqID: 8, Err: nil})
	require.NotContains(t, next.Pending, int64(8))
	require.Len(t, effects, 1)
	doneEff, ok = effects[0].(effCompleteSpawnReply)
	require.True(t, ok)
	require.NoError(t, doneEff.Result.Err)
}

func TestSpawnActorRuntime_HandleStartAndStopChild_UpdatesRegistry(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{DelightHome: home}

	emitted := make(chan framework.Input, 8)
	child := &fakeChild{}
	r := &spawnActorRuntime{
		cfg:     cfg,
		ownerID: "owner-1",
		emit: func(in framework.Input) {
			select {
			case emitted <- in:
			default:
			}
		},
		startChild: func(ctx context.Context, _ *config.Config, _ string, _ bool, directory string, _ string) (string, spawnedChild, error) {
			_ = ctx
			require.Equal(t, "/tmp/dir", directory)
			return "child-1", child, nil
		},
	}

	r.handleStartChild(context.Background(), effStartChild{ReqID: 1, Directory: "/tmp/dir"})

	select {
	case in := <-emitted:
		evt, ok := in.(evChildStarted)
		require.True(t, ok)
		require.Equal(t, int64(1), evt.ReqID)
		require.Equal(t, "child-1", evt.SessionID)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for start completion event")
	}

	data, err := os.ReadFile(filepath.Join(home, "spawned.sessions.json"))
	require.NoError(t, err)
	var reg spawnRegistry
	require.NoError(t, json.Unmarshal(data, &reg))
	require.Equal(t, "/tmp/dir", reg.Owners["owner-1"]["child-1"].Directory)

	// Stop: should remove registry entry and close the child.
	r.childrenMu.Lock()
	r.children = map[string]spawnedChild{"child-1": child}
	r.childrenMu.Unlock()

	r.handleStopChild(context.Background(), effStopChild{ReqID: 2, SessionID: "child-1"})

	select {
	case in := <-emitted:
		evt, ok := in.(evChildStopped)
		require.True(t, ok)
		require.Equal(t, int64(2), evt.ReqID)
		require.NoError(t, evt.Err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for stop completion event")
	}

	require.Equal(t, 1, child.closedCount())

	data, err = os.ReadFile(filepath.Join(home, "spawned.sessions.json"))
	require.NoError(t, err)
	reg = spawnRegistry{}
	require.NoError(t, json.Unmarshal(data, &reg))
	_, ok := reg.Owners["owner-1"]["child-1"]
	require.False(t, ok)
}

func TestSpawnActorRuntime_HandleStopChild_NotFound(t *testing.T) {
	t.Parallel()

	emitted := make(chan framework.Input, 8)
	r := &spawnActorRuntime{
		ownerID: "owner-1",
		children: map[string]spawnedChild{
			"child-1": &fakeChild{},
		},
		emit: func(in framework.Input) {
			select {
			case emitted <- in:
			default:
			}
		},
	}

	r.handleStopChild(context.Background(), effStopChild{ReqID: 1, SessionID: "missing"})

	select {
	case in := <-emitted:
		evt, ok := in.(evChildStopped)
		require.True(t, ok)
		require.Equal(t, int64(1), evt.ReqID)
		require.Error(t, evt.Err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for stop completion event")
	}
}

func TestSpawnActorRuntime_HandleShutdown_ClosesAllChildren(t *testing.T) {
	t.Parallel()

	emitted := make(chan framework.Input, 8)
	child1 := &fakeChild{}
	child2 := &fakeChild{}
	r := &spawnActorRuntime{
		ownerID: "owner-1",
		children: map[string]spawnedChild{
			"child-1": child1,
			"child-2": child2,
		},
		emit: func(in framework.Input) {
			select {
			case emitted <- in:
			default:
			}
		},
	}

	r.handleShutdown(context.Background(), effShutdownChildren{ReqID: 9})

	select {
	case in := <-emitted:
		evt, ok := in.(evChildStopped)
		require.True(t, ok)
		require.Equal(t, int64(9), evt.ReqID)
		require.NoError(t, evt.Err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for shutdown completion event")
	}

	require.Equal(t, 1, child1.closedCount())
	require.Equal(t, 1, child2.closedCount())
}

func TestSpawnActor_ActorLoop_SpawnCompletesReply(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{DelightHome: home}

	rt := &spawnActorRuntime{
		cfg:     cfg,
		ownerID: "owner-1",
		startChild: func(ctx context.Context, _ *config.Config, _ string, _ bool, directory string, _ string) (string, spawnedChild, error) {
			_ = ctx
			require.Equal(t, "/tmp/dir", directory)
			return "child-1", &fakeChild{}, nil
		},
	}

	a := framework.New(
		spawnActorState{OwnerSessionID: "owner-1"},
		func(state spawnActorState, input framework.Input) (spawnActorState, []framework.Effect) {
			return reduceSpawnActor(state, input)
		},
		rt,
	)
	a.Start()
	defer a.Stop()

	reply := make(chan spawnResult, 1)
	require.True(t, a.Enqueue(cmdSpawnChild{Directory: "/tmp/dir", Reply: reply}))

	select {
	case res := <-reply:
		require.NoError(t, res.Err)
		require.Equal(t, "child-1", res.SessionID)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for spawn reply")
	}
}

func TestSpawnActorRuntime_RegistryRoundTrip(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{DelightHome: home}

	r := &spawnActorRuntime{
		cfg:     cfg,
		ownerID: "owner-1",
	}

	// Empty registry should load on first call.
	reg, err := r.loadRegistry()
	require.NoError(t, err)
	require.NotNil(t, reg.Owners)

	r.registerSpawnedSession("child-1", "/tmp/a")
	r.registerSpawnedSession("child-2", "/tmp/b")

	// Verify file was written and includes both sessions under owner.
	data, err := os.ReadFile(filepath.Join(home, "spawned.sessions.json"))
	require.NoError(t, err)
	var reg2 spawnRegistry
	require.NoError(t, json.Unmarshal(data, &reg2))
	require.Equal(t, "/tmp/a", reg2.Owners["owner-1"]["child-1"].Directory)
	require.Equal(t, "/tmp/b", reg2.Owners["owner-1"]["child-2"].Directory)

	r.removeSpawnedSession("child-1")
	data, err = os.ReadFile(filepath.Join(home, "spawned.sessions.json"))
	require.NoError(t, err)
	reg2 = spawnRegistry{}
	require.NoError(t, json.Unmarshal(data, &reg2))
	_, ok := reg2.Owners["owner-1"]["child-1"]
	require.False(t, ok)
	_, ok = reg2.Owners["owner-1"]["child-2"]
	require.True(t, ok)
}

func TestSpawnActorRuntime_HandleRestore_RewritesSessionID(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{DelightHome: home}

	// Seed a registry entry for the owner.
	seed := spawnRegistry{Owners: map[string]map[string]spawnEntry{
		"owner-1": {
			"child-old": {SessionID: "child-old", Directory: "/tmp/dir"},
		},
	}}
	raw, err := json.Marshal(seed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "spawned.sessions.json"), raw, 0600))

	emitted := make(chan framework.Input, 8)
	r := &spawnActorRuntime{
		cfg:     cfg,
		ownerID: "owner-1",
		emit: func(in framework.Input) {
			select {
			case emitted <- in:
			default:
			}
		},
		startChild: func(ctx context.Context, _ *config.Config, _ string, _ bool, directory string, _ string) (string, spawnedChild, error) {
			_ = ctx
			require.Equal(t, "/tmp/dir", directory)
			return "child-new", &fakeChild{}, nil
		},
	}

	r.handleRestore(context.Background(), effRestoreChildren{ReqID: 1})

	select {
	case in := <-emitted:
		evt, ok := in.(evChildStopped)
		require.True(t, ok)
		require.Equal(t, int64(1), evt.ReqID)
		require.NoError(t, evt.Err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for restore completion event")
	}

	// Registry should have been rewritten to the new session id.
	data, err := os.ReadFile(filepath.Join(home, "spawned.sessions.json"))
	require.NoError(t, err)
	var reg spawnRegistry
	require.NoError(t, json.Unmarshal(data, &reg))
	owner := reg.Owners["owner-1"]
	_, ok := owner["child-old"]
	require.False(t, ok)
	_, ok = owner["child-new"]
	require.True(t, ok)
	require.Equal(t, "/tmp/dir", owner["child-new"].Directory)
}
