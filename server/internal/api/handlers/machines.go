package handlers

import (
	"database/sql"
	"encoding/base64"
	"log"
	"net/http"
	"time"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
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
		log.Printf("Auto-creating account for user: %s", userID)
		_, err = h.queries.CreateAccount(c.Request.Context(), models.CreateAccountParams{
			ID:        userID,
			PublicKey: "auto-created-" + userID, // Placeholder
		})
		if err != nil {
			log.Printf("Failed to auto-create account: %v", err)
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
		log.Printf("CreateMachine error: %v", err)
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
		log.Printf("Failed to set machine inactive: %v", err)
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
			log.Printf("Failed to allocate user seq for new-machine: %v", err)
		} else {
			var daemonStateValue any
			if machine.DaemonState.Valid {
				daemonStateValue = machine.DaemonState.String
			}
			var dataKey any
			if len(machine.DataEncryptionKey) > 0 {
				dataKey = base64.StdEncoding.EncodeToString(machine.DataEncryptionKey)
			}
			newPayload := map[string]any{
				"id":  types.NewCUID(),
				"seq": userSeq1,
				"body": map[string]any{
					"t":                  "new-machine",
					"machineId":          machine.ID,
					"seq":                machine.Seq,
					"metadata":           machine.Metadata,
					"metadataVersion":    machine.MetadataVersion,
					"daemonState":        daemonStateValue,
					"daemonStateVersion": machine.DaemonStateVersion,
					"dataEncryptionKey":  dataKey,
					"active":             machine.Active != 0,
					"activeAt":           machine.LastActiveAt.UnixMilli(),
					"createdAt":          machine.CreatedAt.UnixMilli(),
					"updatedAt":          machine.UpdatedAt.UnixMilli(),
				},
				"createdAt": time.Now().UnixMilli(),
			}
			h.updates.EmitUpdateToUser(userID, newPayload)
		}

		userSeq2, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err != nil {
			log.Printf("Failed to allocate user seq for update-machine: %v", err)
		} else {
			updatePayload := map[string]any{
				"id":  types.NewCUID(),
				"seq": userSeq2,
				"body": map[string]any{
					"t":         "update-machine",
					"machineId": machine.ID,
					"metadata": map[string]any{
						"value":   machine.Metadata,
						"version": machine.MetadataVersion,
					},
				},
				"createdAt": time.Now().UnixMilli(),
			}
			h.updates.EmitUpdateToUser(userID, updatePayload)
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

	// Verify machine exists
	_, err := h.queries.GetMachine(c.Request.Context(), models.GetMachineParams{
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

	// Delete machine
	if err := h.queries.DeleteMachine(c.Request.Context(), models.DeleteMachineParams{
		AccountID: userID,
		ID:        machineID,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to delete machine"})
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
		ephemeral := map[string]any{
			"type":     "machine-activity",
			"id":       machineID,
			"active":   req.Active,
			"activeAt": req.Time,
		}
		h.updates.EmitEphemeralToUser(userID, ephemeral)
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
