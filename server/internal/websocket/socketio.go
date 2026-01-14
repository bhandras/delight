package websocket

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/bhandras/delight/server/internal/models"
	sessionruntime "github.com/bhandras/delight/server/internal/session/runtime"
	"github.com/bhandras/delight/server/internal/websocket/handlers"
	pkgtypes "github.com/bhandras/delight/server/pkg/types"
	"github.com/bhandras/delight/shared/logger"
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
		Credentials: false,
	})

	// SocketIOPingInterval defines how frequently the server pings clients to
	// detect stale/disconnected sockets.
	//
	// This influences how quickly we mark sessions/terminals offline after an
	// abrupt CLI exit (where no graceful disconnect event is emitted).
	const SocketIOPingInterval = 5 * time.Second

	// SocketIOPingTimeout defines how long the server waits before considering a
	// socket dead (no pong received).
	const SocketIOPingTimeout = 15 * time.Second

	opts.SetPingTimeout(SocketIOPingTimeout)
	opts.SetPingInterval(SocketIOPingInterval)

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
	ClientType string // "session-scoped", "user-scoped", or "terminal-scoped"
	SessionID  string
	TerminalID string
	Socket     *socket.Socket // Reference to the socket for emitting
}

// setupHandlers configures Socket.IO event handlers
func (s *SocketIOServer) setupHandlers() {
	queries := models.New(s.db)
	handlerDeps := handlers.NewDeps(queries, queries, queries, queries, time.Now, pkgtypes.NewCUID)

	// Connection handler
	s.server.On("connection", func(clients ...any) {
		client := clients[0].(*socket.Socket)
		s.handleConnection(client, handlerDeps)
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

		if targetSD.ClientType == "terminal-scoped" {
			return true
		}

		if targetSD.ClientType == "session-scoped" && targetSD.SessionID != sessionID {
			return true
		}

		logger.Tracef("Emitting update to %s client (socket %v)", targetSD.ClientType, key)
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

// getSocketData retrieves socket metadata by socket ID
func (s *SocketIOServer) getSocketData(socketID string) *SocketData {
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
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "false")

		// Handle preflight
		if c.Request.Method == "OPTIONS" {
			c.Status(http.StatusOK)
			return
		}

		logger.Tracef("Socket.IO request: %s %s", c.Request.Method, c.Request.URL.Path)

		// Serve Socket.IO
		httpHandler.ServeHTTP(c.Writer, c.Request)
	}
}

// Close shuts down the Socket.IO server
func (s *SocketIOServer) Close() error {
	s.server.Close(nil)
	return nil
}
