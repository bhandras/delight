package runtime

// Event is an input to the session runtime loop.
type Event interface {
	isEvent()
}

// SetWorkingEvent toggles the "working" state for a session.
type SetWorkingEvent struct {
	// Working is the new working state.
	Working bool

	// AtMs is a wall-clock timestamp in milliseconds since epoch.
	AtMs int64
}

func (SetWorkingEvent) isEvent() {}
