package handlers

import (
	"errors"
	"fmt"

	protocolwire "github.com/bhandras/delight/shared/wire"
)

// SocketHandshake is the validated Socket.IO handshake auth payload.
type SocketHandshake struct {
	Token      string
	ClientType string
	SessionID  string
	TerminalID string
}

// ValidateSocketAuthPayload validates the Socket.IO handshake auth payload and
// applies server-side defaults (e.g. default client type).
func ValidateSocketAuthPayload(auth protocolwire.SocketAuthPayload) (SocketHandshake, error) {
	if auth.Token == "" {
		return SocketHandshake{}, errors.New("missing authentication token")
	}

	clientType := auth.ClientType
	if clientType == "" {
		clientType = "user-scoped"
	}

	switch clientType {
	case "user-scoped":
	case "session-scoped":
		if auth.SessionID == "" {
			return SocketHandshake{}, errors.New("session ID required for session-scoped clients")
		}
	case "terminal-scoped":
		if auth.TerminalID == "" {
			return SocketHandshake{}, errors.New("terminal ID required for terminal-scoped clients")
		}
	default:
		return SocketHandshake{}, fmt.Errorf("invalid client type: %s", clientType)
	}

	return SocketHandshake{
		Token:      auth.Token,
		ClientType: clientType,
		SessionID:  auth.SessionID,
		TerminalID: auth.TerminalID,
	}, nil
}
