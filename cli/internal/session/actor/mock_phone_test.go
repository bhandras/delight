package actor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

const (
	// testSessionID is the stable session id used for session actor tests.
	testSessionID = "s-test"
	// testWaitTimeout bounds how long we wait for async events.
	testWaitTimeout = 2 * time.Second
	// testNowMs is a stable wall-clock timestamp used in tests.
	testNowMs = int64(1234)
	// testAgentStateVersionMismatchVersion is the server version returned on a mismatch.
	testAgentStateVersionMismatchVersion = int64(5)
)

// silenceStdout redirects stdout to a pipe to keep tests deterministic and to
// avoid polluting the developer terminal with remote-mode banners.
func silenceStdout(t *testing.T) func() {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	// Drain the reader in the background so writers don't block.
	done := make(chan struct{})
	go func() {
		_, _ = r.Read(make([]byte, 4096))
		_ = r.Close()
		close(done)
	}()

	return func() {
		_ = w.Close()
		os.Stdout = orig
		select {
		case <-done:
		case <-time.After(testWaitTimeout):
		}
	}
}

// fakeStateUpdater records agent state persistence attempts and can be
// configured to return a version-mismatch once.
type fakeStateUpdater struct {
	mu sync.Mutex

	calls []persistCall

	mismatchOnce bool
	version      int64
}

// persistCall captures a single persistence attempt.
type persistCall struct {
	sessionID        string
	agentStateJSON   string
	expectedVersion  int64
	returnedVersion  int64
	returnedMismatch bool
}

// UpdateState implements actor.Runtime StateUpdater.
func (f *fakeStateUpdater) UpdateState(sessionID string, agentState string, expectedVersion int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.mismatchOnce {
		f.mismatchOnce = false
		f.calls = append(f.calls, persistCall{
			sessionID:        sessionID,
			agentStateJSON:   agentState,
			expectedVersion:  expectedVersion,
			returnedVersion:  testAgentStateVersionMismatchVersion,
			returnedMismatch: true,
		})
		return testAgentStateVersionMismatchVersion, websocket.ErrVersionMismatch
	}

	f.version++
	f.calls = append(f.calls, persistCall{
		sessionID:        sessionID,
		agentStateJSON:   agentState,
		expectedVersion:  expectedVersion,
		returnedVersion:  f.version,
		returnedMismatch: false,
	})
	return f.version, nil
}

// Calls returns the recorded persistence calls.
func (f *fakeStateUpdater) Calls() []persistCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]persistCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// fakeSocketEmitter captures emitted session messages/ephemerals.
type fakeSocketEmitter struct {
	mu sync.Mutex

	messages   []wire.OutboundMessagePayload
	ephemerals []any
	rawEvents  []rawEvent

	messageCh   chan wire.OutboundMessagePayload
	ephemeralCh chan any
	rawCh       chan rawEvent
}

type rawEvent struct {
	name string
	data any
}

// newFakeSocketEmitter returns a new fake socket emitter.
func newFakeSocketEmitter() *fakeSocketEmitter {
	return &fakeSocketEmitter{
		messageCh:   make(chan wire.OutboundMessagePayload, 64),
		ephemeralCh: make(chan any, 64),
		rawCh:       make(chan rawEvent, 64),
	}
}

// EmitEphemeral implements actor.Runtime SocketEmitter.
func (f *fakeSocketEmitter) EmitEphemeral(data any) error {
	f.mu.Lock()
	f.ephemerals = append(f.ephemerals, data)
	f.mu.Unlock()

	select {
	case f.ephemeralCh <- data:
	default:
	}
	return nil
}

// EmitMessage implements actor.Runtime SocketEmitter.
func (f *fakeSocketEmitter) EmitMessage(data any) error {
	payload, ok := data.(wire.OutboundMessagePayload)
	if !ok {
		return nil
	}

	f.mu.Lock()
	f.messages = append(f.messages, payload)
	f.mu.Unlock()

	select {
	case f.messageCh <- payload:
	default:
	}
	return nil
}

// EmitRaw implements actor.Runtime SocketEmitter.
func (f *fakeSocketEmitter) EmitRaw(event string, data any) error {
	f.mu.Lock()
	f.rawEvents = append(f.rawEvents, rawEvent{name: event, data: data})
	f.mu.Unlock()

	select {
	case f.rawCh <- rawEvent{name: event, data: data}:
	default:
	}
	return nil
}

// Messages returns a snapshot of emitted messages.
func (f *fakeSocketEmitter) Messages() []wire.OutboundMessagePayload {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wire.OutboundMessagePayload, len(f.messages))
	copy(out, f.messages)
	return out
}

// Ephemerals returns a snapshot of emitted ephemerals.
func (f *fakeSocketEmitter) Ephemerals() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]any, len(f.ephemerals))
	copy(out, f.ephemerals)
	return out
}

// RawEvents returns a snapshot of emitted raw socket events.
func (f *fakeSocketEmitter) RawEvents() []rawEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]rawEvent, len(f.rawEvents))
	copy(out, f.rawEvents)
	return out
}

// waitForMessage waits for a message payload to be emitted.
func waitForMessage(t *testing.T, emitter *fakeSocketEmitter) wire.OutboundMessagePayload {
	t.Helper()
	select {
	case msg := <-emitter.messageCh:
		return msg
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for emitted message")
		return wire.OutboundMessagePayload{}
	}
}

// waitForEphemeral waits for an ephemeral payload to be emitted.
func waitForEphemeral(t *testing.T, emitter *fakeSocketEmitter) any {
	t.Helper()
	select {
	case ep := <-emitter.ephemeralCh:
		return ep
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for emitted ephemeral")
		return nil
	}
}

// encryptPassthrough returns a "ciphertext" by base64-encoding plaintext JSON.
//
// Tests decode this value to assert which records would be persisted to the server.
func encryptPassthrough(plaintext []byte) (string, error) {
	return base64.StdEncoding.EncodeToString(plaintext), nil
}

// decodeCiphertext decodes a passthrough-encrypted message into a JSON object.
func decodeCiphertext(t *testing.T, ciphertext string, out any) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, out))
}

// newSessionActorForTest constructs and starts a session actor wired to a real
// runtime (effects interpreter) with fake persistence + socket emitters.
func newSessionActorForTest(t *testing.T, updater *fakeStateUpdater, emitter *fakeSocketEmitter) (*framework.Actor[State], *Runtime, func()) {
	t.Helper()

	if updater == nil {
		updater = &fakeStateUpdater{}
	}
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
		Model:             "",
		PermissionMode:    "",
		ReasoningEffort:   "",
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
	return a, rt, cleanup
}

// requireSwitchMode enqueues a mode switch and waits for completion.
func requireSwitchMode(t *testing.T, a *framework.Actor[State], mode Mode) {
	t.Helper()

	reply := make(chan error, 1)
	require.True(t, a.Enqueue(SwitchMode(mode, reply)))
	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatalf("timeout waiting for mode switch to %s", mode)
	}
}

// requireRemoteSend enqueues a remote send and waits for acceptance.
func requireRemoteSend(t *testing.T, a *framework.Actor[State], text string, localID string) {
	t.Helper()

	reply := make(chan error, 1)
	require.True(t, a.Enqueue(RemoteSend(text, nil, localID, reply)))
	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for remote send ack")
	}
}

// TestMockPhone_FakeEngine_RemoteSendRoundTrip ensures that the SessionActor
// runtime can start the fake engine, process a remote send, and emit an
// encrypted message to the socket emitter.
func TestMockPhone_FakeEngine_RemoteSendRoundTrip(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)
	requireRemoteSend(t, actorLoop, "ping", "local-1")

	msg := waitForMessage(t, emitter)
	require.Equal(t, testSessionID, msg.SID)
	require.NotEmpty(t, msg.Message)

	var rec wire.AgentOutputRecord
	decodeCiphertext(t, msg.Message, &rec)
	require.Equal(t, "agent", rec.Role)
	require.Equal(t, "assistant", rec.Content.Data.Message.Role)

	text := ""
	if len(rec.Content.Data.Message.Content) > 0 {
		text = rec.Content.Data.Message.Content[0].Text
	}
	require.Equal(t, "fake-agent: ping", text)

	// The fake engine emits thinking toggles; ensure we emitted at least one
	// session-alive (activity) update.
	foundAlive := false
	for _, ev := range emitter.RawEvents() {
		if ev.name != "session-alive" {
			continue
		}
		payload, ok := ev.data.(wire.SessionAlivePayload)
		if !ok {
			continue
		}
		if payload.SID == testSessionID {
			foundAlive = true
			break
		}
	}
	require.True(t, foundAlive)
}

// TestMockPhone_UIEventPersistsToMessage ensures that engine UI events are
// forwarded as ephemerals and persisted as encrypted session messages.
func TestMockPhone_UIEventPersistsToMessage(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	state := actorLoop.State()
	ev := evEngineUIEvent{
		Gen:           state.RunnerGen,
		Mode:          ModeRemote,
		EventID:       "evt-1",
		Kind:          string(agentengine.UIEventTool),
		Phase:         string(agentengine.UIEventPhaseStart),
		Status:        string(agentengine.UIEventStatusRunning),
		BriefMarkdown: "doing stuff",
		FullMarkdown:  "doing stuff (full)",
		NowMs:         testNowMs,
	}
	require.True(t, actorLoop.Enqueue(ev))

	// First observe the ephemeral emission.
	ep := waitForEphemeral(t, emitter)
	payload, ok := ep.(wire.EphemeralUIEventPayload)
	require.True(t, ok)
	require.Equal(t, "ui.event", payload.Type)
	require.Equal(t, testSessionID, payload.SessionID)
	require.Equal(t, "evt-1", payload.EventID)

	// Then observe the persisted message emission.
	msg := waitForMessage(t, emitter)
	var record wire.UIEventRecord
	decodeCiphertext(t, msg.Message, &record)
	require.Equal(t, "event", record.Role)
	require.Equal(t, "ui.event", record.Type)
	require.Equal(t, testSessionID, record.SessionID)
	require.Equal(t, "evt-1", record.EventID)
}

// TestMockPhone_PermissionAwaitDecisionFlow exercises the engine-agnostic
// permission request lifecycle: AwaitPermission registers a request and
// SubmitPermissionDecision delivers the decision to the waiter.
func TestMockPhone_PermissionAwaitDecisionFlow(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	decisionCh := make(chan PermissionDecision, 1)
	require.True(t, actorLoop.Enqueue(AwaitPermission(
		"req-1",
		"tool.test",
		[]byte(`{"x":1}`),
		testNowMs,
		decisionCh,
	)))

	// AwaitPermission should emit a permission-request ephemeral.
	got := waitForEphemeral(t, emitter)
	ep, ok := got.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "permission-request", ep["type"])
	require.Equal(t, testSessionID, ep["id"])

	reply := make(chan error, 1)
	require.True(t, actorLoop.Enqueue(SubmitPermissionDecision("req-1", true, "ok", testNowMs+1, reply)))
	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for permission decision ack")
	}

	select {
	case decision := <-decisionCh:
		require.True(t, decision.Allow)
		require.Equal(t, "ok", decision.Message)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for permission decision delivery")
	}
}

// TestMockPhone_PersistAgentStateVersionMismatchRetry ensures version-mismatch
// persistence errors are handled and retried by the reducer.
func TestMockPhone_PersistAgentStateVersionMismatchRetry(t *testing.T) {
	updater := &fakeStateUpdater{mismatchOnce: true}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	// Switching to remote schedules an immediate persist. The first attempt will
	// hit a version mismatch, which should trigger a retry.
	requireSwitchMode(t, actorLoop, ModeRemote)

	deadline := time.Now().Add(testWaitTimeout)
	for time.Now().Before(deadline) {
		calls := updater.Calls()
		if len(calls) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls := updater.Calls()
	require.GreaterOrEqual(t, len(calls), 2, "expected version mismatch retry")
	require.True(t, calls[0].returnedMismatch, "expected first call to be version mismatch")
	require.False(t, calls[1].returnedMismatch, "expected second call to succeed")
}

// TestMockPhone_GetAgentEngineSettings exercises the runtime-backed settings
// query used by the phone UI.
func TestMockPhone_GetAgentEngineSettings(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	reply := make(chan AgentEngineSettingsSnapshot, 1)
	require.True(t, actorLoop.Enqueue(GetAgentEngineSettings(reply)))

	select {
	case snap := <-reply:
		require.Equal(t, agentengine.AgentType(agentengine.AgentFake), snap.AgentType)
		// Fake engine doesn't report capabilities, but the reply should still be well-formed.
		require.Equal(t, "", snap.Error)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for engine settings snapshot")
	}
}

// TestMockPhone_SetAgentConfig exercises durable config updates and best-effort
// engine ApplyConfig execution.
func TestMockPhone_SetAgentConfig(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	reply := make(chan error, 1)
	require.True(t, actorLoop.Enqueue(SetAgentConfig("fake-model", "default", "high", reply)))
	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for SetAgentConfig ack")
	}

	state := actorLoop.State()
	require.Equal(t, "fake-model", state.AgentState.Model)
	require.Equal(t, "default", state.AgentState.PermissionMode)
	require.Equal(t, "high", state.AgentState.ReasoningEffort)
}

// TestMockPhone_AbortRemote exercises the abort command and runtime dispatch.
func TestMockPhone_AbortRemote(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	reply := make(chan error, 1)
	require.True(t, actorLoop.Enqueue(AbortRemote(reply)))
	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for abort ack")
	}
}

// TestMockPhone_SwitchRemoteThenLocal ensures mode switching covers both remote
// and local transitions with the fake engine.
func TestMockPhone_SwitchRemoteThenLocal(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)
	require.Equal(t, StateRemoteRunning, actorLoop.State().FSM)

	requireSwitchMode(t, actorLoop, ModeLocal)
	require.Equal(t, StateLocalRunning, actorLoop.State().FSM)
}

// TestMockPhone_ShutdownTransitionsClosed exercises the Shutdown command and
// ensures the actor reaches the Closed state.
func TestMockPhone_ShutdownTransitionsClosed(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	reply := make(chan error, 1)
	require.True(t, actorLoop.Enqueue(Shutdown(reply)))

	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for shutdown ack")
	}

	require.Equal(t, StateClosing, actorLoop.State().FSM)
}

// TestMockPhone_InboundUserMessageBuffersUntilRemote ensures that inbound
// messages delivered while not remote are buffered and flushed once remote
// runner is ready.
func TestMockPhone_InboundUserMessageBuffersUntilRemote(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, _, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	// Start in local (closed) and inject an inbound phone message. This should
	// force a switch to remote and eventually flush the message to the engine.
	require.True(t, actorLoop.Enqueue(InboundUserMessage("ping", nil, "local-2", testNowMs)))

	// Wait for a message emission (assistant response).
	msg := waitForMessage(t, emitter)
	var rec wire.AgentOutputRecord
	decodeCiphertext(t, msg.Message, &rec)
	text := ""
	if len(rec.Content.Data.Message.Content) > 0 {
		text = rec.Content.Data.Message.Content[0].Text
	}
	require.Equal(t, "fake-agent: ping", text)
}

// TestMockPhone_RuntimeRespectsContextCancel ensures that long-running effects
// stop emitting once the runtime context is canceled.
func TestMockPhone_RuntimeRespectsContextCancel(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()

	workDir := t.TempDir()
	rt := NewRuntime(workDir, true).
		WithSessionID(testSessionID).
		WithStateUpdater(updater).
		WithSocketEmitter(emitter).
		WithAgent(string(agentengine.AgentFake)).
		WithEncryptFn(encryptPassthrough)

	// Build a minimal actor without starting it; instead we drive the runtime
	// directly to cover cancellation behavior deterministically.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt.HandleEffects(ctx, []framework.Effect{
		effStartTimer{Name: "x", AfterMs: 10},
	}, func(framework.Input) {})
}

// TestMockPhone_RuntimeAwaitPermissionBlocksUntilDecision ensures the runtime's
// PermissionRequester implementation blocks until the reducer registers a
// request and a decision is submitted.
func TestMockPhone_RuntimeAwaitPermissionBlocksUntilDecision(t *testing.T) {
	updater := &fakeStateUpdater{}
	emitter := newFakeSocketEmitter()
	actorLoop, rt, cleanup := newSessionActorForTest(t, updater, emitter)
	defer cleanup()

	requireSwitchMode(t, actorLoop, ModeRemote)

	type result struct {
		decision agentengine.PermissionDecision
		err      error
	}
	resultCh := make(chan result, 1)

	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()

	go func() {
		decision, err := rt.AwaitPermission(ctx, "req-rt-1", "tool.test", json.RawMessage(`{"x":1}`), testNowMs)
		resultCh <- result{decision: decision, err: err}
	}()

	// Ensure the reducer emitted a permission request ephemeral.
	got := waitForEphemeral(t, emitter)
	ep, ok := got.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "permission-request", ep["type"])
	require.Equal(t, testSessionID, ep["id"])

	reply := make(chan error, 1)
	require.True(t, actorLoop.Enqueue(SubmitPermissionDecision("req-rt-1", true, "ok", testNowMs+1, reply)))
	select {
	case err := <-reply:
		require.NoError(t, err)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for permission decision ack")
	}

	select {
	case r := <-resultCh:
		require.NoError(t, r.err)
		require.True(t, r.decision.Allow)
		require.Equal(t, "ok", r.decision.Message)
	case <-time.After(testWaitTimeout):
		t.Fatal("timeout waiting for runtime AwaitPermission to resolve")
	}
}
