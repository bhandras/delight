package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
)

const (
	// pushKeyUsage matches the Swift implementation for push payload encryption.
	pushKeyUsage = "Delight Push"
	// pushKeyPath is the path used for deriving the push payload key.
	pushKeyPath = "notifications"
	// pushPayloadVersion identifies the encrypted push payload schema.
	pushPayloadVersion = 1
	// pushRequestTimeout bounds the total time spent sending a push request.
	pushRequestTimeout = 5 * time.Second
	// pushResponseBodyLimit keeps response parsing bounded.
	pushResponseBodyLimit = 8 * 1024
)

// PushConfig configures the encrypted push notifier.
type PushConfig struct {
	// ServerURL is the base URL of the Delight server.
	ServerURL string
	// TokenProvider returns the current auth token.
	TokenProvider func() string
	// MasterSecret holds the local master key bytes.
	MasterSecret []byte
	// Cooldown is the minimum interval between notifications per alert key.
	Cooldown time.Duration
}

// PushMessage describes the encrypted payload to deliver to a device.
type PushMessage struct {
	AlertKey   string
	Event      string
	Agent      string
	Host       string
	Path       string
	Label      string
	SessionID  string
	SessionTag string
	TerminalID string
	ToolName   string
	Timestamp  int64
}

// PushNotifier sends encrypted push notifications through the server.
type PushNotifier struct {
	serverURL     string
	tokenProvider func() string
	client        *http.Client
	cooldown      time.Duration
	key           []byte

	mu       sync.Mutex
	lastSent map[string]time.Time
}

// NewPushNotifier creates a new PushNotifier.
func NewPushNotifier(cfg PushConfig) (*PushNotifier, error) {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	if cfg.TokenProvider == nil {
		return nil, fmt.Errorf("token provider is required")
	}
	if len(cfg.MasterSecret) == 0 {
		return nil, fmt.Errorf("master secret is required")
	}
	if cfg.Cooldown < 0 {
		return nil, fmt.Errorf("cooldown must be non-negative")
	}

	key, err := crypto.DeriveKey(cfg.MasterSecret, pushKeyUsage, []string{pushKeyPath})
	if err != nil {
		return nil, fmt.Errorf("derive push key: %w", err)
	}

	return &PushNotifier{
		serverURL:     strings.TrimRight(cfg.ServerURL, "/"),
		tokenProvider: cfg.TokenProvider,
		client:        &http.Client{Timeout: pushRequestTimeout},
		cooldown:      cfg.Cooldown,
		key:           key,
		lastSent:      make(map[string]time.Time),
	}, nil
}

// Notify sends an encrypted push notification if allowed by cooldown rules.
func (n *PushNotifier) Notify(ctx context.Context, msg PushMessage) error {
	if n == nil {
		return fmt.Errorf("push notifier is nil")
	}
	if strings.TrimSpace(msg.AlertKey) == "" {
		return fmt.Errorf("alert key is required")
	}
	if strings.TrimSpace(msg.Event) == "" {
		return fmt.Errorf("event is required")
	}
	if !n.shouldSend(msg.AlertKey) {
		return nil
	}

	payload := pushPayload{
		Version:    pushPayloadVersion,
		Event:      msg.Event,
		Agent:      msg.Agent,
		Host:       msg.Host,
		Path:       msg.Path,
		Label:      msg.Label,
		SessionID:  msg.SessionID,
		SessionTag: msg.SessionTag,
		TerminalID: msg.TerminalID,
		ToolName:   msg.ToolName,
		Timestamp:  msg.Timestamp,
	}

	ciphertext, err := n.encryptPayload(payload)
	if err != nil {
		return err
	}

	body, err := json.Marshal(pushRequest{Ciphertext: ciphertext})
	if err != nil {
		return fmt.Errorf("marshal push request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pushRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.serverURL+"/v1/push-notifications", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}

	token := strings.TrimSpace(n.tokenProvider())
	if token == "" {
		return fmt.Errorf("auth token is missing")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send push request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, pushResponseBodyLimit))
	if err != nil {
		return fmt.Errorf("read push response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("push request failed: %s", formatPushError(resp.Status, respBody))
	}

	var result pushResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode push response: %w", err)
	}
	if !result.Success || result.Sent <= 0 || result.Failed > 0 {
		return fmt.Errorf(
			"push delivery failed: success=%t sent=%d failed=%d",
			result.Success,
			result.Sent,
			result.Failed,
		)
	}

	n.markSent(msg.AlertKey)
	return nil
}

// pushPayload is the encrypted payload delivered to the iOS app.
type pushPayload struct {
	Version    int    `json:"version"`
	Event      string `json:"event"`
	Agent      string `json:"agent"`
	Host       string `json:"host"`
	Path       string `json:"path"`
	Label      string `json:"label"`
	SessionID  string `json:"sessionId,omitempty"`
	SessionTag string `json:"sessionTag,omitempty"`
	TerminalID string `json:"terminalId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

// pushRequest is the API request body for sending an encrypted push payload.
type pushRequest struct {
	Ciphertext string `json:"ciphertext"`
}

// pushResponse captures the server's push delivery summary.
type pushResponse struct {
	Success bool `json:"success"`
	Sent    int  `json:"sent"`
	Failed  int  `json:"failed"`
}

// pushErrorResponse captures JSON API errors returned by the server.
type pushErrorResponse struct {
	Error string `json:"error"`
}

// encryptPayload encrypts the push payload and returns base64 ciphertext.
func (n *PushNotifier) encryptPayload(payload pushPayload) (string, error) {
	cipher, err := crypto.EncryptWithDataKey(payload, n.key)
	if err != nil {
		return "", fmt.Errorf("encrypt push payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(cipher), nil
}

// shouldSend returns whether a notification is allowed under cooldown rules.
func (n *PushNotifier) shouldSend(alertKey string) bool {
	if n.cooldown <= 0 {
		return true
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	last, ok := n.lastSent[alertKey]
	if !ok {
		return true
	}
	return time.Since(last) >= n.cooldown
}

// markSent records that a notification was sent for the alert key.
func (n *PushNotifier) markSent(alertKey string) {
	if n.cooldown <= 0 {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	n.lastSent[alertKey] = time.Now()
}

// formatPushError extracts a useful error string from a non-2xx push response.
func formatPushError(status string, body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return status
	}

	var parsed pushErrorResponse
	if err := json.Unmarshal(body, &parsed); err == nil && strings.TrimSpace(parsed.Error) != "" {
		return fmt.Sprintf("%s (%s)", status, strings.TrimSpace(parsed.Error))
	}

	return fmt.Sprintf("%s (%s)", status, trimmed)
}
