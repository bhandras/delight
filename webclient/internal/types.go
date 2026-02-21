package webclient

import "github.com/bhandras/delight/shared/webapi"

const (
	// defaultStateDirName is the runtime state folder under the user's home.
	defaultStateDirName = ".delight/webclient"
	// stateFileName is the persisted JSON file for bridge runtime state.
	stateFileName = "state.json"
	// maxLogEntries is the in-memory debug log capacity.
	maxLogEntries = 2000
	// maxEventBuffer keeps enough events for typical reconnect windows.
	maxEventBuffer = 2000
	// sseKeepAlivePeriod keeps intermediaries from timing out idle streams.
	sseKeepAlivePeriod = 20
	// defaultMessagePageLimit is used when a messages limit is not provided.
	defaultMessagePageLimit = 50
)

// Config holds runtime configuration for the web bridge process.
type Config struct {
	// ListenAddr is the HTTP listen address for the bridge.
	ListenAddr string
	// ServerURL is the default Delight server base URL.
	ServerURL string
	// StatePath is the optional path for persisted runtime state.
	StatePath string
	// APIToken is an optional static bearer token for bridge API auth.
	APIToken string
	// OriginsCSV is a comma-separated browser Origin allowlist.
	OriginsCSV string
}

// persistentState stores bridge runtime state across restarts.
type persistentState struct {
	ServerURL    string             `json:"serverURL"`
	MasterKey    string             `json:"masterKey"`
	Token        string             `json:"token"`
	Preferences  webapi.Preferences `json:"preferences"`
	LastSession  string             `json:"lastSession"`
	LastTerminal string             `json:"lastTerminal"`
}

// accountCreateRequest defines the request body for account creation.
type accountCreateRequest struct {
	ServerURL string `json:"serverURL"`
	MasterKey string `json:"masterKey"`
}

// accountConnectRequest defines the request body for account reconnect.
type accountConnectRequest struct {
	ServerURL string `json:"serverURL"`
	MasterKey string `json:"masterKey"`
}

// serverConfigRequest defines server URL update payload.
type serverConfigRequest struct {
	ServerURL string `json:"serverURL"`
}

// pairTerminalRequest defines the pairing request body.
type pairTerminalRequest struct {
	QRURL string `json:"qrURL"`
}

// approveTerminalRequest defines explicit terminal approval request body.
type approveTerminalRequest struct {
	TerminalPublicKey string `json:"terminalPublicKey"`
}

// sendMessageRequest defines session send request body.
type sendMessageRequest struct {
	Text          string `json:"text"`
	RawRecordJSON string `json:"rawRecordJSON"`
	LocalID       string `json:"localID"`
}

// switchRequest defines take-control mode switch body.
type switchRequest struct {
	Mode string `json:"mode"`
}

// permissionRequest defines permission decision body.
type permissionRequest struct {
	RequestID string `json:"requestId"`
	Allow     bool   `json:"allow"`
	Message   string `json:"message"`
}

// agentConfigRequest defines agent configuration update body.
type agentConfigRequest struct {
	Model           *string `json:"model"`
	PermissionMode  *string `json:"permissionMode"`
	ReasoningEffort *string `json:"reasoningEffort"`
}

// agentCapabilitiesRequest defines capability query options.
type agentCapabilitiesRequest struct {
	Model string `json:"model"`
}

// rawUserMessageRecord mirrors the CLI user message envelope for SendMessage.
type rawUserMessageRecord struct {
	Role    string                   `json:"role"`
	Content rawUserMessageRecordBody `json:"content"`
}

// rawUserMessageRecordBody stores the message block payload.
type rawUserMessageRecordBody struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// apiSuccess wraps simple success responses.
type apiSuccess struct {
	Success bool `json:"success"`
}

// streamPublishPayload is the payload wrapper sent for SDK updates.
type streamPublishPayload struct {
	SessionID string      `json:"sessionID,omitempty"`
	Update    interface{} `json:"update,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	Message   string      `json:"message,omitempty"`
}
