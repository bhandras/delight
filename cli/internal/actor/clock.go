package actor

import "time"

// Clock provides a testable time source.
//
// Reducers should remain deterministic and must not call a Clock directly.
// Instead, runtimes should use Clock and inject timestamps via events.
type Clock interface {
	Now() time.Time
}

// RealClock is a production Clock implementation backed by time.Now.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

