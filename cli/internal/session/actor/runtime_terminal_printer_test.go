package actor

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/stretchr/testify/require"
)

// captureStdout redirects os.Stdout for the duration of the test and returns a
// restore function that also returns the captured output.
func captureStdout(t *testing.T) func() string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	return func() string {
		_ = w.Close()
		os.Stdout = orig
		out, _ := io.ReadAll(r)
		_ = r.Close()
		return string(out)
	}
}

type stubInjectEngine struct {
	events chan agentengine.Event

	injected []string

	started []agentengine.EngineStartSpec
	stopped []agentengine.Mode
	applied []agentengine.AgentConfig

	closed bool
}

func (s *stubInjectEngine) Start(_ context.Context, spec agentengine.EngineStartSpec) error {
	s.started = append(s.started, spec)
	return nil
}

func (s *stubInjectEngine) Stop(_ context.Context, mode agentengine.Mode) error {
	s.stopped = append(s.stopped, mode)
	return nil
}

func (s *stubInjectEngine) Close(context.Context) error {
	s.closed = true
	return nil
}
func (s *stubInjectEngine) SendUserMessage(context.Context, agentengine.UserMessage) error {
	return nil
}
func (s *stubInjectEngine) Abort(context.Context) error { return nil }
func (s *stubInjectEngine) Capabilities() agentengine.AgentCapabilities {
	return agentengine.AgentCapabilities{}
}
func (s *stubInjectEngine) CurrentConfig() agentengine.AgentConfig { return agentengine.AgentConfig{} }
func (s *stubInjectEngine) ApplyConfig(_ context.Context, cfg agentengine.AgentConfig) error {
	s.applied = append(s.applied, cfg)
	return nil
}
func (s *stubInjectEngine) Events() <-chan agentengine.Event { return s.events }
func (s *stubInjectEngine) Wait() error                      { return nil }

func (s *stubInjectEngine) InjectLine(text string) error {
	s.injected = append(s.injected, text)
	return nil
}

func TestRuntime_ACPConfigRoundTrip(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(t.TempDir(), false)
	rt.WithACPConfig("http://example", "agent", "sid")
	baseURL, agentName, sessionID := rt.ACPConfig()
	require.Equal(t, "http://example", baseURL)
	require.Equal(t, "agent", agentName)
	require.Equal(t, "sid", sessionID)
}

func TestRuntime_EngineLocalSendLine_InjectorCalled(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(t.TempDir(), false)
	engine := &stubInjectEngine{}

	rt.mu.Lock()
	rt.engine = engine
	rt.engineLocalGen = 7
	rt.mu.Unlock()

	rt.engineLocalSendLine(effLocalSendLine{Gen: 7, Text: "hello"})
	require.Equal(t, []string{"hello"}, engine.injected)
}

func TestRuntime_PrintRemoteUIEvent_WritesOutput(t *testing.T) {
	restore := captureStdout(t)

	rt := NewRuntime(t.TempDir(), false)
	rt.printRemoteUIEventIfApplicable(agentengine.EvUIEvent{
		Kind:          agentengine.UIEventThinking,
		BriefMarkdown: "thinking…",
	})

	out := restore()
	require.Contains(t, out, "[thinking]")
	require.Contains(t, out, "thinking")
}

func TestRuntime_PrintRemoteRecord_UserText_WritesOutput(t *testing.T) {
	restore := captureStdout(t)

	rt := NewRuntime(t.TempDir(), false)
	rt.printRemoteRecordIfApplicable([]byte(`{"role":"user","content":{"type":"text","text":"hi"}}`))

	out := restore()
	require.Contains(t, out, "[user]")
	require.Contains(t, out, "hi")
}
