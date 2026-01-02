package wire

// Null marshals as JSON null.
//
// This is useful when the wire shape intentionally includes a key with a null
// value (rather than omitting the key entirely).
type Null struct{}

func (Null) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// VersionedAny is a versioned wrapper where the value may be arbitrary JSON.
type VersionedAny struct {
	// Value is the arbitrary JSON value.
	Value any `json:"value"`
	// Version is the monotonic version.
	Version int64 `json:"version"`
}

// EphemeralMachineActivityPayload is a user-scoped "ephemeral" event payload
// for machine activity.
type EphemeralMachineActivityPayload struct {
	// Type must be "machine-activity".
	Type string `json:"type"`
	// ID is the machine id.
	ID string `json:"id"`
	// Active is true when the machine is active.
	Active bool `json:"active"`
	// ActiveAt is a wall-clock timestamp in milliseconds since epoch.
	ActiveAt int64 `json:"activeAt"`
}

// EphemeralUsagePayload is a user-scoped "ephemeral" event payload for usage
// reporting.
type EphemeralUsagePayload struct {
	// Type must be "usage".
	Type string `json:"type"`
	// ID is the session id.
	ID string `json:"id"`
	// Key identifies the usage source.
	Key string `json:"key"`
	// Tokens is a JSON object containing token counts.
	Tokens map[string]any `json:"tokens"`
	// Cost is a JSON object containing cost information.
	Cost map[string]any `json:"cost"`
	// Timestamp is when the report was recorded, in ms since epoch.
	Timestamp int64 `json:"timestamp"`
}

// UpdateBodyNewSession is the body for `t == "new-session"`.
type UpdateBodyNewSession struct {
	T                 string  `json:"t"`
	ID                string  `json:"id"`
	Seq               int64   `json:"seq"`
	Metadata          string  `json:"metadata"`
	MetadataVersion   int64   `json:"metadataVersion"`
	AgentState        *string `json:"agentState"`
	AgentStateVersion int64   `json:"agentStateVersion"`
	DataEncryptionKey *string `json:"dataEncryptionKey"`
	Active            bool    `json:"active"`
	ActiveAt          int64   `json:"activeAt"`
	CreatedAt         int64   `json:"createdAt"`
	UpdatedAt         int64   `json:"updatedAt"`
}

// UpdateBodyDeleteSession is the body for `t == "delete-session"`.
type UpdateBodyDeleteSession struct {
	T   string `json:"t"`
	SID string `json:"sid"`
}

// UpdateBodyNewMachine is the body for `t == "new-machine"`.
type UpdateBodyNewMachine struct {
	T                  string  `json:"t"`
	MachineID          string  `json:"machineId"`
	Seq                int64   `json:"seq"`
	Metadata           string  `json:"metadata"`
	MetadataVersion    int64   `json:"metadataVersion"`
	DaemonState        *string `json:"daemonState"`
	DaemonStateVersion int64   `json:"daemonStateVersion"`
	DataEncryptionKey  *string `json:"dataEncryptionKey"`
	Active             bool    `json:"active"`
	ActiveAt           int64   `json:"activeAt"`
	CreatedAt          int64   `json:"createdAt"`
	UpdatedAt          int64   `json:"updatedAt"`
}

// UpdateBodyUpdateMachine is the body for `t == "update-machine"`.
type UpdateBodyUpdateMachine struct {
	T           string           `json:"t"`
	MachineID   string           `json:"machineId"`
	Metadata    *VersionedString `json:"metadata,omitempty"`
	DaemonState *VersionedString `json:"daemonState,omitempty"`
}

// UpdateBodyUpdateAccount is the body for `t == "update-account"`.
//
// This body is intentionally permissive: different endpoints update different
// subsets of fields.
type UpdateBodyUpdateAccount struct {
	T         string        `json:"t"`
	ID        string        `json:"id"`
	Settings  *VersionedAny `json:"settings,omitempty"`
	FirstName *string       `json:"firstName,omitempty"`
	LastName  *string       `json:"lastName,omitempty"`
	Username  *string       `json:"username,omitempty"`
	Avatar    any           `json:"avatar,omitempty"`
}

// UpdateBodyNewFeedPost is the body for `t == "new-feed-post"`.
type UpdateBodyNewFeedPost struct {
	T         string `json:"t"`
	ID        any    `json:"id"`
	Body      any    `json:"body"`
	Cursor    any    `json:"cursor"`
	CreatedAt any    `json:"createdAt"`
	RepeatKey any    `json:"repeatKey"`
}

// UpdateBodyKVBatchUpdate is the body for `t == "kv-batch-update"`.
type UpdateBodyKVBatchUpdate struct {
	T       string     `json:"t"`
	Changes []KVChange `json:"changes"`
}

// KVChange is a single kv change entry for kv-batch-update.
type KVChange struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Version int64  `json:"version"`
}

// UpdateBodyNewArtifact is the body for `t == "new-artifact"`.
type UpdateBodyNewArtifact struct {
	T                 string `json:"t"`
	ArtifactID        string `json:"artifactId"`
	Seq               int64  `json:"seq"`
	Header            string `json:"header"`
	HeaderVersion     int64  `json:"headerVersion"`
	Body              string `json:"body"`
	BodyVersion       int64  `json:"bodyVersion"`
	DataEncryptionKey string `json:"dataEncryptionKey"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
}

// UpdateBodyUpdateArtifact is the body for `t == "update-artifact"`.
type UpdateBodyUpdateArtifact struct {
	T          string           `json:"t"`
	ArtifactID string           `json:"artifactId"`
	Header     *VersionedString `json:"header,omitempty"`
	Body       *VersionedString `json:"body,omitempty"`
}

// UpdateBodyDeleteArtifact is the body for `t == "delete-artifact"`.
type UpdateBodyDeleteArtifact struct {
	T          string `json:"t"`
	ArtifactID string `json:"artifactId"`
}
