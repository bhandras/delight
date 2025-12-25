package crypto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenClaims represents the JWT token payload
type TokenClaims struct {
	UserID string                 `json:"user"`
	Extras map[string]interface{} `json:"extras,omitempty"`
	jwt.RegisteredClaims
}

// JWTManager handles JWT token creation and verification
type JWTManager struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

// NewJWTManager creates a new JWT manager from master secret
func NewJWTManager(masterSecret string) (*JWTManager, error) {
	// Derive Ed25519 key from master secret
	seed := sha256.Sum256([]byte(masterSecret))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)

	return &JWTManager{
		privateKey: privateKey,
		publicKey:  publicKey,
	}, nil
}

// CreateToken creates a new JWT token for a user
func (m *JWTManager) CreateToken(userID string, extras map[string]interface{}) (string, error) {
	claims := TokenClaims{
		UserID: userID,
		Extras: extras,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "happy-server",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(m.privateKey)
}

// VerifyToken verifies and parses a JWT token
func (m *JWTManager) VerifyToken(tokenString string) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.publicKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*TokenClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}
