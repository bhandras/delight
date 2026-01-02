package wire

// ArtifactReadRequest is the client -> server payload for "artifact-read".
type ArtifactReadRequest struct {
	ArtifactID string `json:"artifactId"`
}

// ArtifactCreateRequest is the client -> server payload for "artifact-create".
type ArtifactCreateRequest struct {
	// ID is the artifact id.
	ID string `json:"id"`
	// Header is base64-encoded bytes.
	Header string `json:"header"`
	// Body is base64-encoded bytes.
	Body string `json:"body"`
	// DataEncryptionKey is base64-encoded bytes.
	DataEncryptionKey string `json:"dataEncryptionKey"`
}

// ArtifactDeleteRequest is the client -> server payload for "artifact-delete".
type ArtifactDeleteRequest struct {
	ArtifactID string `json:"artifactId"`
}

// ArtifactUpdatePart is an optional update for a single artifact field.
type ArtifactUpdatePart struct {
	// Data is base64-encoded bytes.
	Data string `json:"data"`
	// ExpectedVersion is the optimistic concurrency version.
	ExpectedVersion int64 `json:"expectedVersion"`
}

// ArtifactUpdateRequest is the client -> server payload for "artifact-update".
type ArtifactUpdateRequest struct {
	ArtifactID string              `json:"artifactId"`
	Header     *ArtifactUpdatePart `json:"header,omitempty"`
	Body       *ArtifactUpdatePart `json:"body,omitempty"`
}
