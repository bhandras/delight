package actor

import (
	"reflect"

	"github.com/bhandras/delight/cli/internal/agentengine"
)

// isNilInterface returns true if the provided interface value is nil, including
// the common case where the interface is non-nil but holds a nil concrete
// pointer.
func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// shouldMutateTTY reports whether the runtime should perform terminal mutations
// (tty mode resets, raw /dev/tty takeback watcher) for the selected agent.
//
// Fake agent sessions are frequently used in integration tests where the CLI
// process still shares the developer's controlling terminal. In those contexts,
// touching /dev/tty can leave the user's terminal in a broken state (raw mode,
// reset sequences) if the test process interrupts or kills the CLI.
func shouldMutateTTY(agent agentengine.AgentType) bool {
	switch agent {
	case agentengine.AgentFake, agentengine.AgentACP:
		return false
	default:
		return true
	}
}
