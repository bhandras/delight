package sdk

import "testing"

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
			if gotValue != tt.wantValue || gotOK != tt.wantOK {
				t.Fatalf("got (value=%v ok=%v), want (value=%v ok=%v)", gotValue, gotOK, tt.wantValue, tt.wantOK)
			}
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
		active:           true,
		controlledByUser: false,
		connected:        true,
		updatedAt:        42,
		fetchedAt:        100,
	}

	fsm, ui := deriveSessionUI(now, connected, active, "not-json", cached)

	if fsm.state != "remote" {
		t.Fatalf("fsm.state=%q, want %q", fsm.state, "remote")
	}
	if fsm.controlledByUser {
		t.Fatalf("fsm.controlledByUser=true, want false")
	}
	if uiState, _ := ui["state"].(string); uiState != "remote" {
		t.Fatalf("ui.state=%q, want %q", uiState, "remote")
	}
	if canSend, _ := ui["canSend"].(bool); !canSend {
		t.Fatalf("ui.canSend=false, want true")
	}
	if canTake, _ := ui["canTakeControl"].(bool); canTake {
		t.Fatalf("ui.canTakeControl=true, want false")
	}
}

func TestDeriveSessionUI_DefaultsToLocalWhenUnknown(t *testing.T) {
	t.Parallel()

	now := int64(456)
	connected := true
	active := true

	fsm, ui := deriveSessionUI(now, connected, active, "not-json", nil)

	if fsm.state != "local" {
		t.Fatalf("fsm.state=%q, want %q", fsm.state, "local")
	}
	if !fsm.controlledByUser {
		t.Fatalf("fsm.controlledByUser=false, want true")
	}
	if uiState, _ := ui["state"].(string); uiState != "local" {
		t.Fatalf("ui.state=%q, want %q", uiState, "local")
	}
}

