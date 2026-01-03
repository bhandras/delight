package handlers

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/gin-gonic/gin"
)

// TerminalHandler implements CRUD + keep-alives for terminals.
type TerminalHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

// NewTerminalHandler constructs a TerminalHandler.
func NewTerminalHandler(db *sql.DB, updates *websocket.SocketIOServer) *TerminalHandler {
	return &TerminalHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

// TerminalResponse represents a terminal in API responses.
type TerminalResponse struct {
	ID                 string  `json:"id"`
	Seq                int64   `json:"seq"`
	Metadata           string  `json:"metadata"`
	MetadataVersion    int64   `json:"metadataVersion"`
	DaemonState        *string `json:"daemonState"`
	DaemonStateVersion int64   `json:"daemonStateVersion"`
	DataEncryptionKey  *string `json:"dataEncryptionKey"`
	Active             bool    `json:"active"`
	ActiveAt           int64   `json:"activeAt"`
	CreatedAt          int64   `json:"createdAt"`
	UpdatedAt          int64   `json:"updatedAt"`
}

// CreateTerminalRequest represents the request to create/register a terminal.
type CreateTerminalRequest struct {
	ID                string  `json:"id" binding:"required"`
	Metadata          string  `json:"metadata" binding:"required"`
	DaemonState       *string `json:"daemonState"`
	DataEncryptionKey *string `json:"dataEncryptionKey"`
}

// UpdateTerminalActivityRequest represents keep-alive request.
type UpdateTerminalActivityRequest struct {
	Active bool  `json:"active"`
	Time   int64 `json:"time"` // Unix timestamp (ms)
}

// ListTerminals handles GET /v1/terminals.
//
// Returns a raw array (not wrapped in an object) to match existing client
// expectations and keep payloads small.
func (h *TerminalHandler) ListTerminals(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	terminals, err := h.queries.ListTerminals(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to list terminals"})
		return
	}

	response := make([]TerminalResponse, len(terminals))
	for i, terminal := range terminals {
		response[i] = h.toTerminalResponse(terminal)
	}
	c.JSON(http.StatusOK, response)
}

// CreateTerminal handles POST /v1/terminals.
func (h *TerminalHandler) CreateTerminal(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req CreateTerminalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Ensure account exists (auto-create if necessary).
	_, err := h.queries.GetAccountByID(c.Request.Context(), userID)
	if err == sql.ErrNoRows {
		logger.Infof("Auto-creating account for user: %s", userID)
		_, err = h.queries.CreateAccount(c.Request.Context(), models.CreateAccountParams{
			ID:        userID,
			PublicKey: "auto-created-" + userID, // Placeholder
		})
		if err != nil {
			logger.Errorf("Failed to auto-create account: %v", err)
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create account"})
			return
		}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error checking account"})
		return
	}

	// Check if terminal already exists.
	existing, err := h.queries.GetTerminal(c.Request.Context(), models.GetTerminalParams{
		AccountID: userID,
		ID:        req.ID,
	})
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"terminal": h.toTerminalResponse(existing)})
		return
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Decode data encryption key if provided.
	var dataEncryptionKey []byte
	if req.DataEncryptionKey != nil && *req.DataEncryptionKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(*req.DataEncryptionKey)
		if err != nil || len(decoded) != 32 {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid dataEncryptionKey (must be 32 bytes base64)"})
			return
		}
		dataEncryptionKey = decoded
	}

	daemonState := sql.NullString{}
	daemonStateVersion := int64(0)
	if req.DaemonState != nil {
		daemonState.String = *req.DaemonState
		daemonState.Valid = true
		daemonStateVersion = 1
	}

	if err := h.queries.CreateTerminal(c.Request.Context(), models.CreateTerminalParams{
		ID:                 req.ID,
		AccountID:          userID,
		Metadata:           req.Metadata,
		MetadataVersion:    1,
		DaemonState:        daemonState,
		DaemonStateVersion: daemonStateVersion,
		DataEncryptionKey:  dataEncryptionKey,
	}); err != nil {
		logger.Errorf("CreateTerminal error: %v", err)
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create terminal"})
		return
	}

	// New terminals start inactive until the CLI sends keep-alives.
	now := time.Now()
	if err := h.queries.UpdateTerminalActivity(c.Request.Context(), models.UpdateTerminalActivityParams{
		Active:       0,
		LastActiveAt: now,
		AccountID:    userID,
		ID:           req.ID,
	}); err != nil {
		logger.Warnf("Failed to set terminal inactive: %v", err)
	}

	terminal, err := h.queries.GetTerminal(c.Request.Context(), models.GetTerminalParams{
		AccountID: userID,
		ID:        req.ID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to fetch created terminal"})
		return
	}

	if h.updates != nil {
		userSeq1, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			logger.Errorf("Failed to allocate user seq for new-terminal: %v", err)
		} else {
			var daemonStateValue *string
			if terminal.DaemonState.Valid {
				v := terminal.DaemonState.String
				daemonStateValue = &v
			}
			var dataKey *string
			if len(terminal.DataEncryptionKey) > 0 {
				v := base64.StdEncoding.EncodeToString(terminal.DataEncryptionKey)
				dataKey = &v
			}

			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq1,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyNewTerminal{
					T:                  "new-terminal",
					TerminalID:         terminal.ID,
					Seq:                terminal.Seq,
					Metadata:           terminal.Metadata,
					MetadataVersion:    terminal.MetadataVersion,
					DaemonState:        daemonStateValue,
					DaemonStateVersion: terminal.DaemonStateVersion,
					DataEncryptionKey:  dataKey,
					Active:             terminal.Active != 0,
					ActiveAt:           terminal.LastActiveAt.UnixMilli(),
					CreatedAt:          terminal.CreatedAt.UnixMilli(),
					UpdatedAt:          terminal.UpdatedAt.UnixMilli(),
				},
			})
		}

		userSeq2, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			logger.Errorf("Failed to allocate user seq for update-terminal: %v", err)
		} else {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq2,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateTerminal{
					T:          "update-terminal",
					TerminalID: terminal.ID,
					Metadata: &protocolwire.VersionedString{
						Value:   terminal.Metadata,
						Version: terminal.MetadataVersion,
					},
				},
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"terminal": h.toTerminalResponse(terminal)})
}

// GetTerminal handles GET /v1/terminals/:id.
func (h *TerminalHandler) GetTerminal(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	terminalID := c.Param("id")

	terminal, err := h.queries.GetTerminal(c.Request.Context(), models.GetTerminalParams{
		AccountID: userID,
		ID:        terminalID,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "terminal not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"terminal": h.toTerminalResponse(terminal)})
}

// DeleteTerminal handles DELETE /v1/terminals/:id.
func (h *TerminalHandler) DeleteTerminal(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	terminalID := c.Param("id")

	if err := h.queries.DeleteTerminal(c.Request.Context(), models.DeleteTerminalParams{
		AccountID: userID,
		ID:        terminalID,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to delete terminal"})
		return
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// KeepAlive handles POST /v1/terminals/:id/alive and updates activity.
func (h *TerminalHandler) KeepAlive(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	terminalID := c.Param("id")

	var req UpdateTerminalActivityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	active := int64(0)
	if req.Active {
		active = 1
	}
	lastActiveAt := time.UnixMilli(req.Time)

	if err := h.queries.UpdateTerminalActivity(c.Request.Context(), models.UpdateTerminalActivityParams{
		Active:       active,
		LastActiveAt: lastActiveAt,
		AccountID:    userID,
		ID:           terminalID,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update activity"})
		return
	}

	if h.updates != nil {
		h.updates.EmitEphemeralToUser(userID, protocolwire.EphemeralTerminalActivityPayload{
			Type:     "terminal-activity",
			ID:       terminalID,
			Active:   req.Active,
			ActiveAt: req.Time,
		})
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// toTerminalResponse converts a database terminal to an API response object.
func (h *TerminalHandler) toTerminalResponse(terminal models.Terminal) TerminalResponse {
	var daemonState *string
	if terminal.DaemonState.Valid {
		daemonState = &terminal.DaemonState.String
	}

	var dataEncryptionKey *string
	if terminal.DataEncryptionKey != nil {
		encoded := base64.StdEncoding.EncodeToString(terminal.DataEncryptionKey)
		dataEncryptionKey = &encoded
	}

	return TerminalResponse{
		ID:                 terminal.ID,
		Seq:                terminal.Seq,
		Metadata:           terminal.Metadata,
		MetadataVersion:    terminal.MetadataVersion,
		DaemonState:        daemonState,
		DaemonStateVersion: terminal.DaemonStateVersion,
		DataEncryptionKey:  dataEncryptionKey,
		Active:             terminal.Active != 0,
		ActiveAt:           terminal.LastActiveAt.UnixMilli(),
		CreatedAt:          terminal.CreatedAt.UnixMilli(),
		UpdatedAt:          terminal.UpdatedAt.UnixMilli(),
	}
}

