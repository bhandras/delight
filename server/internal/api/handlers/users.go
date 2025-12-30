package handlers

import (
	"database/sql"
	"net/http"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
)

type UserHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

func NewUserHandler(db *sql.DB, updates *websocket.SocketIOServer) *UserHandler {
	return &UserHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

// UserProfileResponse represents a user profile
type UserProfileResponse struct {
	ID                string         `json:"id"`
	Timestamp         int64          `json:"timestamp"`
	PublicKey         string         `json:"publicKey"`
	FirstName         *string        `json:"firstName"`
	LastName          *string        `json:"lastName"`
	Username          *string        `json:"username"`
	Avatar            *Avatar        `json:"avatar"`
	Github            *GitHubProfile `json:"github"`
	ConnectedServices []string       `json:"connectedServices"`
	Settings          *string        `json:"settings"`
	SettingsVersion   int64          `json:"settingsVersion"`
}

// GitHubProfile represents GitHub profile data
type GitHubProfile struct {
	ID        int64   `json:"id"`
	Login     string  `json:"login"`
	Name      string  `json:"name"`
	AvatarURL string  `json:"avatar_url"`
	Email     *string `json:"email,omitempty"`
	Bio       *string `json:"bio"`
}

// Avatar represents user avatar metadata
type Avatar struct {
	URL       string  `json:"url"`
	Width     *int64  `json:"width"`
	Height    *int64  `json:"height"`
	Thumbhash *string `json:"thumbhash"`
}

// UpdateSettingsRequest represents settings update
type UpdateSettingsRequest struct {
	Settings        *string `json:"settings"`
	ExpectedVersion int64   `json:"expectedVersion"`
}

// UpdateProfileRequest represents profile update
type UpdateProfileRequest struct {
	FirstName *string `json:"firstName"`
	LastName  *string `json:"lastName"`
	Username  *string `json:"username"`
}

// GetProfile handles GET /v1/user
func (h *UserHandler) GetProfile(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	account, err := h.queries.GetAccountByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to get profile"})
		return
	}

	c.JSON(http.StatusOK, h.toProfileResponse(account))
}

// GetSettings handles GET /v1/account/settings
func (h *UserHandler) GetSettings(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	account, err := h.queries.GetAccountByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get account settings"})
		return
	}

	var settings *string
	if account.Settings.Valid {
		settings = &account.Settings.String
	}

	c.JSON(http.StatusOK, gin.H{
		"settings":        settings,
		"settingsVersion": account.SettingsVersion,
	})
}

// UpdateSettings handles POST /v1/user/settings
func (h *UserHandler) UpdateSettings(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	settingsValue := sql.NullString{}
	if req.Settings != nil {
		settingsValue = sql.NullString{String: *req.Settings, Valid: true}
	}

	// Update with optimistic concurrency control
	err := h.queries.UpdateAccountSettings(c.Request.Context(), models.UpdateAccountSettingsParams{
		Settings:          settingsValue,
		SettingsVersion:   req.ExpectedVersion + 1,
		ID:                userID,
		SettingsVersion_2: req.ExpectedVersion,
	})

	if err != nil {
		current, fetchErr := h.queries.GetAccountByID(c.Request.Context(), userID)
		if fetchErr != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to fetch current settings"})
			return
		}
		var currentSettings *string
		if current.Settings.Valid {
			currentSettings = &current.Settings.String
		}
		c.JSON(http.StatusOK, gin.H{
			"success":         false,
			"error":           "version-mismatch",
			"currentVersion":  current.SettingsVersion,
			"currentSettings": currentSettings,
		})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			var settingsValue any
			if req.Settings != nil {
				settingsValue = *req.Settings
			}
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateAccount{
					T:  "update-account",
					ID: userID,
					Settings: &protocolwire.VersionedAny{
						Value:   settingsValue,
						Version: req.ExpectedVersion + 1,
					},
				},
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"version": req.ExpectedVersion + 1,
	})
}

// UpdateProfile handles POST /v1/user/profile
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	firstName := sql.NullString{}
	if req.FirstName != nil {
		firstName.String = *req.FirstName
		firstName.Valid = true
	}

	lastName := sql.NullString{}
	if req.LastName != nil {
		lastName.String = *req.LastName
		lastName.Valid = true
	}

	username := sql.NullString{}
	if req.Username != nil {
		username.String = *req.Username
		username.Valid = true
	}

	err := h.queries.UpdateAccountProfile(c.Request.Context(), models.UpdateAccountProfileParams{
		FirstName: firstName,
		LastName:  lastName,
		Username:  username,
		ID:        userID,
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update profile"})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateAccount{
					T:         "update-account",
					ID:        userID,
					FirstName: req.FirstName,
					LastName:  req.LastName,
					Username:  req.Username,
				},
			})
		}
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// DeleteAvatar handles DELETE /v1/user/avatar
func (h *UserHandler) DeleteAvatar(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	err := h.queries.DeleteAccountAvatar(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to delete avatar"})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateAccount{
					T:      "update-account",
					ID:     userID,
					Avatar: protocolwire.Null{},
				},
			})
		}
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// Helper to convert account to profile response
func (h *UserHandler) toProfileResponse(account models.Account) UserProfileResponse {
	var firstName, lastName, username *string
	if account.FirstName.Valid {
		firstName = &account.FirstName.String
	}
	if account.LastName.Valid {
		lastName = &account.LastName.String
	}
	if account.Username.Valid {
		username = &account.Username.String
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

	var settings *string
	if account.Settings.Valid {
		settings = &account.Settings.String
	}

	return UserProfileResponse{
		ID:                account.ID,
		Timestamp:         account.CreatedAt.Unix(),
		PublicKey:         account.PublicKey,
		FirstName:         firstName,
		LastName:          lastName,
		Username:          username,
		Avatar:            avatar,
		Github:            nil,
		ConnectedServices: []string{},
		Settings:          settings,
		SettingsVersion:   account.SettingsVersion,
	}
}
