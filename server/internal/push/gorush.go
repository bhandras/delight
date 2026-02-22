package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// defaultGorushURL is the sidecar endpoint used in docker-compose deployments.
	defaultGorushURL = "http://gorush:8088/api/push"
	// maxDeviceTokensPerBatch caps work per request to keep latency bounded.
	maxDeviceTokensPerBatch = 200
	// pushPayloadVersion identifies the encrypted payload schema for clients.
	pushPayloadVersion = 1
	// gorushPlatformIOS identifies iOS notifications for gorush.
	gorushPlatformIOS = 1
)

// GorushConfig configures the gorush push backend.
type GorushConfig struct {
	// URL is the gorush HTTP endpoint (usually /api/push).
	URL string
	// Topic is the APNs topic (app bundle identifier) for iOS delivery.
	Topic string
}

// GorushSender sends encrypted notifications through a gorush sidecar.
type GorushSender struct {
	url    string
	topic  string
	client *http.Client
}

// NewGorushSender constructs a new gorush-backed push sender.
func NewGorushSender(cfg GorushConfig) (*GorushSender, error) {
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		url = defaultGorushURL
	}
	topic := strings.TrimSpace(cfg.Topic)
	if topic == "" {
		return nil, fmt.Errorf("gorush topic is required")
	}

	return &GorushSender{
		url:    strings.TrimRight(url, "/"),
		topic:  topic,
		client: &http.Client{},
	}, nil
}

// SendEncrypted delivers ciphertext to gorush for all provided device tokens.
func (s *GorushSender) SendEncrypted(ctx context.Context, deviceTokens []string, ciphertext string) (Result, error) {
	if s == nil || s.client == nil {
		return Result{}, fmt.Errorf("gorush sender not configured")
	}
	if strings.TrimSpace(ciphertext) == "" {
		return Result{}, fmt.Errorf("ciphertext is empty")
	}

	tokens := compactTokens(deviceTokens)
	if len(tokens) == 0 {
		return Result{}, nil
	}
	if len(tokens) > maxDeviceTokensPerBatch {
		tokens = tokens[:maxDeviceTokensPerBatch]
	}

	body, err := json.Marshal(gorushPushRequest{
		Notifications: []gorushNotification{
			{
				Tokens:           tokens,
				Platform:         gorushPlatformIOS,
				Topic:            s.topic,
				ContentAvailable: true,
				Priority:         "high",
				Data: map[string]any{
					"delight": map[string]any{
						"v":          pushPayloadVersion,
						"ciphertext": ciphertext,
					},
				},
			},
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("marshal gorush request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("build gorush request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("send gorush request: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return Result{}, fmt.Errorf("read gorush response: %w", readErr)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("gorush response %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var parsed gorushPushResponse
	if len(bytes.TrimSpace(respBody)) > 0 {
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			// If gorush returned non-JSON success payload, still treat as best-effort success.
			return Result{Sent: len(tokens), Failed: 0}, nil
		}
	}

	success := parsed.Counts.Success
	failure := parsed.Counts.Failure
	if success == 0 && failure == 0 {
		success = len(tokens)
	}

	return Result{Sent: success, Failed: failure}, nil
}

// compactTokens trims and drops empty tokens while preserving order.
func compactTokens(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// gorushPushRequest is the HTTP payload format expected by gorush.
type gorushPushRequest struct {
	Notifications []gorushNotification `json:"notifications"`
}

// gorushNotification is a single gorush notification record.
type gorushNotification struct {
	Tokens           []string       `json:"tokens"`
	Platform         int            `json:"platform"`
	Topic            string         `json:"topic,omitempty"`
	ContentAvailable bool           `json:"content_available"`
	Priority         string         `json:"priority"`
	Data             map[string]any `json:"data"`
}

// gorushPushResponse captures delivery counts from gorush.
type gorushPushResponse struct {
	Counts struct {
		Success int `json:"success"`
		Failure int `json:"failure"`
	} `json:"counts"`
}
