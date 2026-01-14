package runtime

// State holds the in-memory session runtime state.
//
// It is updated only by the runtime loop and is intentionally small so it can
// be snapshotted and tested deterministically.
type State struct {
	// SessionID identifies the Delight session this runtime instance belongs to.
	SessionID string

	// Working is true when the agent is currently working on a request.
	Working bool
}
