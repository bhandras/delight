package types

import (
	"fmt"
	"time"
)

// CUID generates a unique identifier (simplified version)
// In production, use github.com/lucsky/cuid or similar
func NewCUID() string {
	// For now, use a simple timestamp-based ID
	// TODO: Replace with proper CUID implementation
	return fmt.Sprintf("c%d", time.Now().UnixNano())
}

// Common response types

type ErrorResponse struct {
	Error string `json:"error"`
}

type SuccessResponse struct {
	Success bool `json:"success"`
}

// WebSocket event types

type UpdateEvent struct {
	ID        string      `json:"id"`
	Seq       int64       `json:"seq"`
	Body      interface{} `json:"body"`
	CreatedAt int64       `json:"createdAt"`
}

type EphemeralEvent struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Active   bool   `json:"active"`
	ActiveAt int64  `json:"activeAt"`
	Thinking *bool  `json:"thinking,omitempty"`
}

// Connection types for WebSocket
type ConnectionType string

const (
	ConnectionTypeUserScoped     ConnectionType = "user-scoped"
	ConnectionTypeSessionScoped  ConnectionType = "session-scoped"
	ConnectionTypeTerminalScoped ConnectionType = "terminal-scoped"
)

// Auth types

type AuthRequest struct {
	PublicKey   string `json:"publicKey" binding:"required"`
	ChallengeID string `json:"challengeId" binding:"required"`
	Signature   string `json:"signature" binding:"required"`
}

type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
}

type AuthChallengeRequest struct {
	PublicKey string `json:"publicKey" binding:"required"`
}

type AuthChallengeResponse struct {
	ChallengeID string `json:"challengeId"`
	Challenge   string `json:"challenge"`
}

type TerminalAuthRequestBody struct {
	PublicKey string `json:"publicKey" binding:"required"`
}

type TerminalAuthStatusResponse struct {
	Status   string  `json:"status"` // "pending" or "authorized"
	Response *string `json:"response,omitempty"`
}

type TerminalAuthResponseBody struct {
	PublicKey string `json:"publicKey" binding:"required"`
	Response  string `json:"response" binding:"required"`
}
