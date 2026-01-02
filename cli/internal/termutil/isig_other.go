//go:build !darwin && !linux

package termutil

// EnableISIG is a no-op on unsupported platforms.
func EnableISIG(fd int) {
	_ = fd
}
