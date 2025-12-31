package actor

import "fmt"

var (
	// ErrInvalidMode is returned when a command references an unknown target mode.
	ErrInvalidMode = fmt.Errorf("invalid mode")
	// ErrNotRemote is returned when remote-only operations are requested while not in remote mode.
	ErrNotRemote = fmt.Errorf("not in remote mode")
	// ErrUnknownPermissionRequest is returned when a permission decision is submitted for a missing request id.
	ErrUnknownPermissionRequest = fmt.Errorf("unknown permission request")
)

