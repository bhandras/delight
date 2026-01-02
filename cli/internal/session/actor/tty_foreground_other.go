//go:build !darwin && !linux

package actor

// ensureTTYForegroundSelf is a no-op on unsupported platforms.
func ensureTTYForegroundSelf() {}
