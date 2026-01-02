//go:build !darwin && !linux

package actor

// enableISIG is a no-op on unsupported platforms.
func enableISIG(fd int) {
	_ = fd
}
