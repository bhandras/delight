package websocket

import (
	"context"

	"github.com/bhandras/delight/server/internal/websocket/handlers"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
)

func (s *SocketIOServer) handleConnection(client *socket.Socket, deps handlers.Deps) {
	socketID := string(client.Id())

	logger.Infof("Socket.IO connection attempt (socket ID: %s)", socketID)

	handshake := client.Handshake()

	authMap := handshake.Auth
	if len(authMap) == 0 {
		logger.Warnf("Socket.IO missing auth data (socket %s)", socketID)
		client.Emit("error", map[string]string{"message": "Missing authentication data"})
		client.Disconnect(true)
		return
	}

	var authPayload protocolwire.SocketAuthPayload
	if err := decodeAny(authMap, &authPayload); err != nil {
		logger.Warnf("Socket.IO invalid auth data (socket %s): %v", socketID, err)
		client.Emit("error", map[string]string{"message": "Invalid authentication data"})
		client.Disconnect(true)
		return
	}

	// Do not log the full handshake auth payload; it contains a bearer token.
	logger.Tracef(
		"Socket.IO handshake: clientType=%s sessionId=%s terminalId=%s socketId=%s",
		authPayload.ClientType,
		authPayload.SessionID,
		authPayload.TerminalID,
		socketID,
	)

	handshakeAuth, err := handlers.ValidateSocketAuthPayload(authPayload)
	if err != nil {
		logger.Warnf("Socket.IO handshake auth rejected (socket %s): %v", socketID, err)
		client.Emit("error", map[string]string{"message": err.Error()})
		client.Disconnect(true)
		return
	}

	claims, err := s.jwtManager.VerifyToken(handshakeAuth.Token)
	if err != nil {
		logger.Warnf("Socket.IO invalid token (socket %s): %v", socketID, err)
		client.Emit("error", map[string]string{"message": "Invalid authentication token"})
		client.Disconnect(true)
		return
	}

	userID := claims.Subject
	logger.Debugf("Socket.IO token verified: userID=%s, clientType=%s, sessionId=%s, terminalId=%s, socketId=%s",
		userID, handshakeAuth.ClientType, handshakeAuth.SessionID, handshakeAuth.TerminalID, socketID)

	socketData := &SocketData{
		UserID:     userID,
		ClientType: handshakeAuth.ClientType,
		SessionID:  handshakeAuth.SessionID,
		TerminalID: handshakeAuth.TerminalID,
		Socket:     client,
	}
	s.socketData.Store(socketID, socketData)

	logger.Infof("Socket.IO client ready (user: %s, clientType: %s)", userID, handshakeAuth.ClientType)

	if handshakeAuth.ClientType == "terminal-scoped" && handshakeAuth.TerminalID != "" {
		result := handlers.ConnectTerminalScoped(
			context.Background(),
			deps,
			handlers.NewAuthContext(userID, handshakeAuth.ClientType, socketID),
			handshakeAuth.TerminalID,
		)
		s.emitHandlerUpdates(socketID, result)
	}

	s.registerClientHandlers(client, deps, socketID)
}
