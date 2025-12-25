package websocket

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// SimpleServer is a plain WebSocket server (not Socket.IO)
type SimpleServer struct {
	db         *sql.DB
	jwtManager *crypto.JWTManager
	clients    map[*websocket.Conn]*ClientInfo
	mu         sync.RWMutex
}

// ClientInfo stores information about a connected client
type ClientInfo struct {
	UserID    string
	SessionID string
	Conn      *websocket.Conn
}

// Event represents a WebSocket message
type Event struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

// NewSimpleServer creates a new plain WebSocket server
func NewSimpleServer(db *sql.DB, jwtManager *crypto.JWTManager) *SimpleServer {
	return &SimpleServer{
		db:         db,
		jwtManager: jwtManager,
		clients:    make(map[*websocket.Conn]*ClientInfo),
	}
}

// HandleWebSocket handles WebSocket connections
func (s *SimpleServer) HandleWebSocket(c *gin.Context) {
	// Upgrade connection
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Get user ID from auth middleware (already set in context)
	userID, exists := c.Get("user_id")
	if !exists {
		log.Println("No user_id in context")
		return
	}

	// Register client
	clientInfo := &ClientInfo{
		UserID: userID.(string),
		Conn:   conn,
	}

	s.mu.Lock()
	s.clients[conn] = clientInfo
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	log.Printf("WebSocket client connected: %s", userID)

	// Read messages
	for {
		var event Event
		err := conn.ReadJSON(&event)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		log.Printf("Received event: %s from %s", event.Type, userID)

		// Handle event
		s.handleEvent(clientInfo, &event)
	}

	log.Printf("WebSocket client disconnected: %s", userID)
}

// handleEvent processes incoming WebSocket events
func (s *SimpleServer) handleEvent(client *ClientInfo, event *Event) {
	switch event.Type {
	case "message":
		// TODO: Store message in database
		log.Printf("Message event: %+v", event.Data)

	case "session-alive":
		// TODO: Update session last_active_at
		sessionID, _ := event.Data["sessionId"].(string)
		log.Printf("Session alive: %s", sessionID)

	case "update-metadata":
		// TODO: Update session metadata
		log.Printf("Update metadata: %+v", event.Data)

	case "update-state":
		// TODO: Update agent state
		log.Printf("Update state: %+v", event.Data)

	default:
		log.Printf("Unknown event type: %s", event.Type)
	}
}

// Broadcast sends an event to all connected clients
func (s *SimpleServer) Broadcast(event *Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal event: %v", err)
		return
	}

	for conn := range s.clients {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			log.Printf("Failed to send message: %v", err)
		}
	}
}
