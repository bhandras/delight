package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type KVHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

func NewKVHandler(db *sql.DB, updates *websocket.SocketIOServer) *KVHandler {
	return &KVHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

type KVItem struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

// ListKV handles GET /v1/kv
func (h *KVHandler) ListKV(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	prefix := c.DefaultQuery("prefix", "")
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	items, err := h.queries.ListKV(c.Request.Context(), models.ListKVParams{
		AccountID: userID,
		Column2:   prefix,
		Column3:   sql.NullString{String: prefix, Valid: true},
		Limit:     int64(limit),
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list items"})
		return
	}

	result := []KVItem{}
	for _, item := range items {
		if item.Value.Valid {
			result = append(result, KVItem{
				Key:     item.Key,
				Value:   item.Value.String,
				Version: item.Version,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"items": result})
}

// GetKV handles GET /v1/kv/:key
func (h *KVHandler) GetKV(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	key := c.Param("key")

	item, err := h.queries.GetKV(c.Request.Context(), models.GetKVParams{
		AccountID: userID,
		Key:       key,
	})

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Key not found"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get value"})
		return
	}

	if !item.Value.Valid {
		c.JSON(http.StatusNotFound, gin.H{"error": "Key not found"})
		return
	}

	c.JSON(http.StatusOK, KVItem{
		Key:     item.Key,
		Value:   item.Value.String,
		Version: item.Version,
	})
}

type MutationRequest struct {
	Key     string  `json:"key" binding:"required"`
	Value   *string `json:"value"`
	Version int64   `json:"version" binding:"required"`
}

type MutateRequest struct {
	Mutations []MutationRequest `json:"mutations" binding:"required,min=1,max=100"`
}

type MutationResult struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

type MutationError struct {
	Key     string  `json:"key"`
	Error   string  `json:"error"`
	Version int64   `json:"version"`
	Value   *string `json:"value"`
}

// MutateKV handles POST /v1/kv
func (h *KVHandler) MutateKV(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req MutateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Use a transaction for atomic operations
	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to mutate values"})
		return
	}
	defer tx.Rollback()

	txQueries := h.queries.WithTx(tx)
	results := []MutationResult{}
	errors := []MutationError{}
	changes := []map[string]any{}

	for _, mutation := range req.Mutations {
		// Check current version
		current, err := txQueries.GetKV(c.Request.Context(), models.GetKVParams{
			AccountID: userID,
			Key:       mutation.Key,
		})

		// For new keys, version should be -1
		if err == sql.ErrNoRows {
			if mutation.Version != -1 {
				errors = append(errors, MutationError{
					Key:     mutation.Key,
					Error:   "version-mismatch",
					Version: -1,
					Value:   nil,
				})
				continue
			}
		} else if err != nil {
			errors = append(errors, MutationError{
				Key:     mutation.Key,
				Error:   "version-mismatch",
				Version: -1,
				Value:   nil,
			})
			continue
		} else {
			// Existing key - check version matches
			if mutation.Version != current.Version {
				val := &current.Value.String
				if !current.Value.Valid {
					val = nil
				}
				errors = append(errors, MutationError{
					Key:     mutation.Key,
					Error:   "version-mismatch",
					Version: current.Version,
					Value:   val,
				})
				continue
			}
		}

		newVersion := mutation.Version + 1

		value := sql.NullString{}
		if mutation.Value != nil {
			value.String = *mutation.Value
			value.Valid = true
		}

		// Upsert (null value indicates deletion but preserves version history)
		err = txQueries.UpsertKV(c.Request.Context(), models.UpsertKVParams{
			ID:        uuid.New().String(),
			AccountID: userID,
			Key:       mutation.Key,
			Value:     value,
			Version:   newVersion,
		})

		if err != nil {
			errors = append(errors, MutationError{
				Key:     mutation.Key,
				Error:   "version-mismatch",
				Version: mutation.Version,
				Value:   mutation.Value,
			})
			continue
		}

		results = append(results, MutationResult{
			Key:     mutation.Key,
			Version: newVersion,
		})
		var changeValue any
		if mutation.Value != nil {
			changeValue = *mutation.Value
		}
		changes = append(changes, map[string]any{
			"key":     mutation.Key,
			"value":   changeValue,
			"version": newVersion,
		})
	}

	if len(errors) > 0 {
		tx.Rollback()
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"errors":  errors,
		})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to mutate values"})
		return
	}

	if h.updates != nil && len(changes) > 0 {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			updatePayload := map[string]any{
				"id":  types.NewCUID(),
				"seq": userSeq,
				"body": map[string]any{
					"t":       "kv-batch-update",
					"changes": changes,
				},
				"createdAt": time.Now().UnixMilli(),
			}
			h.updates.EmitUpdateToUser(userID, updatePayload)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"results": results,
	})
}

type BulkGetRequest struct {
	Keys []string `json:"keys" binding:"required,min=1,max=100"`
}

// BulkGetKV handles POST /v1/kv/bulk
func (h *KVHandler) BulkGetKV(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req BulkGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	values := []KVItem{}
	for _, key := range req.Keys {
		item, err := h.queries.GetKV(c.Request.Context(), models.GetKVParams{
			AccountID: userID,
			Key:       key,
		})

		if err == nil && item.Value.Valid {
			values = append(values, KVItem{
				Key:     item.Key,
				Value:   item.Value.String,
				Version: item.Version,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"values": values})
}
