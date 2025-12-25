package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/gin-gonic/gin"
)

type FeedHandler struct {
	queries *models.Queries
}

func NewFeedHandler(db *sql.DB) *FeedHandler {
	return &FeedHandler{
		queries: models.New(db),
	}
}

type FeedItemResponse struct {
	ID        string      `json:"id"`
	Body      interface{} `json:"body"`
	RepeatKey *string     `json:"repeatKey"`
	Cursor    string      `json:"cursor"`
	CreatedAt int64       `json:"createdAt"`
}

func (h *FeedHandler) ListFeed(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	limit := int64(50)
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}

	beforeCounter, beforeSet := parseFeedCursor(c.Query("before"))
	afterCounter, afterSet := parseFeedCursor(c.Query("after"))

	params := models.ListFeedItemsParams{
		UserID: userID,
		Limit:  limit,
	}
	if beforeSet {
		params.Column2 = beforeCounter
		params.Counter = beforeCounter
	}
	if afterSet {
		params.Column4 = afterCounter
		params.Counter_2 = afterCounter
	}

	items, err := h.queries.ListFeedItems(c.Request.Context(), params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get feed"})
		return
	}

	response := make([]FeedItemResponse, 0, len(items))
	for _, item := range items {
		var body interface{}
		if err := json.Unmarshal([]byte(item.Body), &body); err != nil {
			body = nil
		}
		var repeatKey *string
		if item.RepeatKey.Valid {
			repeatKey = &item.RepeatKey.String
		}
		response = append(response, FeedItemResponse{
			ID:        item.ID,
			Body:      body,
			RepeatKey: repeatKey,
			Cursor:    feedCursor(item.Counter),
			CreatedAt: item.CreatedAt.UnixMilli(),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"items":   response,
		"hasMore": len(response) == int(limit),
	})
}

func parseFeedCursor(cursor string) (int64, bool) {
	if cursor == "" {
		return 0, false
	}
	parts := strings.Split(cursor, "-")
	if len(parts) != 2 {
		return 0, false
	}
	counter, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return counter, true
}

func feedCursor(counter int64) string {
	return "0-" + strconv.FormatInt(counter, 10)
}
