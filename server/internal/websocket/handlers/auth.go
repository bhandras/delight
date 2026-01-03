package handlers

// AuthContext carries authenticated socket identity information into handler
// functions. It intentionally excludes transport-specific types.
type AuthContext struct {
	userID     string
	clientType string
	socketID   string
}

// NewAuthContext constructs an AuthContext for a single socket event.
func NewAuthContext(userID, clientType, socketID string) AuthContext {
	return AuthContext{
		userID:     userID,
		clientType: clientType,
		socketID:   socketID,
	}
}

// UserID returns the authenticated account id.
func (a AuthContext) UserID() string {
	return a.userID
}

// ClientType returns the logical client type ("session-scoped", "user-scoped",
// or "terminal-scoped").
func (a AuthContext) ClientType() string {
	return a.clientType
}

// SocketID returns the caller socket id.
func (a AuthContext) SocketID() string {
	return a.socketID
}
