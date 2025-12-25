package websocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/pkg/types"
	socketio "github.com/googollee/go-socket.io"
	"github.com/googollee/go-socket.io/engineio"
	"github.com/googollee/go-socket.io/engineio/transport"
	"github.com/googollee/go-socket.io/engineio/transport/polling"
	"github.com/googollee/go-socket.io/engineio/transport/websocket"
)

type Server struct {
	io         *socketio.Server
	manager    *ConnectionManager
	router     *EventRouter
	db         *sql.DB
	queries    *models.Queries
	jwtManager *crypto.JWTManager
}

// NewServer creates a new WebSocket server
func NewServer(db *sql.DB, jwtManager *crypto.JWTManager) (*Server, error) {
	// Create Socket.IO server with custom transport
	opts := &engineio.Options{
		Transports: []transport.Transport{
			&polling.Transport{
				CheckOrigin: func(r *http.Request) bool {
					return true // Allow all origins for self-hosting
				},
			},
			&websocket.Transport{
				CheckOrigin: func(r *http.Request) bool {
					return true
				},
			},
		},
		PingTimeout:  45 * time.Second,
		PingInterval: 15 * time.Second,
	}

	io := socketio.NewServer(opts)

	manager := NewConnectionManager()
	router := NewEventRouter(manager)

	server := &Server{
		io:         io,
		manager:    manager,
		router:     router,
		db:         db,
		queries:    models.New(db),
		jwtManager: jwtManager,
	}

	// Register event handlers
	server.setupHandlers()

	return server, nil
}

// setupHandlers registers all Socket.IO event handlers
func (s *Server) setupHandlers() {
	s.io.OnConnect("/", func(conn socketio.Conn) error {
		log.Printf("[WebSocket] Client connected: %s", conn.ID())

		// Authentication happens in OnEvent for "authenticate"
		// or we can check auth header during connection

		return nil
	})

	s.io.OnEvent("/", "authenticate", func(conn socketio.Conn, authData AuthData) {
		// Verify JWT token
		claims, err := s.jwtManager.VerifyToken(authData.Token)
		if err != nil {
			log.Printf("[WebSocket] Authentication failed for %s: %v", conn.ID(), err)
			conn.Emit("error", map[string]string{"message": "authentication failed"})
			conn.Close()
			return
		}

		userID := claims.Subject

		// Determine connection type
		var connType ConnectionType
		var sessionID, machineID string

		switch authData.ClientType {
		case "user-scoped":
			connType = ConnectionTypeUserScoped
		case "session-scoped":
			connType = ConnectionTypeSessionScoped
			sessionID = authData.SessionID
		case "machine-scoped":
			connType = ConnectionTypeMachineScoped
			machineID = authData.MachineID
		default:
			log.Printf("[WebSocket] Invalid client type: %s", authData.ClientType)
			conn.Emit("error", map[string]string{"message": "invalid client type"})
			conn.Close()
			return
		}

		// Register connection
		client := &ClientConnection{
			Type:      connType,
			Socket:    conn,
			UserID:    userID,
			SessionID: sessionID,
			MachineID: machineID,
		}

		s.manager.AddConnection(client)

		log.Printf("[WebSocket] Authenticated: user=%s type=%s session=%s machine=%s",
			userID, connType, sessionID, machineID)

		conn.Emit("authenticated", map[string]bool{"success": true})
	})

	s.io.OnEvent("/", "disconnect", func(conn socketio.Conn) {
		log.Printf("[WebSocket] Client disconnected: %s", conn.ID())
		s.manager.RemoveConnection(conn.ID())
	})

	s.io.OnDisconnect("/", func(conn socketio.Conn, reason string) {
		log.Printf("[WebSocket] Client disconnected: %s (reason: %s)", conn.ID(), reason)
		s.manager.RemoveConnection(conn.ID())
	})

	// Message events
	s.io.OnEvent("/", "message", s.handleMessage)
	s.io.OnEvent("/", "session-alive", s.handleSessionAlive)
	s.io.OnEvent("/", "session-end", s.handleSessionEnd)
	s.io.OnEvent("/", "update-metadata", s.handleUpdateMetadata)
	s.io.OnEvent("/", "update-state", s.handleUpdateState)

	// Utility events
	s.io.OnEvent("/", "ping", func(conn socketio.Conn, ack func()) {
		if ack != nil {
			ack()
		}
	})

	s.io.OnError("/", func(conn socketio.Conn, err error) {
		log.Printf("[WebSocket] Error on connection %s: %v", conn.ID(), err)
	})
}

// handleMessage handles incoming messages
func (s *Server) handleMessage(conn socketio.Conn, data MessageEvent) {
	ctx := context.Background()
	client := s.manager.GetConnection(conn.ID())
	if client == nil {
		conn.Emit("error", map[string]string{"message": "not authenticated"})
		return
	}

	// Get session
	session, err := s.queries.GetSessionByID(ctx, data.SessionID)
	if err != nil {
		log.Printf("[WebSocket] Session not found: %s", data.SessionID)
		return
	}

	// Verify ownership
	if session.AccountID != client.UserID {
		return
	}

	// Get next message sequence number
	seqResult, err := s.queries.GetLatestMessageSeq(context.Background(), data.SessionID)
	if err != nil {
		log.Printf("[WebSocket] Failed to get message seq: %v", err)
		return
	}

	// seqResult is interface{}, need to cast to int64
	var maxSeq int64
	if val, ok := seqResult.(int64); ok {
		maxSeq = val
	}
	nextSeq := maxSeq + 1

	// Check for duplicate (if localId provided)
	if data.LocalID != "" {
		_, err := s.queries.GetMessageByLocalID(context.Background(), models.GetMessageByLocalIDParams{
			SessionID: data.SessionID,
			LocalID:   sql.NullString{String: data.LocalID, Valid: true},
		})
		if err == nil {
			// Message already exists, don't create duplicate
			log.Printf("[WebSocket] Duplicate message (localId=%s), skipping", data.LocalID)
			return
		}
	}

	// Create message
	localID := sql.NullString{}
	if data.LocalID != "" {
		localID.String = data.LocalID
		localID.Valid = true
	}

	// Wrap message content in encrypted envelope
	contentJSON, _ := json.Marshal(map[string]interface{}{
		"t": "encrypted",
		"c": data.Message,
	})

	message, err := s.queries.CreateMessage(context.Background(), models.CreateMessageParams{
		ID:        types.NewCUID(),
		SessionID: data.SessionID,
		LocalID:   localID,
		Seq:       nextSeq,
		Content:   string(contentJSON),
	})

	if err != nil {
		log.Printf("[WebSocket] Failed to create message: %v", err)
		return
	}

	// Allocate user sequence number
	userSeq, err := s.queries.UpdateAccountSeq(context.Background(), client.UserID)
	if err != nil {
		log.Printf("[WebSocket] Failed to allocate user seq: %v", err)
		return
	}

	// Broadcast new-message update
	// Emit content as stored in the DB (object), fallback to wrapping the incoming string
	var contentObj map[string]interface{}
	if err := json.Unmarshal([]byte(message.Content), &contentObj); err != nil || contentObj == nil {
		log.Printf("[WebSocket] Stored content not JSON, wrapping: %s (err=%v)", message.Content, err)
		contentObj = map[string]interface{}{
			"t": "encrypted",
			"c": data.Message,
		}
	}

	updatePayload := UpdatePayload{
		ID:  types.NewCUID(),
		Seq: userSeq,
		Body: map[string]interface{}{
			"t":   "new-message",
			"sid": data.SessionID,
			"message": map[string]interface{}{
				"id":        message.ID,
				"seq":       message.Seq,
				"content":   contentObj,
				"localId":   message.LocalID,
				"createdAt": message.CreatedAt.UnixMilli(),
				"updatedAt": message.UpdatedAt.UnixMilli(),
			},
		},
		CreatedAt: time.Now().UnixMilli(),
	}

	log.Printf("[WebSocket] Emitting new-message update sid=%s msgID=%s content=%v rawContent=%s", data.SessionID, message.ID, contentObj, message.Content)

	// Send to all interested connections (except sender)
	filter := &RecipientFilter{
		Type:      FilterAllInterestedInSession,
		SessionID: data.SessionID,
	}

	s.router.EmitUpdate(client.UserID, updatePayload, filter, conn.ID())
}

// handleSessionAlive handles session keep-alive
func (s *Server) handleSessionAlive(conn socketio.Conn, data SessionAliveEvent) {
	client := s.manager.GetConnection(conn.ID())
	if client == nil {
		return
	}

	// Update session activity
	lastActiveAt := time.UnixMilli(data.Time)
	err := s.queries.UpdateSessionActivity(context.Background(), models.UpdateSessionActivityParams{
		Active:       1,
		LastActiveAt: lastActiveAt,
		ID:           data.SessionID,
	})

	if err != nil {
		log.Printf("[WebSocket] Failed to update session activity: %v", err)
		return
	}

	// Broadcast ephemeral activity event
	ephemeral := EphemeralPayload{
		Type:     "activity",
		ID:       data.SessionID,
		Active:   true,
		ActiveAt: data.Time,
		Thinking: &data.Thinking,
	}

	filter := &RecipientFilter{
		Type:      FilterAllInterestedInSession,
		SessionID: data.SessionID,
	}

	s.router.EmitEphemeral(client.UserID, ephemeral, filter, conn.ID())
}

// handleSessionEnd handles session end
func (s *Server) handleSessionEnd(conn socketio.Conn, data SessionEndEvent) {
	client := s.manager.GetConnection(conn.ID())
	if client == nil {
		return
	}

	// Update session to inactive
	lastActiveAt := time.UnixMilli(data.Time)
	err := s.queries.UpdateSessionActivity(context.Background(), models.UpdateSessionActivityParams{
		Active:       0,
		LastActiveAt: lastActiveAt,
		ID:           data.SessionID,
	})

	if err != nil {
		log.Printf("[WebSocket] Failed to end session: %v", err)
		return
	}

	// Broadcast ephemeral activity event
	ephemeral := EphemeralPayload{
		Type:     "activity",
		ID:       data.SessionID,
		Active:   false,
		ActiveAt: data.Time,
	}

	filter := &RecipientFilter{
		Type:      FilterAllInterestedInSession,
		SessionID: data.SessionID,
	}

	s.router.EmitEphemeral(client.UserID, ephemeral, filter, conn.ID())
}

// handleUpdateMetadata handles session metadata updates
func (s *Server) handleUpdateMetadata(conn socketio.Conn, data UpdateMetadataEvent, ack func(UpdateResponse)) {
	client := s.manager.GetConnection(conn.ID())
	if client == nil {
		if ack != nil {
			ack(UpdateResponse{Result: "error"})
		}
		return
	}

	// Update with optimistic concurrency control
	rowsAffected, err := s.queries.UpdateSessionMetadata(context.Background(), models.UpdateSessionMetadataParams{
		Metadata:          data.Metadata,
		MetadataVersion:   data.ExpectedVersion + 1,
		ID:                data.SessionID,
		MetadataVersion_2: data.ExpectedVersion,
	})

	if err != nil || rowsAffected == 0 {
		// Version mismatch - get current version
		session, err := s.queries.GetSessionByID(context.Background(), data.SessionID)
		if err == nil && ack != nil {
			ack(UpdateResponse{
				Result:   "version-mismatch",
				Version:  session.MetadataVersion,
				Metadata: &session.Metadata,
			})
		} else if ack != nil {
			ack(UpdateResponse{Result: "error"})
		}
		return
	}

	// Success - send acknowledgment
	if ack != nil {
		ack(UpdateResponse{
			Result:  "success",
			Version: data.ExpectedVersion + 1,
		})
	}

	// Allocate user sequence number
	userSeq, err := s.queries.UpdateAccountSeq(context.Background(), client.UserID)
	if err != nil {
		log.Printf("[WebSocket] Failed to allocate user seq: %v", err)
		return
	}

	// Broadcast update-session event
	updatePayload := UpdatePayload{
		ID:  types.NewCUID(),
		Seq: userSeq,
		Body: map[string]interface{}{
			"t":  "update-session",
			"id": data.SessionID,
			"metadata": map[string]interface{}{
				"value":   data.Metadata,
				"version": data.ExpectedVersion + 1,
			},
		},
		CreatedAt: time.Now().UnixMilli(),
	}

	filter := &RecipientFilter{
		Type:      FilterAllInterestedInSession,
		SessionID: data.SessionID,
	}

	s.router.EmitUpdate(client.UserID, updatePayload, filter, conn.ID())
}

// handleUpdateState handles agent state updates
func (s *Server) handleUpdateState(conn socketio.Conn, data UpdateStateEvent, ack func(UpdateResponse)) {
	client := s.manager.GetConnection(conn.ID())
	if client == nil {
		if ack != nil {
			ack(UpdateResponse{Result: "error"})
		}
		return
	}

	agentState := sql.NullString{}
	if data.AgentState != nil {
		agentState.String = *data.AgentState
		agentState.Valid = true
	}

	// Update with optimistic concurrency control
	rowsAffected, err := s.queries.UpdateSessionAgentState(context.Background(), models.UpdateSessionAgentStateParams{
		AgentState:          agentState,
		AgentStateVersion:   data.ExpectedVersion + 1,
		ID:                  data.SessionID,
		AgentStateVersion_2: data.ExpectedVersion,
	})

	if err != nil || rowsAffected == 0 {
		// Version mismatch
		if ack != nil {
			ack(UpdateResponse{Result: "version-mismatch"})
		}
		return
	}

	// Success
	if ack != nil {
		ack(UpdateResponse{
			Result:  "success",
			Version: data.ExpectedVersion + 1,
		})
	}

	// Allocate user sequence number
	userSeq, err := s.queries.UpdateAccountSeq(context.Background(), client.UserID)
	if err != nil {
		log.Printf("[WebSocket] Failed to allocate user seq: %v", err)
		return
	}

	// Broadcast update-session event
	updatePayload := UpdatePayload{
		ID:  types.NewCUID(),
		Seq: userSeq,
		Body: map[string]interface{}{
			"t":  "update-session",
			"id": data.SessionID,
			"agentState": map[string]interface{}{
				"value":   data.AgentState,
				"version": data.ExpectedVersion + 1,
			},
		},
		CreatedAt: time.Now().UnixMilli(),
	}

	filter := &RecipientFilter{
		Type:      FilterAllInterestedInSession,
		SessionID: data.SessionID,
	}

	s.router.EmitUpdate(client.UserID, updatePayload, filter, conn.ID())
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.io.ServeHTTP(w, r)
}

// Start starts the Socket.IO server (for background processing)
func (s *Server) Start() error {
	go func() {
		if err := s.io.Serve(); err != nil {
			log.Printf("[WebSocket] Server error: %v", err)
		}
	}()
	return nil
}

// Close closes the Socket.IO server
func (s *Server) Close() error {
	return s.io.Close()
}

// GetRouter returns the event router (for use by HTTP handlers)
func (s *Server) GetRouter() *EventRouter {
	return s.router
}
