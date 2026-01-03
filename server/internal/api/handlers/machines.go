package handlers

import (
	"database/sql"
	"encoding/base64"
	"fmt"
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

type MachineHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

func NewMachineHandler(db *sql.DB, updates *websocket.SocketIOServer) *MachineHandler {
	return &MachineHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

// MachineResponse represents a machine in API responses
type MachineResponse struct {
	ID                 string  `json:"id"`
	Seq                int64   `json:"seq"`
	Metadata           string  `json:"metadata"`
	MetadataVersion    int64   `json:"metadataVersion"`
	DaemonState        *string `json:"daemonState"`
	DaemonStateVersion int64   `json:"daemonStateVersion"`
	DataEncryptionKey  *string `json:"dataEncryptionKey"`
	Active             bool    `json:"active"`
	ActiveAt           int64   `json:"activeAt"` // matches TypeScript server's API format
	CreatedAt          int64   `json:"createdAt"`
	UpdatedAt          int64   `json:"updatedAt"`
}

// CreateMachineRequest represents the request to create/register a machine
type CreateMachineRequest struct {
	ID                string  `json:"id" binding:"required"`
	Metadata          string  `json:"metadata" binding:"required"`
	DaemonState       *string `json:"daemonState"`
	DataEncryptionKey *string `json:"dataEncryptionKey"`
}

// UpdateMachineActivityRequest represents keep-alive request
type UpdateMachineActivityRequest struct {
	Active bool  `json:"active"`
	Time   int64 `json:"time"` // Unix timestamp (ms)
}

// ListMachines handles GET /v1/machines
// Returns a raw array of machines (not wrapped in an object) to match TypeScript server API
func (h *MachineHandler) ListMachines(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	machines, err := h.queries.ListMachines(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to list machines"})
		return
	}

	// Convert to response format
	response := make([]MachineResponse, len(machines))
	for i, machine := range machines {
		response[i] = h.toMachineResponse(machine)
	}

	// Return raw array (not wrapped) to match TypeScript server format
	c.JSON(http.StatusOK, response)
}

// CreateMachine handles POST /v1/machines
func (h *MachineHandler) CreateMachine(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req CreateMachineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Ensure account exists (auto-create if necessary)
	_, err := h.queries.GetAccountByID(c.Request.Context(), userID)
	if err == sql.ErrNoRows {
		// Auto-create account with a placeholder public key
		// This can happen when the database is reset but the CLI still has a valid token
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

	// Check if machine already exists
	existing, err := h.queries.GetMachine(c.Request.Context(), models.GetMachineParams{
		AccountID: userID,
		ID:        req.ID,
	})

	if err == nil {
		// Machine exists, return it without changing activity
		c.JSON(http.StatusOK, gin.H{"machine": h.toMachineResponse(existing)})
		return
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Decode data encryption key if provided
	var dataEncryptionKey []byte
	if req.DataEncryptionKey != nil && *req.DataEncryptionKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(*req.DataEncryptionKey)
		if err != nil || len(decoded) != 32 {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid dataEncryptionKey (must be 32 bytes base64)"})
			return
		}
		dataEncryptionKey = decoded
	}

	// Create new machine
	daemonState := sql.NullString{}
	daemonStateVersion := int64(0)
	if req.DaemonState != nil {
		daemonState.String = *req.DaemonState
		daemonState.Valid = true
		daemonStateVersion = 1
	}

	err = h.queries.CreateMachine(c.Request.Context(), models.CreateMachineParams{
		ID:                 req.ID,
		AccountID:          userID,
		Metadata:           req.Metadata,
		MetadataVersion:    1,
		DaemonState:        daemonState,
		DaemonStateVersion: daemonStateVersion,
		DataEncryptionKey:  dataEncryptionKey,
	})

	if err != nil {
		logger.Errorf("CreateMachine error: %v", err)
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create machine"})
		return
	}

	// Match TypeScript server behavior: new machines start inactive.
	now := time.Now()
	if err := h.queries.UpdateMachineActivity(c.Request.Context(), models.UpdateMachineActivityParams{
		Active:       0,
		LastActiveAt: now,
		AccountID:    userID,
		ID:           req.ID,
	}); err != nil {
		logger.Warnf("Failed to set machine inactive: %v", err)
	}

	// Fetch the created machine
	machine, err := h.queries.GetMachine(c.Request.Context(), models.GetMachineParams{
		AccountID: userID,
		ID:        req.ID,
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to fetch created machine"})
		return
	}

	if h.updates != nil {
		userSeq1, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			logger.Errorf("Failed to allocate user seq for new-machine: %v", err)
		} else {
			var daemonStateValue *string
			if machine.DaemonState.Valid {
				v := machine.DaemonState.String
				daemonStateValue = &v
			}
			var dataKey *string
			if len(machine.DataEncryptionKey) > 0 {
				v := base64.StdEncoding.EncodeToString(machine.DataEncryptionKey)
				dataKey = &v
			}
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq1,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyNewMachine{
					T:                  "new-machine",
					MachineID:          machine.ID,
					Seq:                machine.Seq,
					Metadata:           machine.Metadata,
					MetadataVersion:    machine.MetadataVersion,
					DaemonState:        daemonStateValue,
					DaemonStateVersion: machine.DaemonStateVersion,
					DataEncryptionKey:  dataKey,
					Active:             machine.Active != 0,
					ActiveAt:           machine.LastActiveAt.UnixMilli(),
					CreatedAt:          machine.CreatedAt.UnixMilli(),
					UpdatedAt:          machine.UpdatedAt.UnixMilli(),
				},
			})
		}

		userSeq2, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			logger.Errorf("Failed to allocate user seq for update-machine: %v", err)
		} else {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq2,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateMachine{
					T:         "update-machine",
					MachineID: machine.ID,
					Metadata: &protocolwire.VersionedString{
						Value:   machine.Metadata,
						Version: machine.MetadataVersion,
					},
				},
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"machine": h.toMachineResponse(machine)})
}

// GetMachine handles GET /v1/machines/:id
func (h *MachineHandler) GetMachine(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	machineID := c.Param("id")

	machine, err := h.queries.GetMachine(c.Request.Context(), models.GetMachineParams{
		AccountID: userID,
		ID:        machineID,
	})

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "machine not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"machine": h.toMachineResponse(machine)})
}

// DeleteMachine handles DELETE /v1/machines/:id
func (h *MachineHandler) DeleteMachine(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	machineID := c.Param("id")

	ctx := c.Request.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()
	qtx := h.queries.WithTx(tx)

	sessionIDsByMachineKey, err := qtx.ListSessionIDsForMachine(ctx, models.ListSessionIDsForMachineParams{
		AccountID: userID,
		MachineID: machineID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	tagPrefix := fmt.Sprintf("m-%s-", machineID)
	sessionIDsByTag, err := qtx.ListSessionIDsByTagLike(ctx, models.ListSessionIDsByTagLikeParams{
		AccountID: userID,
		Tag:       tagPrefix + "%",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	sessionIDs := make(map[string]struct{}, len(sessionIDsByMachineKey)+len(sessionIDsByTag))
	for _, sessionID := range sessionIDsByMachineKey {
		sessionIDs[sessionID] = struct{}{}
	}
	for _, sessionID := range sessionIDsByTag {
		sessionIDs[sessionID] = struct{}{}
	}

	// Best-effort: "machines" don't have a direct FK relationship to sessions, so
	// we treat "belonging to a machine" as "has an access key issued for this
	// machine". This keeps the UI semantics intuitive (delete machine deletes the
	// machine's terminals).
	//
	// Sessions also carry a stable tag prefix derived from the machine id (see
	// stableSessionTag in the CLI). This is the primary linkage when access_keys are
	// not used.
	for sessionID := range sessionIDs {
		if err := qtx.DeleteSession(ctx, sessionID); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to delete session"})
			return
		}
	}

	// Delete machine (cascade deletes any remaining access_keys rows).
	if err := qtx.DeleteMachine(ctx, models.DeleteMachineParams{
		AccountID: userID,
		ID:        machineID,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to delete machine"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// KeepAlive handles POST /v1/machines/:id/alive
// Updates machine last active timestamp
func (h *MachineHandler) KeepAlive(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	machineID := c.Param("id")

	var req UpdateMachineActivityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Update activity
	active := int64(0)
	if req.Active {
		active = 1
	}

	lastActiveAt := time.UnixMilli(req.Time)

	err := h.queries.UpdateMachineActivity(c.Request.Context(), models.UpdateMachineActivityParams{
		Active:       active,
		LastActiveAt: lastActiveAt,
		AccountID:    userID,
		ID:           machineID,
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update activity"})
		return
	}

	if h.updates != nil {
		h.updates.EmitEphemeralToUser(userID, protocolwire.EphemeralMachineActivityPayload{
			Type:     "machine-activity",
			ID:       machineID,
			Active:   req.Active,
			ActiveAt: req.Time,
		})
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// Helper to convert database machine to API response
func (h *MachineHandler) toMachineResponse(machine models.Machine) MachineResponse {
	var daemonState *string
	if machine.DaemonState.Valid {
		daemonState = &machine.DaemonState.String
	}

	var dataEncryptionKey *string
	if machine.DataEncryptionKey != nil {
		encoded := base64.StdEncoding.EncodeToString(machine.DataEncryptionKey)
		dataEncryptionKey = &encoded
	}

	return MachineResponse{
		ID:                 machine.ID,
		Seq:                machine.Seq,
		Metadata:           machine.Metadata,
		MetadataVersion:    machine.MetadataVersion,
		DaemonState:        daemonState,
		DaemonStateVersion: machine.DaemonStateVersion,
		DataEncryptionKey:  dataEncryptionKey,
		Active:             machine.Active != 0,
		ActiveAt:           machine.LastActiveAt.UnixMilli(), // Field renamed to match TypeScript API
		CreatedAt:          machine.CreatedAt.UnixMilli(),
		UpdatedAt:          machine.UpdatedAt.UnixMilli(),
	}
}
