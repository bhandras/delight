package sdk

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestControlledByUserFromAgentStateJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		agentJSON string
		wantValue bool
		wantOK    bool
	}{
		{name: "empty", agentJSON: "", wantValue: true, wantOK: false},
		{name: "invalid", agentJSON: "not-json", wantValue: true, wantOK: false},
		{name: "missingField", agentJSON: `{"requests":{}}`, wantValue: true, wantOK: false},
		{name: "true", agentJSON: `{"controlledByUser":true}`, wantValue: true, wantOK: true},
		{name: "false", agentJSON: `{"controlledByUser":false}`, wantValue: false, wantOK: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotValue, gotOK := controlledByUserFromAgentStateJSON(tt.agentJSON)
			require.Equal(t, tt.wantValue, gotValue)
			require.Equal(t, tt.wantOK, gotOK)
		})
	}
}

func TestDeriveSessionUI_UsesCachedWhenAgentStateInvalid(t *testing.T) {
	t.Parallel()

	now := int64(123)
	connected := true
	active := true

	cached := &sessionFSMState{
		state:            "remote",
		online:           true,
		controlledByUser: false,
		connected:        true,
		updatedAt:        42,
		fetchedAt:        100,
	}

	fsm, ui := deriveSessionUI(now, connected, active, false, "not-json", cached)

	require.Equal(t, "remote", fsm.state)
	require.False(t, fsm.controlledByUser)
	require.Equal(t, true, ui["online"])
	require.Equal(t, "remote", ui["mode"])
}

func TestDeriveSessionUI_DefaultsToLocalWhenUnknown(t *testing.T) {
	t.Parallel()

	now := int64(456)
	connected := true
	active := true

	fsm, ui := deriveSessionUI(now, connected, active, false, "not-json", nil)

	require.Equal(t, "local", fsm.state)
	require.True(t, fsm.controlledByUser)
	require.Equal(t, true, ui["online"])
	require.Equal(t, "local", ui["mode"])
}

func TestDeriveSessionUI_ModeAndWorking(t *testing.T) {
	t.Parallel()

	now := int64(1)

	_, ui := deriveSessionUI(now, true, true, true, `{"controlledByUser":false}`, nil)
	require.Equal(t, "remote", ui["mode"])
	require.Equal(t, true, ui["working"])

	_, ui = deriveSessionUI(now, true, true, false, `{"controlledByUser":true}`, nil)
	require.Equal(t, "local", ui["mode"])
	require.Equal(t, false, ui["working"])
}

func TestComputeSessionFSM_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		connected        bool
		active           bool
		controlledByUser bool
		wantState        string
	}{
		{name: "disconnected", connected: false, active: true, controlledByUser: true, wantState: "disconnected"},
		{name: "offline", connected: true, active: false, controlledByUser: true, wantState: "offline"},
		{name: "local", connected: true, active: true, controlledByUser: true, wantState: "local"},
		{name: "remote", connected: true, active: true, controlledByUser: false, wantState: "remote"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsm, ui := deriveSessionUI(123, tt.connected, tt.active, false, `{"controlledByUser":`+boolToJSON(tt.controlledByUser)+`}`, nil)
			require.Equal(t, tt.wantState, fsm.state)
			require.Equal(t, tt.wantState != "disconnected", ui["connected"])
			require.Equal(t, tt.active, ui["online"])
			if tt.wantState == "local" || tt.wantState == "remote" {
				require.Equal(t, tt.wantState, ui["mode"])
			} else {
				require.Equal(t, "", ui["mode"])
			}
			require.Equal(t, false, ui["switching"])
			require.Equal(t, "", ui["transition"])
		})
	}
}

func TestDeriveSessionUI_SwitchingDisablesActions(t *testing.T) {
	t.Parallel()

	now := int64(1_000)
	cached := &sessionFSMState{
		state:       "local",
		online:      true,
		connected:   true,
		switching:   true,
		transition:  "to-remote",
		switchingAt: now - 50,
	}

	fsm, ui := deriveSessionUI(now, true, true, false, `{"controlledByUser":true}`, cached)

	require.Equal(t, "local", fsm.state)
	require.Equal(t, "local", ui["mode"])
	require.Equal(t, true, ui["switching"])
	require.Equal(t, "to-remote", ui["transition"])
}

func TestDeriveSessionUI_SwitchingTTLExpires(t *testing.T) {
	t.Parallel()

	now := int64(100_000)
	cached := &sessionFSMState{
		state:       "local",
		online:      true,
		connected:   true,
		switching:   true,
		transition:  "to-remote",
		switchingAt: now - 20_000, // > 15s TTL
	}

	_, ui := deriveSessionUI(now, true, true, false, `{"controlledByUser":true}`, cached)
	require.Equal(t, false, ui["switching"])
	require.Equal(t, "", ui["transition"])
	require.Equal(t, "local", ui["mode"])
}

func TestDeriveSessionUI_SwitchingKeepsPreviousUIState(t *testing.T) {
	t.Parallel()

	now := int64(10_000)
	cached := &sessionFSMState{
		state:       "remote",
		online:      true,
		connected:   true,
		switching:   true,
		transition:  "to-local",
		switchingAt: now - 50,
	}

	_, ui := deriveSessionUI(now, true, true, false, `{"controlledByUser":true}`, cached)
	require.Equal(t, "remote", ui["mode"])
	require.Equal(t, true, ui["switching"])
	require.Equal(t, "to-local", ui["transition"])
}

func TestHandleUpdate_UpdateSession_AgentStateUpdatesFSM(t *testing.T) {
	t.Parallel()

	c := NewClient("http://example.invalid")
	sessionID := "s1"

	c.sessionFSM[sessionID] = sessionFSMState{
		state:            "remote",
		online:           true,
		connected:        true,
		controlledByUser: false,
		switching:        true,
		transition:       "to-local",
		switchingAt:      time.Now().UnixMilli(),
	}

	c.handleUpdate(map[string]interface{}{
		"body": map[string]interface{}{
			"t":  "update-session",
			"id": sessionID,
			"agentState": map[string]interface{}{
				"value": `{"controlledByUser":true}`,
			},
		},
	})

	got := c.sessionFSM[sessionID]
	require.Equal(t, "local", got.state)
	require.True(t, got.controlledByUser)
	require.True(t, got.online)
	require.True(t, got.connected)
	require.False(t, got.switching)
	require.Equal(t, "", got.transition)
}

func TestApplyAgentStateToSessionFSM_EmitsSessionUIUpdate(t *testing.T) {
	t.Parallel()

	c := NewClient("http://example.invalid")
	sessionID := "s1"
	listener := newCaptureListener()
	c.SetListener(listener)

	c.sessionFSM[sessionID] = sessionFSMState{
		state:            "remote",
		online:           true,
		connected:        true,
		controlledByUser: false,
		uiJSON:           "",
	}

	c.applyAgentStateToSessionFSM(sessionID, `{"controlledByUser":true}`)

	listener.waitUpdate(t)
	listener.mu.Lock()
	got := listener.lastUpdate
	listener.mu.Unlock()

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	body, _ := decoded["body"].(map[string]any)
	require.NotNil(t, body)
	require.Equal(t, "session-ui", body["t"])
	require.Equal(t, sessionID, body["sid"])

	ui, _ := body["ui"].(map[string]any)
	require.NotNil(t, ui)
	require.Equal(t, true, ui["online"])
	require.Equal(t, "local", ui["mode"])
}

func boolToJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
