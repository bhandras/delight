//go:build !darwin && !linux

package termutil

// EnsureTTYForegroundSelf is a no-op on unsupported platforms.
func EnsureTTYForegroundSelf() {}
