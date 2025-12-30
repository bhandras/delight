package wire

// AccessKeyGetRequest is the client -> server payload for "access-key-get".
type AccessKeyGetRequest struct {
	// SessionID is the session id for the lookup.
	SessionID string `json:"sessionId"`
	// MachineID is the machine id for the lookup.
	MachineID string `json:"machineId"`
}
