package sdk

import (
	"encoding/json"
	"time"
)

const (
	// sessionFSMStaleAfter defines how long cached session FSM snapshots remain
	// valid before the SDK refreshes them via ListSessions.
	sessionFSMStaleAfter = 2 * time.Second

	// sessionSwitchingTTL defines the maximum time the UI should display a
	// "switching" state before expiring it (to avoid stuck spinners).
	sessionSwitchingTTL = 15 * time.Second
)

// sessionFSMState is the SDK-owned, derived per-session control state.
//
// It is computed from:
// - connectivity (socket connected)
// - session activity (CLI online)
// - agentState.controlledByUser (desktop vs phone control)
//
// UI layers should treat this as the source of truth.
type sessionFSMState struct {
	state            string
	online           bool
	working          bool
	workingAt        int64
	controlledByUser bool
	connected        bool
	switching        bool
	transition       string
	switchingAt      int64
	uiJSON           string
	updatedAt        int64
	fetchedAt        int64
}

// controlledByUserFromAgentStateJSON extracts controlledByUser from the
// plaintext agentState JSON string.
func controlledByUserFromAgentStateJSON(agentState string) (value bool, ok bool) {
	if agentState == "" {
		return true, false
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(agentState), &decoded); err != nil {
		return true, false
	}
	if v, ok := decoded["controlledByUser"].(bool); ok {
		return v, true
	}
	return true, false
}

// computeSessionFSM derives a stable UI state string from connectivity and
// control ownership.
func computeSessionFSM(connected bool, active bool, controlledByUser bool) sessionFSMState {
	switch {
	case !connected:
		return sessionFSMState{state: "disconnected", connected: false, online: active, controlledByUser: controlledByUser}
	case !active:
		return sessionFSMState{state: "offline", connected: true, online: false, controlledByUser: controlledByUser}
	case controlledByUser:
		return sessionFSMState{state: "local", connected: true, online: true, controlledByUser: true}
	default:
		return sessionFSMState{state: "remote", connected: true, online: true, controlledByUser: false}
	}
}

// deriveSessionUI computes the SDK-derived session FSM snapshot and a JSON-ish
// UI map for clients.
//
// The returned ui map is intentionally view-friendly and avoids requiring UIs
// to re-implement control logic.
func deriveSessionUI(
	now int64,
	connected bool,
	active bool,
	working bool,
	agentState string,
	cached *sessionFSMState,
) (sessionFSMState, map[string]any) {
	controlledByUser, ok := controlledByUserFromAgentStateJSON(agentState)
	if !ok && cached != nil {
		controlledByUser = cached.controlledByUser
	}

	workingAt := int64(0)
	if cached != nil {
		workingAt = cached.workingAt
	}
	if working {
		if cached == nil || !cached.working {
			workingAt = now
		}
	} else if cached != nil && cached.working {
		// ListSessions (and other sources) can briefly report working=false even if the
		// client has recently observed a turn in-flight via activity ephemerals.
		// Keep working true for a short window to avoid UI flicker.
		if workingAt > 0 && time.UnixMilli(now).Sub(time.UnixMilli(workingAt)) <= sessionFSMStaleAfter {
			working = true
		}
	}

	fsm := computeSessionFSM(connected, active, controlledByUser)
	fsm.fetchedAt = now
	fsm.working = working
	fsm.workingAt = workingAt
	if cached != nil {
		fsm.updatedAt = cached.updatedAt
		fsm.switching = cached.switching
		fsm.transition = cached.transition
		fsm.switchingAt = cached.switchingAt
	}

	// mode is the control owner; it is only defined while the session is online.
	mode := modeFromState(fsm.state)

	// Transition UX: keep a stable ui.state value (local/remote/offline/etc) and
	// expose switching/transition flags orthogonally. This makes UI layers pure
	// views: they can show spinners/disable actions without re-implementing the
	// control FSM.
	switching := false
	transition := ""
	if cached != nil && cached.switching {
		switchingAt := time.UnixMilli(cached.switchingAt)
		if cached.switchingAt <= 0 || time.UnixMilli(now).Sub(switchingAt) <= sessionSwitchingTTL {
			switching = true
			transition = cached.transition
			// While switching, keep the last stable control mode so clients don't
			// flicker between local/remote during network lag.
			if cached.state != "" {
				mode = modeFromState(cached.state)
			}
		}
	}

	if !fsm.online {
		mode = ""
	}

	ui := map[string]any{
		"connected":  fsm.connected,
		"online":     fsm.online,
		"working":    working,
		"mode":       mode,
		"switching":  switching,
		"transition": transition,
	}

	return fsm, ui
}

func modeFromState(state string) string {
	switch state {
	case "local", "remote":
		return state
	default:
		return ""
	}
}
