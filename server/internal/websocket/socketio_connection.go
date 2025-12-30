package websocket

import (
	"context"
	"log"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/websocket/handlers"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
)

func (s *SocketIOServer) handleConnection(client *socket.Socket, deps handlers.Deps) {
	socketID := string(client.Id())

	log.Printf("🔌 Socket.IO connection attempt! Socket ID: %s", socketID)

	handshake := client.Handshake()
	log.Printf("  Headers: %+v", handshake.Headers)
	log.Printf("  Query: %+v", handshake.Query)
	log.Printf("  Auth: %+v", handshake.Auth)

	authMap := handshake.Auth
	if authMap == nil || len(authMap) == 0 {
		log.Printf("❌ No auth data provided (socket %s)", socketID)
		client.Emit("error", map[string]string{"message": "Missing authentication data"})
		client.Disconnect(true)
		return
	}

	var authPayload protocolwire.SocketAuthPayload
	if err := decodeAny(authMap, &authPayload); err != nil {
		log.Printf("❌ Invalid auth data provided (socket %s): %v", socketID, err)
		client.Emit("error", map[string]string{"message": "Invalid authentication data"})
		client.Disconnect(true)
		return
	}

	handshakeAuth, err := handlers.ValidateSocketAuthPayload(authPayload)
	if err != nil {
		log.Printf("❌ Handshake auth rejected (socket %s): %v", socketID, err)
		client.Emit("error", map[string]string{"message": err.Error()})
		client.Disconnect(true)
		return
	}

	claims, err := s.jwtManager.VerifyToken(handshakeAuth.Token)
	if err != nil {
		log.Printf("❌ Invalid token provided (socket %s): %v", socketID, err)
		client.Emit("error", map[string]string{"message": "Invalid authentication token"})
		client.Disconnect(true)
		return
	}

	userID := claims.Subject
	log.Printf("✅ Token verified: userID=%s, clientType=%s, sessionId=%s, machineId=%s, socketId=%s",
		userID, handshakeAuth.ClientType, handshakeAuth.SessionID, handshakeAuth.MachineID, socketID)

	socketData := &SocketData{
		UserID:     userID,
		ClientType: handshakeAuth.ClientType,
		SessionID:  handshakeAuth.SessionID,
		MachineID:  handshakeAuth.MachineID,
		Socket:     client,
	}
	s.socketData.Store(socketID, socketData)

	log.Printf("✅ Socket.IO client ready (user: %s, clientType: %s)", userID, handshakeAuth.ClientType)

	if handshakeAuth.ClientType == "machine-scoped" && handshakeAuth.MachineID != "" {
		result := handlers.ConnectMachineScoped(
			context.Background(),
			deps,
			handlers.NewAuthContext(userID, handshakeAuth.ClientType, socketID),
			handshakeAuth.MachineID,
		)
		s.emitHandlerUpdates(socketID, result)
	}

	s.registerClientHandlers(client, deps, socketID)
}
