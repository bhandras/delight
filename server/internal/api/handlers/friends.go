package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
)

type FriendsHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

func NewFriendsHandler(db *sql.DB, updates *websocket.SocketIOServer) *FriendsHandler {
	return &FriendsHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

type friendRequest struct {
	UID string `json:"uid" binding:"required"`
}

type UserProfile struct {
	ID        string  `json:"id"`
	FirstName string  `json:"firstName"`
	LastName  *string `json:"lastName"`
	Avatar    *Avatar `json:"avatar"`
	Username  string  `json:"username"`
	Bio       *string `json:"bio"`
	Status    string  `json:"status"`
}

func (h *FriendsHandler) ListFriends(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	relationships, err := h.queries.ListRelationshipsByFrom(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list friends"})
		return
	}

	friends := make([]UserProfile, 0, len(relationships))
	for _, rel := range relationships {
		account, err := h.queries.GetAccountByID(c.Request.Context(), rel.ToUserID)
		if err != nil {
			continue
		}
		friends = append(friends, buildUserProfile(account, rel.Status))
	}

	c.JSON(http.StatusOK, gin.H{"friends": friends})
}

func (h *FriendsHandler) AddFriend(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req friendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	if req.UID == userID {
		c.JSON(http.StatusOK, gin.H{"user": nil})
		return
	}

	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}
	defer tx.Rollback()

	txQueries := h.queries.WithTx(tx)

	target, err := txQueries.GetAccountByID(c.Request.Context(), req.UID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	currentStatus := relationshipStatus(getRelationship(c, txQueries, userID, req.UID))
	targetStatus := relationshipStatus(getRelationship(c, txQueries, req.UID, userID))
	now := time.Now()

	var resultStatus string
	var feedItems []feedItem

	if targetStatus == "requested" {
		if err := setRelationship(c, txQueries, userID, req.UID, "friend", now); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		if err := setRelationship(c, txQueries, req.UID, userID, "friend", now); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		resultStatus = "friend"
		feedItems = append(feedItems,
			feedItem{UserID: userID, Body: map[string]interface{}{"kind": "friend_accepted", "uid": req.UID}, RepeatKey: "friend_accepted:" + req.UID},
			feedItem{UserID: req.UID, Body: map[string]interface{}{"kind": "friend_accepted", "uid": userID}, RepeatKey: "friend_accepted:" + userID},
		)
	} else if currentStatus == "none" || currentStatus == "rejected" {
		if err := setRelationship(c, txQueries, userID, req.UID, "requested", time.Time{}); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		if targetStatus == "none" {
			if err := setRelationship(c, txQueries, req.UID, userID, "pending", time.Time{}); err != nil {
				c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
				return
			}
		}
		resultStatus = "requested"
		feedItems = append(feedItems, feedItem{
			UserID:    req.UID,
			Body:      map[string]interface{}{"kind": "friend_request", "uid": userID},
			RepeatKey: "friend_request:" + userID,
		})
	} else {
		resultStatus = currentStatus
	}

	feedEvents, err := h.createFeedItems(c, txQueries, feedItems)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update feed"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	h.emitRelationshipUpdates(userID, req.UID, resultStatus, relationshipAction(currentStatus, resultStatus), now.UnixMilli())
	if targetStatus == "requested" {
		h.emitRelationshipUpdates(req.UID, userID, "friend", relationshipAction(targetStatus, "friend"), now.UnixMilli())
	} else if currentStatus == "none" || currentStatus == "rejected" {
		h.emitRelationshipUpdates(req.UID, userID, "pending", relationshipAction(targetStatus, "pending"), now.UnixMilli())
	}
	h.emitFeedUpdates(feedEvents)

	c.JSON(http.StatusOK, gin.H{"user": buildUserProfile(target, resultStatus)})
}

func (h *FriendsHandler) RemoveFriend(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req friendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}
	defer tx.Rollback()

	txQueries := h.queries.WithTx(tx)
	target, err := txQueries.GetAccountByID(c.Request.Context(), req.UID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	currentStatus := relationshipStatus(getRelationship(c, txQueries, userID, req.UID))
	targetStatus := relationshipStatus(getRelationship(c, txQueries, req.UID, userID))

	resultStatus := currentStatus
	now := time.Now()

	switch currentStatus {
	case "requested":
		if err := setRelationship(c, txQueries, userID, req.UID, "rejected", time.Time{}); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		resultStatus = "rejected"
	case "friend":
		if err := setRelationship(c, txQueries, req.UID, userID, "requested", time.Time{}); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		if err := setRelationship(c, txQueries, userID, req.UID, "pending", time.Time{}); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		resultStatus = "requested"
	case "pending":
		if err := setRelationship(c, txQueries, userID, req.UID, "none", time.Time{}); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update relationship"})
			return
		}
		if targetStatus != "rejected" {
			_ = setRelationship(c, txQueries, req.UID, userID, "none", time.Time{})
		}
		resultStatus = "none"
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	h.emitRelationshipUpdates(userID, req.UID, resultStatus, relationshipAction(currentStatus, resultStatus), now.UnixMilli())
	if currentStatus == "friend" {
		h.emitRelationshipUpdates(req.UID, userID, "requested", relationshipAction(targetStatus, "requested"), now.UnixMilli())
	} else if currentStatus == "pending" && targetStatus != "rejected" {
		h.emitRelationshipUpdates(req.UID, userID, "none", relationshipAction(targetStatus, "none"), now.UnixMilli())
	}

	c.JSON(http.StatusOK, gin.H{"user": buildUserProfile(target, resultStatus)})
}

func buildUserProfile(account models.Account, status string) UserProfile {
	var lastName *string
	if account.LastName.Valid {
		lastName = &account.LastName.String
	}
	var avatar *Avatar
	if account.AvatarUrl.Valid {
		avatar = &Avatar{
			URL: account.AvatarUrl.String,
		}
		if account.AvatarWidth.Valid {
			avatar.Width = &account.AvatarWidth.Int64
		}
		if account.AvatarHeight.Valid {
			avatar.Height = &account.AvatarHeight.Int64
		}
		if account.AvatarThumbhash.Valid {
			avatar.Thumbhash = &account.AvatarThumbhash.String
		}
	}
	username := ""
	if account.Username.Valid {
		username = account.Username.String
	}
	return UserProfile{
		ID:        account.ID,
		FirstName: nullSafeString(account.FirstName),
		LastName:  lastName,
		Avatar:    avatar,
		Username:  username,
		Bio:       nil,
		Status:    status,
	}
}

func nullSafeString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func (h *FriendsHandler) emitRelationshipUpdates(userID, otherID, status, action string, timestamp int64) {
	if h.updates == nil {
		return
	}
	userSeq, err := h.queries.UpdateAccountSeq(context.Background(), userID)
	if err != nil {
		return
	}
	updatePayload := map[string]any{
		"id":        types.NewCUID(),
		"seq":       userSeq,
		"createdAt": time.Now().UnixMilli(),
		"body": map[string]any{
			"t":          "relationship-updated",
			"fromUserId": userID,
			"toUserId":   otherID,
			"status":     status,
			"action":     action,
			"timestamp":  timestamp,
		},
	}
	h.updates.EmitUpdateToUser(userID, updatePayload)
}

type feedItem struct {
	UserID    string
	Body      map[string]interface{}
	RepeatKey string
}

type feedEvent struct {
	UserID string
	Item   map[string]interface{}
}

func (h *FriendsHandler) createFeedItems(c *gin.Context, txQueries *models.Queries, items []feedItem) ([]feedEvent, error) {
	if len(items) == 0 {
		return nil, nil
	}

	events := make([]feedEvent, 0, len(items))
	for _, item := range items {
		bodyBytes, err := json.Marshal(item.Body)
		if err != nil {
			return nil, err
		}

		repeatKey := sql.NullString{Valid: false}
		if item.RepeatKey != "" {
			repeatKey = sql.NullString{String: item.RepeatKey, Valid: true}
			if err := txQueries.DeleteFeedItemsByRepeatKey(c.Request.Context(), models.DeleteFeedItemsByRepeatKeyParams{
				UserID:    item.UserID,
				RepeatKey: repeatKey,
			}); err != nil {
				return nil, err
			}
		}

		counter, err := txQueries.UpdateAccountFeedSeq(c.Request.Context(), item.UserID)
		if err != nil {
			return nil, err
		}

		feedID := types.NewCUID()
		if err := txQueries.CreateFeedItem(c.Request.Context(), models.CreateFeedItemParams{
			ID:        feedID,
			UserID:    item.UserID,
			Counter:   counter,
			RepeatKey: repeatKey,
			Body:      string(bodyBytes),
		}); err != nil {
			return nil, err
		}

		eventItem := map[string]interface{}{
			"id":        feedID,
			"body":      item.Body,
			"cursor":    feedCursor(counter),
			"createdAt": time.Now().UnixMilli(),
		}
		if item.RepeatKey != "" {
			eventItem["repeatKey"] = item.RepeatKey
		}
		events = append(events, feedEvent{
			UserID: item.UserID,
			Item:   eventItem,
		})
	}

	return events, nil
}

func (h *FriendsHandler) emitFeedUpdates(events []feedEvent) {
	if h.updates == nil {
		return
	}
	for _, event := range events {
		userSeq, err := h.queries.UpdateAccountSeq(context.Background(), event.UserID)
		if err != nil {
			continue
		}
		updatePayload := map[string]any{
			"id":        types.NewCUID(),
			"seq":       userSeq,
			"createdAt": time.Now().UnixMilli(),
			"body": map[string]any{
				"t":         "new-feed-post",
				"id":        event.Item["id"],
				"body":      event.Item["body"],
				"cursor":    event.Item["cursor"],
				"createdAt": event.Item["createdAt"],
				"repeatKey": event.Item["repeatKey"],
			},
		}
		h.updates.EmitUpdateToUser(event.UserID, updatePayload)
	}
}

func relationshipStatus(status string, ok bool) string {
	if !ok || status == "" {
		return "none"
	}
	return status
}

func relationshipAction(previous, next string) string {
	if next == "none" {
		return "deleted"
	}
	if previous == "none" {
		return "created"
	}
	return "updated"
}

func getRelationship(c *gin.Context, queries *models.Queries, fromID, toID string) (string, bool) {
	rel, err := queries.GetRelationship(c.Request.Context(), models.GetRelationshipParams{
		FromUserID: fromID,
		ToUserID:   toID,
	})
	if err != nil {
		return "none", false
	}
	return rel.Status, true
}

func setRelationship(c *gin.Context, queries *models.Queries, fromID, toID, status string, acceptedAt time.Time) error {
	var accepted sql.NullTime
	if status == "friend" && !acceptedAt.IsZero() {
		accepted = sql.NullTime{Time: acceptedAt, Valid: true}
	}
	return queries.UpsertRelationship(c.Request.Context(), models.UpsertRelationshipParams{
		FromUserID:     fromID,
		ToUserID:       toID,
		Status:         status,
		AcceptedAt:     accepted,
		LastNotifiedAt: sql.NullTime{Valid: false},
	})
}
