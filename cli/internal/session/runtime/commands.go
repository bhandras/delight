package runtime

// Command is an output from the session runtime loop.
//
// Commands are effects that must be executed by a caller (e.g. the session
// Manager). Keeping these as data makes the reducer deterministic and testable.
type Command interface {
	isCommand()
}

// EmitActivityCommand requests emitting an ephemeral "activity" event.
type EmitActivityCommand struct {
	// SessionID identifies which session to associate with the event.
	SessionID string

	// Working indicates whether the session should show as working.
	Working bool

	// Active indicates whether the session is active.
	Active bool

	// ActiveAtMs is a wall-clock timestamp in milliseconds since epoch.
	ActiveAtMs int64
}

func (EmitActivityCommand) isCommand() {}
