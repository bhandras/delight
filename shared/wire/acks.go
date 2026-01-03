package wire

// ResultAck is the minimal ACK response shape used by Socket.IO handlers.
type ResultAck struct {
	// Result is one of "success", "error", or "version-mismatch".
	Result string `json:"result"`
	// Message is an optional error annotation.
	Message string `json:"message,omitempty"`
}

// VersionedAck is an ACK response with an updated version and optional value.
//
// Different events populate different value fields.
type VersionedAck struct {
	// Result is one of "success", "error", or "version-mismatch".
	Result string `json:"result"`
	// Version is the current or updated version.
	Version int64 `json:"version,omitempty"`
	// Message is an optional error annotation.
	Message string `json:"message,omitempty"`

	// Metadata is used by session/terminal metadata updates.
	Metadata string `json:"metadata,omitempty"`
	// AgentState is used by session agentState updates.
	AgentState string `json:"agentState,omitempty"`
	// DaemonState is used by terminal daemonState updates.
	DaemonState string `json:"daemonState,omitempty"`
}

// RPCRegisteredPayload is sent after successfully registering an RPC method.
type RPCRegisteredPayload struct {
	// Method is the registered method name.
	Method string `json:"method"`
}

// RPCUnregisteredPayload is sent after successfully unregistering an RPC method.
type RPCUnregisteredPayload struct {
	// Method is the unregistered method name.
	Method string `json:"method"`
}

// RPCErrorPayload is sent when RPC registration/unregistration fails.
type RPCErrorPayload struct {
	// Type identifies the failing operation ("register", "unregister", etc).
	Type string `json:"type"`
	// Error contains an error message.
	Error string `json:"error"`
}

// RPCAck is the ACK response shape for "rpc-call".
type RPCAck struct {
	// OK indicates whether the call succeeded.
	OK bool `json:"ok"`
	// Error contains an error string when OK is false.
	Error string `json:"error,omitempty"`
	// Result contains the RPC response payload when OK is true.
	Result any `json:"result,omitempty"`
}

// ArtifactAck is the ACK response shape for artifact get/create/delete.
type ArtifactAck struct {
	// Result is one of "success", "error", or "version-mismatch".
	Result string `json:"result"`
	// Message is an optional error annotation.
	Message string `json:"message,omitempty"`
	// Artifact is present on success when relevant.
	Artifact *ArtifactInfo `json:"artifact,omitempty"`
}

// ArtifactInfo is the artifact object embedded in artifact ACK responses.
type ArtifactInfo struct {
	ID            string `json:"id"`
	Header        string `json:"header"`
	HeaderVersion int64  `json:"headerVersion"`
	Body          string `json:"body"`
	BodyVersion   int64  `json:"bodyVersion"`
	Seq           int64  `json:"seq"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// ArtifactPartMismatch is the mismatch payload for a single artifact part.
type ArtifactPartMismatch struct {
	CurrentVersion int64  `json:"currentVersion"`
	CurrentData    string `json:"currentData"`
}

// ArtifactUpdateMismatchAck is the ACK response for artifact-update version mismatches.
type ArtifactUpdateMismatchAck struct {
	Result string                `json:"result"`
	Header *ArtifactPartMismatch `json:"header,omitempty"`
	Body   *ArtifactPartMismatch `json:"body,omitempty"`
}

// ArtifactUpdateSuccessPart is the success payload for a single artifact part.
type ArtifactUpdateSuccessPart struct {
	Version int64  `json:"version"`
	Data    string `json:"data"`
}

// ArtifactUpdateSuccessAck is the ACK response for artifact-update success.
type ArtifactUpdateSuccessAck struct {
	Result string                     `json:"result"`
	Header *ArtifactUpdateSuccessPart `json:"header,omitempty"`
	Body   *ArtifactUpdateSuccessPart `json:"body,omitempty"`
}
