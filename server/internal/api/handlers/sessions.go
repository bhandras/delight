package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
)

type SessionHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

const (
	// sessionDataEncryptionKeyBytes is the byte length for per-session data keys.
	sessionDataEncryptionKeyBytes = 32
)

func NewSessionHandler(db *sql.DB, updates *websocket.SocketIOServer) *SessionHandler {
	return &SessionHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

// SessionResponse represents a session in API responses
type SessionResponse struct {
	ID                string  `json:"id"`
	Seq               int64   `json:"seq"`
	CreatedAt         int64   `json:"createdAt"`
	UpdatedAt         int64   `json:"updatedAt"`
	Active            bool    `json:"active"`
	ActiveAt          int64   `json:"activeAt"`
	Metadata          string  `json:"metadata"`
	MetadataVersion   int64   `json:"metadataVersion"`
	AgentState        *string `json:"agentState"`
	AgentStateVersion int64   `json:"agentStateVersion"`
	DataEncryptionKey *string `json:"dataEncryptionKey"`
	LastMessage       *string `json:"lastMessage"`
}

// CreateSessionRequest represents the request to create a session
type CreateSessionRequest struct {
	Tag               string  `json:"tag" binding:"required"`
	Metadata          string  `json:"metadata" binding:"required"`
	AgentState        *string `json:"agentState"`
	DataEncryptionKey *string `json:"dataEncryptionKey"`
}

// ListSessions handles GET /v1/sessions and GET /v2/sessions/active
func (h *SessionHandler) ListSessions(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	// Check if this is the active sessions endpoint
	isActive := c.Request.URL.Path == "/v2/sessions/active"

	// Get limit parameter
	limit := int64(150)
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}

	var sessions []models.Session
	var err error

	if isActive {
		// Get only active sessions (active within last 15 minutes)
		sessions, err = h.queries.ListActiveSessions(c.Request.Context(), models.ListActiveSessionsParams{
			AccountID: userID,
			Limit:     limit,
		})
	} else {
		// Get all sessions
		sessions, err = h.queries.ListSessions(c.Request.Context(), models.ListSessionsParams{
			AccountID: userID,
			Limit:     limit,
		})
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to list sessions"})
		return
	}

	// Convert to response format
	response := make([]SessionResponse, len(sessions))
	for i, session := range sessions {
		response[i] = h.toSessionResponse(session)
	}

	c.JSON(http.StatusOK, gin.H{"sessions": response})
}

// CreateSession handles POST /v1/sessions
func (h *SessionHandler) CreateSession(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Check if session with this tag already exists
	existing, err := h.queries.GetSessionByTag(c.Request.Context(), models.GetSessionByTagParams{
		AccountID: userID,
		Tag:       req.Tag,
	})

	if err == nil {
		// Session exists; ensure it has a dataEncryptionKey.
		if len(existing.DataEncryptionKey) == 0 {
			key, keyErr := h.ensureSessionDataEncryptionKey(c.Request.Context(), existing.ID)
			if keyErr != nil {
				c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to ensure dataEncryptionKey"})
				return
			}
			existing.DataEncryptionKey = key
		}

		c.JSON(http.StatusOK, gin.H{"session": h.toSessionResponse(existing)})
		return
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Decode data encryption key if provided
	var dataEncryptionKey []byte
	if req.DataEncryptionKey != nil && *req.DataEncryptionKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(*req.DataEncryptionKey)
		if err != nil || len(decoded) != sessionDataEncryptionKeyBytes {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid dataEncryptionKey (must be 32 bytes base64)"})
			return
		}
		dataEncryptionKey = decoded
	}
	if len(dataEncryptionKey) == 0 {
		key, keyErr := h.ensureSessionDataEncryptionKey(c.Request.Context(), "")
		if keyErr != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to generate dataEncryptionKey"})
			return
		}
		dataEncryptionKey = key
	}

	// Create new session
	agentState := sql.NullString{}
	agentStateVersion := int64(0)
	if req.AgentState != nil {
		agentState.String = *req.AgentState
		agentState.Valid = true
	}

	session, err := h.queries.CreateSession(c.Request.Context(), models.CreateSessionParams{
		ID:                types.NewCUID(),
		Tag:               req.Tag,
		AccountID:         userID,
		Metadata:          req.Metadata,
		MetadataVersion:   0,
		AgentState:        agentState,
		AgentStateVersion: agentStateVersion,
		DataEncryptionKey: dataEncryptionKey,
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create session"})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			log.Printf("Failed to allocate user seq for new-session: %v", err)
		} else {
			var agentStateValue *string
			if session.AgentState.Valid {
				v := session.AgentState.String
				agentStateValue = &v
			}
			var dataKey *string
			if len(session.DataEncryptionKey) > 0 {
				v := base64.StdEncoding.EncodeToString(session.DataEncryptionKey)
				dataKey = &v
			}
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyNewSession{
					T:                 "new-session",
					ID:                session.ID,
					Seq:               session.Seq,
					Metadata:          session.Metadata,
					MetadataVersion:   session.MetadataVersion,
					AgentState:        agentStateValue,
					AgentStateVersion: session.AgentStateVersion,
					DataEncryptionKey: dataKey,
					Active:            session.Active != 0,
					ActiveAt:          session.LastActiveAt.UnixMilli(),
					CreatedAt:         session.CreatedAt.UnixMilli(),
					UpdatedAt:         session.UpdatedAt.UnixMilli(),
				},
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"session": h.toSessionResponse(session)})
}

// ensureSessionDataEncryptionKey generates and optionally persists a session key.
//
// When sessionID is empty, it only generates a new key. When non-empty, it
// also persists it to the sessions row.
func (h *SessionHandler) ensureSessionDataEncryptionKey(ctx context.Context, sessionID string) ([]byte, error) {
	key := make([]byte, sessionDataEncryptionKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return key, nil
	}
	if _, err := h.db.ExecContext(ctx, `UPDATE sessions SET data_encryption_key = ? WHERE id = ?`, key, sessionID); err != nil {
		return nil, err
	}
	return key, nil
}

// GetSession handles GET /v1/sessions/:id
func (h *SessionHandler) GetSession(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("id")

	session, err := h.queries.GetSessionByID(c.Request.Context(), sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "session not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Verify ownership
	if session.AccountID != userID {
		c.JSON(http.StatusForbidden, types.ErrorResponse{Error: "access denied"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": h.toSessionResponse(session)})
}

// DeleteSession handles DELETE /v1/sessions/:id
func (h *SessionHandler) DeleteSession(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("id")

	// Verify session exists and user owns it
	session, err := h.queries.GetSessionByID(c.Request.Context(), sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "session not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	if session.AccountID != userID {
		c.JSON(http.StatusForbidden, types.ErrorResponse{Error: "access denied"})
		return
	}

	// Delete session (cascade will delete messages)
	if err := h.queries.DeleteSession(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to delete session"})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			log.Printf("Failed to allocate user seq for delete-session: %v", err)
		} else {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyDeleteSession{
					T:   "delete-session",
					SID: sessionID,
				},
			})
		}
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// GetSessionMessages handles GET /v1/sessions/:id/messages
func (h *SessionHandler) GetSessionMessages(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("id")

	// Verify session exists and user owns it
	session, err := h.queries.GetSessionByID(c.Request.Context(), sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "session not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	if session.AccountID != userID {
		c.JSON(http.StatusForbidden, types.ErrorResponse{Error: "access denied"})
		return
	}

	type PageInfo struct {
		HasMore       bool   `json:"hasMore"`
		NextBeforeSeq *int64 `json:"nextBeforeSeq,omitempty"`
	}

	// Get pagination parameters.
	//
	// Supported modes:
	// - Legacy (limit + offset): returns messages ordered by seq ASC starting at offset.
	// - Cursor (limit + beforeSeq): returns messages where seq < beforeSeq, ordered ASC.
	// - Cursor (limit + afterSeq): returns messages where seq > afterSeq, ordered ASC.
	// - Default (limit only): returns the most recent page (ordered ASC).
	limit := int64(100)
	offset := int64(0)
	offsetProvided := false
	beforeSeq := int64(0)
	afterSeq := int64(0)

	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		offsetProvided = true
		if o, err := strconv.ParseInt(offsetStr, 10, 64); err == nil && o >= 0 {
			offset = o
		}
	}

	if beforeStr := c.Query("beforeSeq"); beforeStr != "" {
		if v, err := strconv.ParseInt(beforeStr, 10, 64); err == nil && v > 0 {
			beforeSeq = v
		}
	}

	if afterStr := c.Query("afterSeq"); afterStr != "" {
		if v, err := strconv.ParseInt(afterStr, 10, 64); err == nil && v > 0 {
			afterSeq = v
		}
	}

	var (
		messages []models.SessionMessage
		page     *PageInfo
		listErr  error
	)

	// Get messages.
	switch {
	case beforeSeq > 0:
		messages, listErr = h.listMessagesBeforeSeq(c.Request.Context(), sessionID, beforeSeq, limit)
		if listErr == nil && len(messages) > 0 {
			minSeq := messages[0].Seq
			for _, msg := range messages[1:] {
				if msg.Seq < minSeq {
					minSeq = msg.Seq
				}
			}
			hasMore, moreErr := h.hasMessagesBeforeSeq(c.Request.Context(), sessionID, minSeq)
			if moreErr == nil {
				page = &PageInfo{HasMore: hasMore, NextBeforeSeq: &minSeq}
			}
		} else if listErr == nil {
			page = &PageInfo{HasMore: false}
		}
	case afterSeq > 0:
		messages, listErr = h.listMessagesAfterSeq(c.Request.Context(), sessionID, afterSeq, limit)
		if listErr == nil {
			page = &PageInfo{HasMore: false}
		}
	case offsetProvided:
		// Legacy support for offset-based paging.
		messages, listErr = h.queries.ListMessages(c.Request.Context(), models.ListMessagesParams{
			SessionID: sessionID,
			Limit:     limit,
			Offset:    offset,
		})
	default:
		// Default: most recent page.
		messages, listErr = h.listMessagesLatest(c.Request.Context(), sessionID, limit)
		if listErr == nil && len(messages) > 0 {
			minSeq := messages[0].Seq
			for _, msg := range messages[1:] {
				if msg.Seq < minSeq {
					minSeq = msg.Seq
				}
			}
			hasMore, moreErr := h.hasMessagesBeforeSeq(c.Request.Context(), sessionID, minSeq)
			if moreErr == nil {
				page = &PageInfo{HasMore: hasMore, NextBeforeSeq: &minSeq}
			}
		} else if listErr == nil {
			page = &PageInfo{HasMore: false}
		}
	}

	if listErr != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to list messages"})
		return
	}

	// Convert to response format
	response := make([]MessageResponse, len(messages))
	for i, msg := range messages {
		response[i] = h.toMessageResponse(msg)
		log.Printf("[API] Sending message id=%s seq=%d content=%s", msg.ID, msg.Seq, msg.Content)
	}

	if page != nil {
		c.JSON(http.StatusOK, gin.H{"messages": response, "page": page})
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": response})
}

// MessageResponse represents a message in API responses
type MessageResponse struct {
	ID        string          `json:"id"`
	Seq       int64           `json:"seq"`
	Content   json.RawMessage `json:"content"`
	LocalID   *string         `json:"localId"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

// Helper to convert database session to API response
func (h *SessionHandler) toSessionResponse(session models.Session) SessionResponse {
	var agentState *string
	if session.AgentState.Valid {
		agentState = &session.AgentState.String
	}

	var dataEncryptionKey *string
	if session.DataEncryptionKey != nil {
		encoded := base64.StdEncoding.EncodeToString(session.DataEncryptionKey)
		dataEncryptionKey = &encoded
	}

	return SessionResponse{
		ID:                session.ID,
		Seq:               session.Seq,
		CreatedAt:         session.CreatedAt.UnixMilli(),
		UpdatedAt:         session.UpdatedAt.UnixMilli(),
		Active:            session.Active != 0,
		ActiveAt:          session.LastActiveAt.UnixMilli(),
		Metadata:          session.Metadata,
		MetadataVersion:   session.MetadataVersion,
		AgentState:        agentState,
		AgentStateVersion: session.AgentStateVersion,
		DataEncryptionKey: dataEncryptionKey,
		LastMessage:       nil, // TODO: implement if needed
	}
}

// Helper to convert database message to API response
func (h *SessionHandler) toMessageResponse(msg models.SessionMessage) MessageResponse {
	var localID *string
	if msg.LocalID.Valid {
		localID = &msg.LocalID.String
	}

	rawContent := json.RawMessage(msg.Content)
	// If stored content is not valid JSON (legacy raw string) OR is JSON but missing the envelope
	var tmp any
	needsWrap := false
	if err := json.Unmarshal(rawContent, &tmp); err != nil {
		needsWrap = true
		log.Printf("[API] Wrapping legacy message content (id=%s, not JSON): %s (err=%v)", msg.ID, msg.Content, err)
	} else {
		if m, ok := tmp.(map[string]any); ok {
			_, hasT := m["t"]
			_, hasC := m["c"]
			if !hasT || !hasC {
				needsWrap = true
				log.Printf("[API] Wrapping non-envelope message content (id=%s): %s", msg.ID, msg.Content)
			}
		} else {
			needsWrap = true
			log.Printf("[API] Wrapping non-object message content (id=%s): %s", msg.ID, msg.Content)
		}
	}

	if needsWrap {
		fallback, _ := json.Marshal(map[string]any{
			"t": "encrypted",
			"c": msg.Content,
		})
		rawContent = fallback
	}

	return MessageResponse{
		ID:        msg.ID,
		Seq:       msg.Seq,
		Content:   rawContent,
		LocalID:   localID,
		CreatedAt: msg.CreatedAt.UnixMilli(),
		UpdatedAt: msg.UpdatedAt.UnixMilli(),
	}
}

func (h *SessionHandler) listMessagesLatest(ctx context.Context, sessionID string, limit int64) ([]models.SessionMessage, error) {
	// Query newest-first then reverse to keep responses chronological (seq ASC).
	rows, err := h.db.QueryContext(ctx, `
SELECT id, session_id, local_id, seq, content, created_at, updated_at
FROM session_messages
WHERE session_id = ?
ORDER BY seq DESC
LIMIT ?;
`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []models.SessionMessage
	for rows.Next() {
		var msg models.SessionMessage
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.LocalID, &msg.Seq, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reverseMessages(messages)
	return messages, nil
}

func (h *SessionHandler) listMessagesBeforeSeq(ctx context.Context, sessionID string, beforeSeq int64, limit int64) ([]models.SessionMessage, error) {
	// Query newest-first then reverse to keep responses chronological (seq ASC).
	rows, err := h.db.QueryContext(ctx, `
SELECT id, session_id, local_id, seq, content, created_at, updated_at
FROM session_messages
WHERE session_id = ?
  AND seq < ?
ORDER BY seq DESC
LIMIT ?;
`, sessionID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []models.SessionMessage
	for rows.Next() {
		var msg models.SessionMessage
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.LocalID, &msg.Seq, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reverseMessages(messages)
	return messages, nil
}

func (h *SessionHandler) listMessagesAfterSeq(ctx context.Context, sessionID string, afterSeq int64, limit int64) ([]models.SessionMessage, error) {
	rows, err := h.db.QueryContext(ctx, `
SELECT id, session_id, local_id, seq, content, created_at, updated_at
FROM session_messages
WHERE session_id = ?
  AND seq > ?
ORDER BY seq ASC
LIMIT ?;
`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []models.SessionMessage
	for rows.Next() {
		var msg models.SessionMessage
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.LocalID, &msg.Seq, &msg.Content, &msg.CreatedAt, &msg.UpdatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (h *SessionHandler) hasMessagesBeforeSeq(ctx context.Context, sessionID string, beforeSeq int64) (bool, error) {
	var exists bool
	err := h.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1
  FROM session_messages
  WHERE session_id = ?
    AND seq < ?
  LIMIT 1
);
`, sessionID, beforeSeq).Scan(&exists)
	return exists, err
}

func reverseMessages(messages []models.SessionMessage) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}
