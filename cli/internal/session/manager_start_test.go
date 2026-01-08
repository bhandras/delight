package session

import (
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
