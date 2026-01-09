package session

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/bhandras/delight/cli/internal/agentengine/codexengine"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestSeedAgentStateFromServerPreservesDurableConfig(t *testing.T) {
	t.Parallel()

	serverState := types.AgentState{
		AgentType:        "codex",
		ControlledByUser: false,
		Model:            "gpt-5.2",
		ReasoningEffort:  "medium",
		PermissionMode:   "read-only",
		ResumeToken:      "server-resume",
		Requests:         map[string]types.AgentPendingRequest{},
		CompletedRequests: map[string]types.AgentCompletedRequest{
			"r1": {ToolName: "can_use_tool", Input: "{}", Allow: true, ResolvedAt: 1},
		},
	}
	raw, err := json.Marshal(serverState)
	require.NoError(t, err)

	m := &Manager{
		agent:                 "codex",
		sessionAgentStateJSON: string(raw),
		cfg:                   &config.Config{},
	}

	got := m.seedAgentStateFromServer()
	require.Equal(t, "codex", got.AgentType)
	require.True(t, got.ControlledByUser)
	require.Equal(t, "gpt-5.2", got.Model)
	require.Equal(t, "medium", got.ReasoningEffort)
	require.Equal(t, "read-only", got.PermissionMode)
	require.Equal(t, "server-resume", got.ResumeToken)
	require.NotNil(t, got.Requests)
	require.NotNil(t, got.CompletedRequests)
}

func TestSeedAgentStateFromServerAppliesCodexDefaults(t *testing.T) {
	t.Parallel()

	m := &Manager{
		agent: "codex",
		cfg:   &config.Config{},
	}

	got := m.seedAgentStateFromServer()
	require.Equal(t, codexengine.DefaultModel(), got.Model)
	require.Equal(t, codexengine.DefaultReasoningEffort(), got.ReasoningEffort)
	require.Equal(t, "default", got.PermissionMode)
}

func TestSeedAgentStateFromServerOverlaysCLIFlags(t *testing.T) {
	t.Parallel()

	serverState := types.AgentState{
		AgentType:         "codex",
		ControlledByUser:  false,
		Model:             "gpt-5.2",
		ReasoningEffort:   "medium",
		PermissionMode:    "default",
		ResumeToken:       "server-resume",
		Requests:          map[string]types.AgentPendingRequest{},
		CompletedRequests: map[string]types.AgentCompletedRequest{},
	}
	raw, err := json.Marshal(serverState)
	require.NoError(t, err)

	m := &Manager{
		agent:                 "codex",
		sessionAgentStateJSON: string(raw),
		cfg: &config.Config{
			Model:       "gpt-5.1-codex-max",
			ResumeToken: "cli-resume",
		},
	}

	got := m.seedAgentStateFromServer()
	require.Equal(t, "gpt-5.1-codex-max", got.Model)
	require.Equal(t, "cli-resume", got.ResumeToken)
	// Ensure unrelated durable config remains intact.
	require.Equal(t, "medium", got.ReasoningEffort)
	require.Equal(t, "default", got.PermissionMode)
}

func TestStableSessionTagForAgent(t *testing.T) {
	t.Parallel()

	require.Equal(t, "t1:codex", stableSessionTagForAgent("t1", "codex"))
	require.Equal(t, "t1", stableSessionTagForAgent("t1", ""))
	require.Equal(t, "", stableSessionTagForAgent("", "codex"))
}

func TestSelectExistingSessionForAgentMatchesAgentAndDir(t *testing.T) {
	t.Parallel()

	meta := types.Metadata{Path: "/tmp/proj", Flavor: "codex"}
	metaRaw, err := json.Marshal(meta)
	require.NoError(t, err)
	metaEnc := base64.StdEncoding.EncodeToString(metaRaw)

	codexState := types.AgentState{AgentType: "codex", ResumeToken: "thread-1"}
	codexRaw, err := json.Marshal(codexState)
	require.NoError(t, err)
	codexJSON := string(codexRaw)

	claudeState := types.AgentState{AgentType: "claude", ResumeToken: "sess-1"}
	claudeRaw, err := json.Marshal(claudeState)
	require.NoError(t, err)
	claudeJSON := string(claudeRaw)

	sessions := []listSessionItem{
		{
			ID:         "s-claude",
			TerminalID: "term-1",
			Metadata:   metaEnc,
			AgentState: &claudeJSON,
			UpdatedAt:  100,
		},
		{
			ID:         "s-codex",
			TerminalID: "term-1",
			Metadata:   metaEnc,
			AgentState: &codexJSON,
			UpdatedAt:  200,
		},
	}

	got, ok := selectExistingSessionForAgent(sessions, "/tmp/proj", "term-1", "codex")
	require.True(t, ok)
	require.Equal(t, "s-codex", got.id)
	require.Equal(t, codexJSON, got.agentStateJSON)
	require.Equal(t, "term-1:codex", got.tag)
}

func TestSelectExistingSessionForAgentPrefersMostRecentActivity(t *testing.T) {
	t.Parallel()

	meta := types.Metadata{Path: "/tmp/proj", Flavor: "codex"}
	metaRaw, err := json.Marshal(meta)
	require.NoError(t, err)
	metaEnc := base64.StdEncoding.EncodeToString(metaRaw)

	state := types.AgentState{AgentType: "codex", ResumeToken: "thread-1"}
	stateRaw, err := json.Marshal(state)
	require.NoError(t, err)
	stateJSON := string(stateRaw)

	sessions := []listSessionItem{
		{
			ID:         "old",
			TerminalID: "term-1",
			Metadata:   metaEnc,
			AgentState: &stateJSON,
			UpdatedAt:  10,
			ActiveAt:   500,
		},
		{
			ID:         "newer-but-inactive",
			TerminalID: "term-1",
			Metadata:   metaEnc,
			AgentState: &stateJSON,
			UpdatedAt:  600,
			ActiveAt:   0,
		},
	}

	got, ok := selectExistingSessionForAgent(sessions, "/tmp/proj", "term-1", "codex")
	require.True(t, ok)
	require.Equal(t, "newer-but-inactive", got.id)
}
