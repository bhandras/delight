package websocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/bhandras/delight/server/internal/models"
	sessionruntime "github.com/bhandras/delight/server/internal/session/runtime"
	"github.com/bhandras/delight/server/internal/websocket/handlers"
	pkgtypes "github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
	sockettypes "github.com/zishang520/socket.io/v3/pkg/types"
)

// SocketIOServer wraps Socket.IO server for Delight
type SocketIOServer struct {
	db         *sql.DB
	jwtManager *crypto.JWTManager
	server     *socket.Server
	socketData sync.Map // Maps socket ID to user data
	sessions   *sessionruntime.Manager
	rpc        *RPCRegistry
}

// NewSocketIOServer creates a new Socket.IO v4 server
func NewSocketIOServer(db *sql.DB, jwtManager *crypto.JWTManager) *SocketIOServer {
	// Create default server options
	opts := socket.DefaultServerOptions()

	// Configure CORS
	opts.SetCors(&sockettypes.Cors{
		Origin:      "*",
		Credentials: true,
	})

	// Set ping timeout and interval to match TypeScript server
	opts.SetPingTimeout(45 * time.Second)
	opts.SetPingInterval(15 * time.Second)

	// Set the path to match what mobile app expects (same as TypeScript server)
	opts.SetPath("/v1/updates")

	// Create Socket.IO server with options
	server := socket.NewServer(nil, opts)

	s := &SocketIOServer{
		db:         db,
		jwtManager: jwtManager,
		server:     server,
		socketData: sync.Map{},
		rpc:        NewRPCRegistry(),
	}
	s.sessions = sessionruntime.NewManager(&sessionruntime.SQLStore{
		Queries: models.New(db),
	}, s)

	// Set up event handlers
	s.setupHandlers()

	return s
}

// SocketData stores connection metadata for each socket
type SocketData struct {
	UserID     string
	ClientType string // "session-scoped", "user-scoped", or "machine-scoped"
	SessionID  string
	MachineID  string
	Socket     *socket.Socket // Reference to the socket for emitting
}

// setupHandlers configures Socket.IO event handlers
func (s *SocketIOServer) setupHandlers() {
	queries := models.New(s.db)
	handlerDeps := handlers.NewDeps(queries, queries, queries, queries, queries, time.Now, pkgtypes.NewCUID)

	// Connection handler
	s.server.On("connection", func(clients ...any) {
		client := clients[0].(*socket.Socket)

		log.Printf("🔌 Socket.IO connection attempt! Socket ID: %s", client.Id())

		// Get handshake data
		handshake := client.Handshake()
		log.Printf("  Headers: %+v", handshake.Headers)
		log.Printf("  Query: %+v", handshake.Query)
		log.Printf("  Auth: %+v", handshake.Auth)

		// Extract auth data from handshake
		// handshake.Auth is map[string]any
		authMap := handshake.Auth
		if authMap == nil || len(authMap) == 0 {
			log.Printf("❌ No auth data provided (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Missing authentication data"})
			client.Disconnect(true)
			return
		}

		var auth protocolwire.SocketAuthPayload
		if err := decodeAny(authMap, &auth); err != nil {
			log.Printf("❌ Invalid auth data provided (socket %s): %v", client.Id(), err)
			client.Emit("error", map[string]string{"message": "Invalid authentication data"})
			client.Disconnect(true)
			return
		}

		// Extract token
		token := auth.Token
		if token == "" {
			log.Printf("❌ No token provided (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Missing authentication token"})
			client.Disconnect(true)
			return
		}

		// Extract client type and optional IDs
		clientType := auth.ClientType
		if clientType == "" {
			clientType = "user-scoped" // Default to user-scoped
		}
		sessionID := auth.SessionID
		machineID := auth.MachineID

		// Validate session-scoped clients have sessionId
		if clientType == "session-scoped" && sessionID == "" {
			log.Printf("❌ Session-scoped client missing sessionId (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Session ID required for session-scoped clients"})
			client.Disconnect(true)
			return
		}

		// Validate machine-scoped clients have machineId
		if clientType == "machine-scoped" && machineID == "" {
			log.Printf("❌ Machine-scoped client missing machineId (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Machine ID required for machine-scoped clients"})
			client.Disconnect(true)
			return
		}

		// Verify JWT token
		claims, err := s.jwtManager.VerifyToken(token)
		if err != nil {
			log.Printf("❌ Invalid token provided (socket %s): %v", client.Id(), err)
			client.Emit("error", map[string]string{"message": "Invalid authentication token"})
			client.Disconnect(true)
			return
		}

		userID := claims.Subject
		log.Printf("✅ Token verified: userID=%s, clientType=%s, sessionId=%s, machineId=%s, socketId=%s",
			userID, clientType, sessionID, machineID, client.Id())

		// Store connection metadata
		socketData := &SocketData{
			UserID:     userID,
			ClientType: clientType,
			SessionID:  sessionID,
			MachineID:  machineID,
			Socket:     client,
		}
		s.socketData.Store(client.Id(), socketData)

		log.Printf("✅ Socket.IO client ready (user: %s, clientType: %s)", userID, clientType)

		if clientType == "machine-scoped" && machineID != "" {
			now := time.Now()
			if err := queries.UpdateMachineActivity(context.Background(), models.UpdateMachineActivityParams{
				Active:       1,
				LastActiveAt: now,
				AccountID:    userID,
				ID:           machineID,
			}); err != nil {
				log.Printf("Failed to update machine activity: %v", err)
			}

			s.emitEphemeralToUserScoped(userID, protocolwire.EphemeralMachineActivityPayload{
				Type:     "machine-activity",
				ID:       machineID,
				Active:   true,
				ActiveAt: now.UnixMilli(),
			}, "")
		}

		// Message event - broadcast to session-scoped clients
		client.On("message", func(data ...any) {
			sd := s.getSocketData(client.Id())
			log.Printf("Message event from user %s (socket %s): %+v", sd.UserID, client.Id(), data)

			// Get the message data
			if len(data) == 0 {
				log.Printf("No message data received")
				return
			}

			var payload protocolwire.OutboundMessagePayload
			if err := decodeAny(data[0], &payload); err != nil {
				log.Printf("Message data decode error: %v (type=%T)", err, data[0])
				return
			}

			// Get the target session ID from the message
			targetSessionID := payload.SID
			if targetSessionID == "" {
				// If no session ID in message, use the sender's session ID (if session-scoped)
				targetSessionID = sd.SessionID
			}

			if targetSessionID == "" {
				log.Printf("No target session ID for message")
				return
			}

			content := payload.Message
			if content == "" {
				log.Printf("❌ No message content provided")
				return
			}

			var localIDValue *string
			if payload.LocalID != "" {
				v := payload.LocalID
				localIDValue = &v
			}

			// Use a fresh background context for DB ops; handshake contexts can be
			// canceled after upgrade.
			s.sessions.EnqueueMessage(context.Background(), sd.UserID, targetSessionID, content, localIDValue, string(client.Id()))
		})

		// Session alive event
		client.On("session-alive", func(data ...any) {
			sd := s.getSocketData(client.Id())
			log.Printf("Session alive from user %s (socket %s): %+v", sd.UserID, client.Id(), data)

			payload, _ := getFirstMap(data)
			sid := getString(payload, "sid")
			if sid == "" {
				return
			}

			t := getInt64(payload, "time")
			if t == 0 {
				t = time.Now().UnixMilli()
			}

			now := time.Now().UnixMilli()
			if t > now {
				t = now
			}
			if t < now-10*60*1000 {
				return
			}

			if err := queries.UpdateSessionActivity(context.Background(), models.UpdateSessionActivityParams{
				Active:       1,
				LastActiveAt: time.UnixMilli(t),
				ID:           sid,
			}); err != nil {
				log.Printf("Failed to update session activity: %v", err)
			}

			thinking := getBool(payload, "thinking")
			s.emitEphemeralToUser(sd.UserID, protocolwire.EphemeralActivityPayload{
				Type:     "activity",
				ID:       sid,
				Active:   true,
				Thinking: thinking,
				ActiveAt: t,
			}, "")
		})

		// Machine alive event
		client.On("machine-alive", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			machineID := getString(payload, "machineId")
			if machineID == "" {
				return
			}
			t := getInt64(payload, "time")
			if t == 0 {
				t = time.Now().UnixMilli()
			}

			now := time.Now().UnixMilli()
			if t > now {
				t = now
			}
			if t < now-10*60*1000 {
				return
			}

			machine, err := queries.GetMachine(context.Background(), models.GetMachineParams{
				AccountID: sd.UserID,
				ID:        machineID,
			})
			if err != nil || machine.AccountID != sd.UserID {
				return
			}

			if err := queries.UpdateMachineActivity(context.Background(), models.UpdateMachineActivityParams{
				Active:       1,
				LastActiveAt: time.UnixMilli(t),
				AccountID:    sd.UserID,
				ID:           machineID,
			}); err != nil {
				log.Printf("Failed to update machine activity: %v", err)
			}

			s.emitEphemeralToUserScoped(sd.UserID, protocolwire.EphemeralMachineActivityPayload{
				Type:     "machine-activity",
				ID:       machineID,
				Active:   true,
				ActiveAt: t,
			}, "")
		})

		// Typed ACK handlers (decode -> handler -> ack -> emit updates)
		onTypedAck[protocolwire.MachineUpdateMetadataPayload](s, client, "machine-update-metadata", handlerDeps, handlers.MachineUpdateMetadata)
		onTypedAck[protocolwire.MachineUpdateStatePayload](s, client, "machine-update-state", handlerDeps, handlers.MachineUpdateState)
		onTypedAck[protocolwire.AccessKeyGetRequest](s, client, "access-key-get", handlerDeps, handlers.AccessKeyGet)
		onTypedAck[protocolwire.ArtifactReadRequest](s, client, "artifact-read", handlerDeps, handlers.ArtifactRead)
		onTypedAck[protocolwire.ArtifactCreateRequest](s, client, "artifact-create", handlerDeps, handlers.ArtifactCreate)
		onTypedAck[protocolwire.ArtifactUpdateRequest](s, client, "artifact-update", handlerDeps, handlers.ArtifactUpdate)
		onTypedAck[protocolwire.ArtifactDeleteRequest](s, client, "artifact-delete", handlerDeps, handlers.ArtifactDelete)
		onTypedAck[protocolwire.UpdateMetadataPayload](s, client, "update-metadata", handlerDeps, handlers.UpdateMetadata)
		onTypedAck[protocolwire.UpdateStatePayload](s, client, "update-state", handlerDeps, handlers.UpdateState)

		// Usage report event
		client.On("usage-report", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			sessionID := getString(payload, "sessionId")
			if sessionID == "" {
				return
			}

			session, err := queries.GetSessionByID(context.Background(), sessionID)
			if err != nil || session.AccountID != sd.UserID {
				return
			}

			key := getString(payload, "key")
			tokens := getMap(payload, "tokens")
			cost := getMap(payload, "cost")
			if key == "" || tokens == nil || cost == nil {
				return
			}

			s.emitEphemeralToUser(sd.UserID, protocolwire.EphemeralUsagePayload{
				Type:      "usage",
				ID:        sessionID,
				Key:       key,
				Tokens:    tokens,
				Cost:      cost,
				Timestamp: time.Now().UnixMilli(),
			}, "")
		})

		// Ephemeral forward (client -> server -> user-scoped)
		client.On("ephemeral", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			if payload == nil {
				return
			}
			s.emitEphemeralToUserScoped(sd.UserID, payload, "")
		})

		// RPC register
		client.On("rpc-register", func(data ...any) {
			sd := s.getSocketData(client.Id())
			raw, _ := getFirstAnyWithAck(data)
			var req protocolwire.RPCRegisterPayload
			if err := decodeAny(raw, &req); err != nil {
				req.Method = ""
			}
			method := req.Method
			if method == "" {
				client.Emit("rpc-error", protocolwire.RPCErrorPayload{Type: "register", Error: "Invalid method name"})
				return
			}
			if shouldDebugRPC() {
				log.Printf("RPC register: user=%s client=%s method=%s", sd.UserID, sd.ClientType, method)
			}
			s.rpc.Register(sd.UserID, method, string(client.Id()))
			client.Emit("rpc-registered", protocolwire.RPCRegisteredPayload{Method: method})
		})

		// RPC unregister
		client.On("rpc-unregister", func(data ...any) {
			sd := s.getSocketData(client.Id())
			raw, _ := getFirstAnyWithAck(data)
			var req protocolwire.RPCRegisterPayload
			if err := decodeAny(raw, &req); err != nil {
				req.Method = ""
			}
			method := req.Method
			if method == "" {
				client.Emit("rpc-error", protocolwire.RPCErrorPayload{Type: "unregister", Error: "Invalid method name"})
				return
			}
			s.rpc.Unregister(sd.UserID, method, string(client.Id()))
			client.Emit("rpc-unregistered", protocolwire.RPCUnregisteredPayload{Method: method})
		})

		// RPC call
		client.On("rpc-call", func(data ...any) {
			sd := s.getSocketData(client.Id())
			raw, ack := getFirstAnyWithAck(data)
			var req protocolwire.RPCCallPayload
			if err := decodeAny(raw, &req); err != nil {
				if ack != nil {
					ack(protocolwire.RPCAck{OK: false, Error: "Invalid parameters"})
				}
				return
			}
			forward, immediateAck := handlers.RPCCall(
				handlers.NewAuthContext(sd.UserID, sd.ClientType, string(client.Id())),
				s.rpc,
				req,
			)
			if immediateAck != nil {
				if ack != nil {
					ack(*immediateAck)
				}
				return
			}
			if forward == nil {
				if ack != nil {
					ack(protocolwire.RPCAck{OK: false, Error: "Internal error"})
				}
				return
			}

			targetSD := s.getSocketData(forward.TargetSocketID())
			if targetSD == nil || targetSD.Socket == nil {
				if ack != nil {
					ack(protocolwire.RPCAck{OK: false, Error: "RPC method not available"})
				}
				return
			}
			target := targetSD.Socket
			if shouldDebugRPC() {
				log.Printf("RPC call: user=%s client=%s method=%s target=%s", sd.UserID, sd.ClientType, req.Method, target.Id())
			}

			target.Timeout(forward.Timeout()).EmitWithAck("rpc-request", forward.Request())(func(args []any, err error) {
				if ack == nil {
					return
				}
				if err != nil {
					ack(protocolwire.RPCAck{OK: false, Error: err.Error()})
					return
				}
				var result any
				if len(args) > 0 {
					result = args[0]
				}
				ack(protocolwire.RPCAck{OK: true, Result: result})
			})
		})

		// Disconnection handler
		client.On("disconnect", func(data ...any) {
			sd := s.getSocketData(client.Id())
			reason := ""
			if len(data) > 0 {
				if r, ok := data[0].(string); ok {
					reason = r
				}
			}
			log.Printf("User disconnected: %s (socket %s, clientType: %s, reason: %s)",
				sd.UserID, client.Id(), sd.ClientType, reason)

			if sd.ClientType == "session-scoped" && sd.SessionID != "" {
				now := time.Now()
				if err := queries.UpdateSessionActivity(context.Background(), models.UpdateSessionActivityParams{
					Active:       0,
					LastActiveAt: now,
					ID:           sd.SessionID,
				}); err != nil {
					log.Printf("Failed to update session activity: %v", err)
				}
				s.emitEphemeralToUser(sd.UserID, protocolwire.EphemeralActivityPayload{
					Type:     "activity",
					ID:       sd.SessionID,
					Active:   false,
					Thinking: false,
					ActiveAt: now.UnixMilli(),
				}, "")
			}

			if sd.ClientType == "machine-scoped" && sd.MachineID != "" {
				now := time.Now()
				if err := queries.UpdateMachineActivity(context.Background(), models.UpdateMachineActivityParams{
					Active:       0,
					LastActiveAt: now,
					AccountID:    sd.UserID,
					ID:           sd.MachineID,
				}); err != nil {
					log.Printf("Failed to update machine activity: %v", err)
				}
				s.emitEphemeralToUserScoped(sd.UserID, protocolwire.EphemeralMachineActivityPayload{
					Type:     "machine-activity",
					ID:       sd.MachineID,
					Active:   false,
					ActiveAt: now.UnixMilli(),
				}, "")
			}
			// Clean up socket data
			s.socketData.Delete(client.Id())
			s.rpc.UnregisterAll(sd.UserID, string(client.Id()))
		})
	})
}

func decodeAny(input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (s *SocketIOServer) emitUpdateToSession(userID, sessionID string, payload any, skipSocketID string) {
	s.socketData.Range(func(key, value any) bool {
		targetSD, ok := value.(*SocketData)
		if !ok {
			return true
		}

		if skipSocketID != "" && key == skipSocketID {
			return true
		}

		if targetSD.UserID != userID || targetSD.Socket == nil {
			return true
		}

		if targetSD.ClientType == "machine-scoped" {
			return true
		}

		if targetSD.ClientType == "session-scoped" && targetSD.SessionID != sessionID {
			return true
		}

		log.Printf("Emitting update to %s client (socket %v)", targetSD.ClientType, key)
		targetSD.Socket.Emit("update", payload)
		return true
	})
}

func (s *SocketIOServer) emitEphemeralToUser(userID string, payload any, skipSocketID string) {
	s.emitEphemeralToUserWithFilter(userID, payload, skipSocketID, false)
}

func (s *SocketIOServer) emitEphemeralToUserScoped(userID string, payload any, skipSocketID string) {
	s.emitEphemeralToUserWithFilter(userID, payload, skipSocketID, true)
}

func (s *SocketIOServer) emitEphemeralToUserWithFilter(userID string, payload any, skipSocketID string, userScopedOnly bool) {
	s.socketData.Range(func(key, value any) bool {
		targetSD, ok := value.(*SocketData)
		if !ok {
			return true
		}
		if skipSocketID != "" && key == skipSocketID {
			return true
		}
		if targetSD.UserID != userID || targetSD.Socket == nil {
			return true
		}
		if userScopedOnly && targetSD.ClientType != "user-scoped" {
			return true
		}
		targetSD.Socket.Emit("ephemeral", payload)
		return true
	})
}

func (s *SocketIOServer) emitUpdateToUser(userID string, payload any, skipSocketID string) {
	s.socketData.Range(func(key, value any) bool {
		targetSD, ok := value.(*SocketData)
		if !ok {
			return true
		}
		if skipSocketID != "" && key == skipSocketID {
			return true
		}
		if targetSD.UserID != userID || targetSD.Socket == nil {
			return true
		}
		targetSD.Socket.Emit("update", payload)
		return true
	})
}

// EmitUpdateToSession exposes session-scoped update emission for internal
// runtimes.
func (s *SocketIOServer) EmitUpdateToSession(userID, sessionID string, payload any, skipSocketID string) {
	s.emitUpdateToSession(userID, sessionID, payload, skipSocketID)
}

// EmitUpdateToUser exposes update emission for HTTP handlers.
func (s *SocketIOServer) EmitUpdateToUser(userID string, payload any) {
	s.emitUpdateToUser(userID, payload, "")
}

// EmitEphemeralToUser exposes ephemeral emission for HTTP handlers.
func (s *SocketIOServer) EmitEphemeralToUser(userID string, payload any) {
	s.emitEphemeralToUser(userID, payload, "")
}

func getFirstMap(data []any) (map[string]any, bool) {
	if len(data) == 0 {
		return nil, false
	}
	payload, ok := data[0].(map[string]any)
	return payload, ok
}

func getFirstMapWithAck(data []any) (map[string]any, func(...any)) {
	var ack func(...any)
	if len(data) == 0 {
		return nil, nil
	}
	if cb, ok := data[len(data)-1].(func(...any)); ok {
		ack = cb
		data = data[:len(data)-1]
	} else if cb, ok := data[len(data)-1].(socket.Ack); ok {
		ack = func(args ...any) {
			cb(args, nil)
		}
		data = data[:len(data)-1]
	}
	payload, _ := getFirstMap(data)
	return payload, ack
}

func getFirstAnyWithAck(data []any) (any, func(...any)) {
	var ack func(...any)
	if len(data) == 0 {
		return nil, nil
	}
	if cb, ok := data[len(data)-1].(func(...any)); ok {
		ack = cb
		data = data[:len(data)-1]
	} else if cb, ok := data[len(data)-1].(socket.Ack); ok {
		ack = func(args ...any) {
			cb(args, nil)
		}
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return nil, ack
	}
	return data[0], ack
}

func shouldDebugRPC() bool {
	if val := os.Getenv("DELIGHT_DEBUG_RPC"); strings.EqualFold(val, "true") || strings.EqualFold(val, "1") {
		return true
	}
	return strings.EqualFold(os.Getenv("DELIGHT_DEBUG_RPC"), "true") ||
		strings.EqualFold(os.Getenv("DELIGHT_DEBUG_RPC"), "1")
}

func getString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}

func getOptionalString(payload map[string]any, key string) *string {
	if payload == nil {
		return nil
	}
	if v, ok := payload[key]; ok {
		if v == nil {
			return nil
		}
		if s, ok := v.(string); ok {
			return &s
		}
	}
	return nil
}

func getInt64(payload map[string]any, key string) int64 {
	if payload == nil {
		return 0
	}
	switch v := payload[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	default:
		return 0
	}
}

func getBool(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	if v, ok := payload[key].(bool); ok {
		return v
	}
	return false
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func getMap(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	if v, ok := payload[key].(map[string]any); ok {
		return v
	}
	return nil
}

// getSocketData retrieves socket metadata by socket ID
func (s *SocketIOServer) getSocketData(socketID any) *SocketData {
	if data, ok := s.socketData.Load(socketID); ok {
		if sd, ok := data.(*SocketData); ok {
			return sd
		}
	}
	return &SocketData{} // Return empty struct if not found
}

// HandleSocketIO creates a Gin handler for Socket.IO
func (s *SocketIOServer) HandleSocketIO() gin.HandlerFunc {
	// Get the HTTP handler from Socket.IO server
	httpHandler := s.server.ServeHandler(nil)

	return func(c *gin.Context) {
		// Add CORS headers to match TypeScript server
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight
		if c.Request.Method == "OPTIONS" {
			c.Status(http.StatusOK)
			return
		}

		log.Printf("Socket.IO request: %s %s", c.Request.Method, c.Request.URL.Path)

		// Serve Socket.IO
		httpHandler.ServeHTTP(c.Writer, c.Request)
	}
}

// Close shuts down the Socket.IO server
func (s *SocketIOServer) Close() error {
	s.server.Close(nil)
	return nil
}
