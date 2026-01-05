package handlers

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"os"
	"time"

	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	db         *sql.DB
	queries    *models.Queries
	jwtManager *crypto.JWTManager
}

func NewAuthHandler(db *sql.DB, jwtManager *crypto.JWTManager) *AuthHandler {
	return &AuthHandler{
		db:         db,
		queries:    models.New(db),
		jwtManager: jwtManager,
	}
}

const (
	// authChallengeBytes is the random byte length of server-issued auth
	// challenges.
	authChallengeBytes = 32
	// authChallengeTTL is how long a challenge remains valid for.
	authChallengeTTL = 5 * time.Minute
)

// PostAuthChallenge returns a server-issued challenge for /v1/auth.
// POST /v1/auth/challenge
func (h *AuthHandler) PostAuthChallenge(c *gin.Context) {
	var req types.AuthChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	publicKeyHex, err := crypto.PublicKeyToHex(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	// Best-effort pruning.
	_ = h.queries.DeleteExpiredAuthChallenges(c.Request.Context())

	challengeID := types.NewCUID()
	challenge := make([]byte, authChallengeBytes)
	if _, err := crypto.RandBytes(challenge); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to generate challenge"})
		return
	}
	expiresAt := time.Now().Add(authChallengeTTL)

	if err := h.queries.CreateAuthChallenge(c.Request.Context(), models.CreateAuthChallengeParams{
		ID:        challengeID,
		PublicKey: publicKeyHex,
		Challenge: challenge,
		ExpiresAt: expiresAt,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create challenge"})
		return
	}

	c.JSON(http.StatusOK, types.AuthChallengeResponse{
		ChallengeID: challengeID,
		Challenge:   base64.StdEncoding.EncodeToString(challenge),
	})
}

// PostAuth handles challenge-response authentication
// POST /v1/auth
func (h *AuthHandler) PostAuth(c *gin.Context) {
	var req types.AuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Convert public key to hex for storage
	publicKeyHex, err := crypto.PublicKeyToHex(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	challenge, err := h.queries.GetAuthChallenge(c.Request.Context(), models.GetAuthChallengeParams{
		ID:        req.ChallengeID,
		PublicKey: publicKeyHex,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid challenge"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	if time.Now().After(challenge.ExpiresAt) {
		_ = h.queries.DeleteAuthChallenge(c.Request.Context(), req.ChallengeID)
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "expired challenge"})
		return
	}

	// Verify signature over server-issued challenge bytes.
	valid, err := crypto.VerifyAuthSignature(req.PublicKey, challenge.Challenge, req.Signature)
	if err != nil || !valid {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid signature"})
		return
	}

	// One-time use: delete challenge after success.
	_ = h.queries.DeleteAuthChallenge(c.Request.Context(), req.ChallengeID)

	// Find or create account
	account, err := h.queries.GetAccountByPublicKey(c.Request.Context(), publicKeyHex)
	if err == sql.ErrNoRows {
		// Create new account
		account, err = h.queries.CreateAccount(c.Request.Context(), models.CreateAccountParams{
			ID:        types.NewCUID(),
			PublicKey: publicKeyHex,
		})
		if err != nil {
			logger.Errorf("PostAuth: CreateAccount failed: %v", err)
			if os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") == "1" {
				c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create account"})
			return
		}
	} else if err != nil {
		logger.Errorf("PostAuth: GetAccountByPublicKey failed: %v", err)
		if os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") == "1" {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Generate JWT token
	token, err := h.jwtManager.CreateToken(account.ID, nil)
	if err != nil {
		logger.Errorf("PostAuth: CreateToken failed: %v", err)
		if os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") == "1" {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create token"})
		return
	}

	c.JSON(http.StatusOK, types.AuthResponse{
		Success: true,
		Token:   token,
	})
}

// PostAuthRequest handles CLI authentication request (step 1 of QR flow)
// POST /v1/auth/request
func (h *AuthHandler) PostAuthRequest(c *gin.Context) {
	var req types.TerminalAuthRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Convert public key to hex
	publicKeyHex, err := crypto.PublicKeyToHex(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	// Create auth request
	requestID := types.NewCUID()
	_, err = h.queries.CreateTerminalAuthRequest(c.Request.Context(), models.CreateTerminalAuthRequestParams{
		ID:        requestID,
		PublicKey: publicKeyHex,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create auth request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":    requestID,
		"state": "requested",
	})
}

// GetAuthRequestStatus handles CLI polling for auth status
// GET /v1/auth/request/status?publicKey=<base64>
// This endpoint checks both terminal_auth_requests and account_auth_requests tables
func (h *AuthHandler) GetAuthRequestStatus(c *gin.Context) {
	publicKeyB64 := c.Query("publicKey")
	if publicKeyB64 == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "missing publicKey parameter"})
		return
	}

	// Convert to hex
	publicKeyHex, err := crypto.PublicKeyToHex(publicKeyB64)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	// First try terminal auth requests
	authReq, err := h.queries.GetTerminalAuthRequest(c.Request.Context(), publicKeyHex)
	if err == nil {
		// Found in terminal_auth_requests
		if authReq.Response.Valid && authReq.ResponseAccountID.Valid {
			response := authReq.Response.String
			c.JSON(http.StatusOK, types.TerminalAuthStatusResponse{
				Status:   "authorized",
				Response: &response,
			})
		} else {
			c.JSON(http.StatusOK, types.TerminalAuthStatusResponse{
				Status: "pending",
			})
		}
		return
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Not found in terminal_auth_requests, try account_auth_requests
	accountReq, err := h.queries.GetAccountAuthRequest(c.Request.Context(), publicKeyHex)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "auth request not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Found in account_auth_requests
	if accountReq.Response.Valid && accountReq.ResponseAccountID.Valid {
		response := accountReq.Response.String
		c.JSON(http.StatusOK, types.TerminalAuthStatusResponse{
			Status:   "authorized",
			Response: &response,
		})
	} else {
		c.JSON(http.StatusOK, types.TerminalAuthStatusResponse{
			Status: "pending",
		})
	}
}

// PostAuthResponse handles mobile app approving auth request (step 2 of QR flow)
// POST /v1/auth/response
// Requires authentication
// This endpoint tries both terminal_auth_requests and account_auth_requests tables
func (h *AuthHandler) PostAuthResponse(c *gin.Context) {
	// Get authenticated user ID
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "unauthorized"})
		return
	}

	var req types.TerminalAuthResponseBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Convert public key to hex
	publicKeyHex, err := crypto.PublicKeyToHex(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	// Verify the account exists (foreign key constraint)
	userIDStr := userID.(string)
	_, err = h.queries.GetAccountByID(c.Request.Context(), userIDStr)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "account not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Try to update terminal_auth_requests first
	err = h.queries.UpdateTerminalAuthResponse(c.Request.Context(), models.UpdateTerminalAuthResponseParams{
		Response: sql.NullString{
			String: req.Response,
			Valid:  true,
		},
		ResponseAccountID: sql.NullString{
			String: userIDStr,
			Valid:  true,
		},
		PublicKey: publicKeyHex,
	})
	if err == nil {
		c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
		return
	}

	// If terminal auth request not found, try account_auth_requests
	err = h.queries.UpdateAccountAuthResponse(c.Request.Context(), models.UpdateAccountAuthResponseParams{
		Response: sql.NullString{
			String: req.Response,
			Valid:  true,
		},
		ResponseAccountID: sql.NullString{
			String: userIDStr,
			Valid:  true,
		},
		PublicKey: publicKeyHex,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update auth request"})
		return
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// PostAccountAuthRequest handles device linking request (step 1 of account QR flow)
// POST /v1/auth/account/request
// This endpoint serves dual purpose:
// 1. Initial POST creates the auth request and auto-approves for NEW accounts
// 2. Subsequent POSTs poll for authorization status
func (h *AuthHandler) PostAccountAuthRequest(c *gin.Context) {
	var req types.TerminalAuthRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Convert public key to hex
	publicKeyHex, err := crypto.PublicKeyToHex(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	// Check if auth request already exists for this public key
	authReq, err := h.queries.GetAccountAuthRequest(c.Request.Context(), publicKeyHex)
	if err == sql.ErrNoRows {
		// No existing auth request - create a new one
		// All devices (including first) must be approved via QR code flow
		requestID := types.NewCUID()
		_, err = h.queries.CreateAccountAuthRequest(c.Request.Context(), models.CreateAccountAuthRequestParams{
			ID:        requestID,
			PublicKey: publicKeyHex,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create auth request"})
			return
		}

		// Return pending state - CLI will display QR code
		c.JSON(http.StatusOK, gin.H{
			"id":    requestID,
			"state": "requested",
		})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "database error"})
		return
	}

	// Auth request exists - check if authorized
	if authReq.Response.Valid && authReq.ResponseAccountID.Valid {
		// Generate token for the account
		token, err := h.jwtManager.CreateToken(authReq.ResponseAccountID.String, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to create token"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"state":    "authorized",
			"token":    token,
			"response": authReq.Response.String,
		})
	} else {
		// Still pending
		c.JSON(http.StatusOK, gin.H{
			"state": "requested",
		})
	}
}

// PostAccountAuthResponse handles authenticated device approving device linking request
// POST /v1/auth/account/response
// Requires authentication
func (h *AuthHandler) PostAccountAuthResponse(c *gin.Context) {
	// Get authenticated user ID
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "unauthorized"})
		return
	}

	var req types.TerminalAuthResponseBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	// Convert public key to hex
	publicKeyHex, err := crypto.PublicKeyToHex(req.PublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid public key"})
		return
	}

	// Update auth request with response
	err = h.queries.UpdateAccountAuthResponse(c.Request.Context(), models.UpdateAccountAuthResponseParams{
		Response: sql.NullString{
			String: req.Response,
			Valid:  true,
		},
		ResponseAccountID: sql.NullString{
			String: userID.(string),
			Valid:  true,
		},
		PublicKey: publicKeyHex,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to update auth request"})
		return
	}

	c.JSON(http.StatusOK, types.SuccessResponse{Success: true})
}

// CleanupOldAuthRequests removes auth requests older than 1 hour
// Should be called periodically (e.g., every 10 minutes)
func (h *AuthHandler) CleanupOldAuthRequests(c *gin.Context) error {
	ctx := c.Request.Context()
	if err := h.queries.DeleteOldTerminalAuthRequests(ctx); err != nil {
		return err
	}
	return h.queries.DeleteOldAccountAuthRequests(ctx)
}
