package wire

// CreateSessionRequest is the HTTP POST /v1/sessions request body.
type CreateSessionRequest struct {
	// Tag is the stable client-generated session tag.
	Tag string `json:"tag"`
	// Metadata is the encrypted metadata payload (base64-encoded).
	Metadata string `json:"metadata"`
}

// CreateSessionResponse is the HTTP POST /v1/sessions response body.
type CreateSessionResponse struct {
	// Session contains the created session object.
	Session CreateSessionResponseSession `json:"session"`
}

// CreateSessionResponseSession is the session object returned in a
// CreateSessionResponse.
type CreateSessionResponseSession struct {
	// ID is the server-assigned session id.
	ID string `json:"id"`
	// DataEncryptionKey is the encrypted data key (base64-encoded) when present.
	DataEncryptionKey string `json:"dataEncryptionKey,omitempty"`
}

// CreateMachineRequest is the HTTP POST /v1/machines request body.
type CreateMachineRequest struct {
	// ID is the client-stable machine id.
	ID string `json:"id"`
	// Metadata is the encrypted machine metadata payload (base64-encoded).
	Metadata string `json:"metadata"`
	// DaemonState is the encrypted daemon state payload (base64-encoded).
	DaemonState string `json:"daemonState"`
}

// CreateMachineResponse is the HTTP POST /v1/machines response body.
type CreateMachineResponse struct {
	// Machine contains the created/updated machine object.
	Machine CreateMachineResponseMachine `json:"machine"`
}

// CreateMachineResponseMachine is the machine object returned in a
// CreateMachineResponse.
type CreateMachineResponseMachine struct {
	// MetadataVersion is the current machine metadata version.
	MetadataVersion int64 `json:"metadataVersion"`
	// DaemonStateVersion is the current daemon state version.
	DaemonStateVersion int64 `json:"daemonStateVersion"`
}
