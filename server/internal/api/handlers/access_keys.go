package handlers

import (
	"database/sql"
	"net/http"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
)

type AccessKeyHandler struct {
	db      *sql.DB
	queries *models.Queries
}

func NewAccessKeyHandler(db *sql.DB) *AccessKeyHandler {
	return &AccessKeyHandler{
		db:      db,
		queries: models.New(db),
	}
}

type CreateAccessKeyRequest struct {
	Data string `json:"data" binding:"required"`
}

type UpdateAccessKeyRequest struct {
	Data            string `json:"data" binding:"required"`
	ExpectedVersion int64  `json:"expectedVersion" binding:"required"`
}

func (h *AccessKeyHandler) GetAccessKey(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("sessionId")
	machineID := c.Param("machineId")

	session, err := h.queries.GetSessionByID(c.Request.Context(), sessionID)
	if err != nil || session.AccountID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session or machine not found"})
		return
	}
	machine, err := h.queries.GetMachine(c.Request.Context(), models.GetMachineParams{
		AccountID: userID,
		ID:        machineID,
	})
	if err != nil || machine.AccountID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session or machine not found"})
		return
	}

	accessKey, err := h.queries.GetAccessKey(c.Request.Context(), models.GetAccessKeyParams{
		AccountID: userID,
		MachineID: machineID,
		SessionID: sessionID,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusOK, gin.H{"accessKey": nil})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get access key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessKey": gin.H{
			"data":        accessKey.Data,
			"dataVersion": accessKey.DataVersion,
			"createdAt":   accessKey.CreatedAt.UnixMilli(),
			"updatedAt":   accessKey.UpdatedAt.UnixMilli(),
		},
	})
}

func (h *AccessKeyHandler) CreateAccessKey(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("sessionId")
	machineID := c.Param("machineId")

	var req CreateAccessKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	session, err := h.queries.GetSessionByID(c.Request.Context(), sessionID)
	if err != nil || session.AccountID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session or machine not found"})
		return
	}
	machine, err := h.queries.GetMachine(c.Request.Context(), models.GetMachineParams{
		AccountID: userID,
		ID:        machineID,
	})
	if err != nil || machine.AccountID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session or machine not found"})
		return
	}

	existing, err := h.queries.GetAccessKey(c.Request.Context(), models.GetAccessKeyParams{
		AccountID: userID,
		MachineID: machineID,
		SessionID: sessionID,
	})
	if err == nil && existing.ID != "" {
		c.JSON(http.StatusConflict, gin.H{"error": "Access key already exists"})
		return
	} else if err != nil && err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create access key"})
		return
	}

	if err := h.queries.CreateAccessKey(c.Request.Context(), models.CreateAccessKeyParams{
		ID:          types.NewCUID(),
		AccountID:   userID,
		MachineID:   machineID,
		SessionID:   sessionID,
		Data:        req.Data,
		DataVersion: 1,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create access key"})
		return
	}

	accessKey, err := h.queries.GetAccessKey(c.Request.Context(), models.GetAccessKeyParams{
		AccountID: userID,
		MachineID: machineID,
		SessionID: sessionID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create access key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"accessKey": gin.H{
			"data":        accessKey.Data,
			"dataVersion": accessKey.DataVersion,
			"createdAt":   accessKey.CreatedAt.UnixMilli(),
			"updatedAt":   accessKey.UpdatedAt.UnixMilli(),
		},
	})
}

func (h *AccessKeyHandler) UpdateAccessKey(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("sessionId")
	machineID := c.Param("machineId")

	var req UpdateAccessKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	current, err := h.queries.GetAccessKey(c.Request.Context(), models.GetAccessKeyParams{
		AccountID: userID,
		MachineID: machineID,
		SessionID: sessionID,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Access key not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to update access key"})
		return
	}

	if current.DataVersion != req.ExpectedVersion {
		c.JSON(http.StatusOK, gin.H{
			"success":        false,
			"error":          "version-mismatch",
			"currentVersion": current.DataVersion,
			"currentData":    current.Data,
		})
		return
	}

	updatedRows, err := h.queries.UpdateAccessKey(c.Request.Context(), models.UpdateAccessKeyParams{
		Data:          req.Data,
		DataVersion:   req.ExpectedVersion + 1,
		AccountID:     userID,
		MachineID:     machineID,
		SessionID:     sessionID,
		DataVersion_2: req.ExpectedVersion,
	})
	if err != nil || updatedRows == 0 {
		latest, err := h.queries.GetAccessKey(c.Request.Context(), models.GetAccessKeyParams{
			AccountID: userID,
			MachineID: machineID,
			SessionID: sessionID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to update access key"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":        false,
			"error":          "version-mismatch",
			"currentVersion": latest.DataVersion,
			"currentData":    latest.Data,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"version": req.ExpectedVersion + 1,
	})
}
