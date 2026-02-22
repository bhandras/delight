package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/push"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
)

const (
	// pushNotifyTimeout bounds the total time spent sending a push batch.
	pushNotifyTimeout = 10 * time.Second
	// maxPushCiphertextLength caps encrypted payload size to fit APNs limits.
	maxPushCiphertextLength = 3200
)

// PushHandler manages push token registration and encrypted push delivery.
type PushHandler struct {
	db      *sql.DB
	queries *models.Queries
	sender  push.Sender
}

// NewPushHandler constructs a PushHandler.
func NewPushHandler(db *sql.DB, sender push.Sender) *PushHandler {
	return &PushHandler{
		db:      db,
		queries: models.New(db),
		sender:  sender,
	}
}

// RegisterPushTokenRequest represents the request to register a device token.
type RegisterPushTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// RegisterPushToken handles POST /v1/push-tokens.
func (h *PushHandler) RegisterPushToken(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req RegisterPushTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	tokenValue := strings.TrimSpace(req.Token)
	if tokenValue == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "token is required"})
		return
	}

	if err := h.ensureAccountExists(c, userID); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create account"})
		return
	}

	_, err := h.queries.CreatePushToken(c.Request.Context(), models.CreatePushTokenParams{
		ID:        uuid.NewString(),
		AccountID: userID,
		Token:     tokenValue,
	})
	if err != nil {
		if isSQLiteConstraintError(err) {
			if touchErr := h.touchPushToken(c.Request.Context(), userID, tokenValue); touchErr != nil {
				c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update push token"})
				return
			}
		} else {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to register push token"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SendPushNotificationRequest represents the request to send an encrypted push.
type SendPushNotificationRequest struct {
	Ciphertext string `json:"ciphertext" binding:"required"`
}

// SendPushNotificationResponse reports push send results.
type SendPushNotificationResponse struct {
	Success bool `json:"success"`
	Sent    int  `json:"sent"`
	Failed  int  `json:"failed"`
}

// SendPushNotification handles POST /v1/push-notifications.
func (h *PushHandler) SendPushNotification(c *gin.Context) {
	if h.sender == nil {
		c.JSON(http.StatusServiceUnavailable, types.ErrorResponse{Error: "push notifications not configured"})
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req SendPushNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	ciphertext := strings.TrimSpace(req.Ciphertext)
	if ciphertext == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "ciphertext is required"})
		return
	}
	if len(ciphertext) > maxPushCiphertextLength {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "ciphertext too large"})
		return
	}

	tokens, err := h.queries.ListPushTokens(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to list push tokens"})
		return
	}

	deviceTokens := make([]string, 0, len(tokens))
	for _, tokenValue := range tokens {
		deviceTokens = append(deviceTokens, tokenValue.Token)
	}
	if len(deviceTokens) == 0 {
		c.JSON(http.StatusOK, SendPushNotificationResponse{Success: true, Sent: 0, Failed: 0})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), pushNotifyTimeout)
	defer cancel()

	result, err := h.sender.SendEncrypted(ctx, deviceTokens, ciphertext)
	if err != nil {
		logger.Warnf("push send error: %v", err)
	}

	c.JSON(http.StatusOK, SendPushNotificationResponse{
		Success: true,
		Sent:    result.Sent,
		Failed:  result.Failed,
	})
}

// ensureAccountExists ensures the authenticated account record exists.
func (h *PushHandler) ensureAccountExists(c *gin.Context, userID string) error {
	_, err := h.queries.GetAccountByID(c.Request.Context(), userID)
	if err == sql.ErrNoRows {
		_, createErr := h.queries.CreateAccount(c.Request.Context(), models.CreateAccountParams{
			ID:        userID,
			PublicKey: "auto-created-" + userID,
		})
		return createErr
	}
	return err
}

// touchPushToken updates the updated_at timestamp for an existing token.
func (h *PushHandler) touchPushToken(ctx context.Context, userID string, tokenValue string) error {
	_, err := h.db.ExecContext(
		ctx,
		"UPDATE account_push_tokens SET updated_at = CURRENT_TIMESTAMP WHERE account_id = ? AND token = ?",
		userID,
		tokenValue,
	)
	return err
}

// isSQLiteConstraintError reports whether err is a constraint error.
func isSQLiteConstraintError(err error) bool {
	var sqlErr sqlite3.Error
	if errors.As(err, &sqlErr) {
		return sqlErr.Code == sqlite3.ErrConstraint
	}
	return false
}
