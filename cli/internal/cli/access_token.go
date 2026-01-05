package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/storage"
)

const (
	// tokenRefreshWindow is how soon before expiry we refresh the token.
	tokenRefreshWindow = 10 * time.Minute
)

type jwtPayload struct {
	Exp float64 `json:"exp"`
}

func jwtExpiresAt(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}

	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}, false
		}
	}

	var payload jwtPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return time.Time{}, false
	}
	if payload.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(payload.Exp), 0), true
}

func isTokenExpiringSoon(token string, window time.Duration) bool {
	exp, ok := jwtExpiresAt(token)
	if !ok {
		// If exp isn't present, don't attempt proactive refresh.
		return false
	}
	return time.Until(exp) <= window
}

// EnsureAccessToken loads the current access token and refreshes it
// automatically when it is expired or near expiry.
//
// Refresh uses Option A: re-auth via /v1/auth/challenge and /v1/auth using the
// deterministic Ed25519 identity derived from the account master secret.
func EnsureAccessToken(cfg *config.Config) (string, error) {
	tokenData, err := os.ReadFile(cfg.AccessKey)
	if err != nil {
		return "", fmt.Errorf("missing %s; run `delight auth` first", cfg.AccessKey)
	}
	token := strings.TrimSpace(string(tokenData))
	if token == "" {
		return "", fmt.Errorf("empty %s; run `delight auth` first", cfg.AccessKey)
	}

	if !isTokenExpiringSoon(token, tokenRefreshWindow) {
		return token, nil
	}

	masterKeyPath := filepath.Join(cfg.DelightHome, "master.key")
	masterSecret, err := storage.LoadSecretKey(masterKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to load %s: %w", masterKeyPath, err)
	}

	newToken, err := authTokenFromMasterSecret(cfg, masterSecret)
	if err != nil {
		return "", err
	}
	newToken = strings.TrimSpace(newToken)
	if newToken == "" {
		return "", fmt.Errorf("auth returned empty token")
	}

	if err := os.WriteFile(cfg.AccessKey, []byte(newToken), 0o600); err != nil {
		return "", fmt.Errorf("failed to write access token: %w", err)
	}

	return newToken, nil
}
