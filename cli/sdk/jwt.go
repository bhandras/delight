package sdk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type jwtPayload struct {
	Exp float64 `json:"exp"`
}

// jwtExpiresAt returns the expiry timestamp encoded in a JWT (if present).
//
// This function does not verify the JWT signature. It is only used for client
// UX/control flow such as proactive refresh. Server-side verification remains
// the source of truth.
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

// isTokenExpiringSoon reports whether a token is already expired or will expire
// within the given window.
func isTokenExpiringSoon(token string, window time.Duration) (bool, error) {
	if strings.TrimSpace(token) == "" {
		return true, fmt.Errorf("token is empty")
	}
	exp, ok := jwtExpiresAt(token)
	if !ok {
		// If we can't parse exp, treat it as non-refreshable but not expired.
		// The server will be authoritative and will 401 if needed.
		return false, nil
	}
	return time.Until(exp) <= window, nil
}
