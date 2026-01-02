package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/bhandras/delight/protocol/logger"
	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/gin-gonic/gin"
)

// AuthMiddleware creates a middleware that validates JWT tokens
func AuthMiddleware(jwtManager *crypto.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get Authorization header
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		// Extract token. Clients are inconsistent about auth schemes:
		// - "Bearer <token>" is common.
		// - Some clients use "Token <token>" or "JWT <token>".
		// - Some legacy integrations send a raw token without a prefix.
		token := authHeader
		if parts := strings.SplitN(authHeader, " ", 2); len(parts) == 2 {
			switch {
			case strings.EqualFold(parts[0], "Bearer"),
				strings.EqualFold(parts[0], "Token"),
				strings.EqualFold(parts[0], "JWT"):
				token = strings.TrimSpace(parts[1])
			}
		}
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			c.Abort()
			return
		}

		// Verify token
		claims, err := jwtManager.VerifyToken(token)
		if err != nil {
			if os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") == "1" {
				logger.Debugf("AuthMiddleware: invalid token: %v", err)
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			c.Abort()
			return
		}

		// Store user ID in context
		c.Set("userID", claims.Subject)
		c.Set("claims", claims)

		c.Next()
	}
}

// GetUserID extracts the user ID from the Gin context
func GetUserID(c *gin.Context) (string, bool) {
	userID, exists := c.Get("userID")
	if !exists {
		return "", false
	}
	return userID.(string), true
}
