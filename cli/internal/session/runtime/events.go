package runtime

// Event is an input to the session runtime loop.
type Event interface {
	isEvent()
}

// SetThinkingEvent toggles the "thinking" state for a session.
type SetThinkingEvent struct {
	// Thinking is the new thinking state.
	Thinking bool

	// AtMs is a wall-clock timestamp in milliseconds since epoch.
	AtMs int64
}

func (SetThinkingEvent) isEvent() {}
