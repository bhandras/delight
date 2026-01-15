package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// pushoverEndpoint is the Pushover API endpoint used for message delivery.
	pushoverEndpoint = "https://api.pushover.net/1/messages.json"
	// pushoverContentType is the HTTP form content type required by Pushover.
	pushoverContentType = "application/x-www-form-urlencoded"
	// defaultPushoverTimeout is the HTTP timeout used for Pushover requests.
	defaultPushoverTimeout = 10 * time.Second
)

// PushoverConfig describes the credentials and defaults for Pushover delivery.
type PushoverConfig struct {
	// Token is the application API token.
	Token string
	// UserKey is the destination user key.
	UserKey string
	// Priority is the Pushover priority value for messages.
	Priority int
	// Cooldown is the minimum interval between notifications per alert key.
	Cooldown time.Duration
}

// PushoverMessage describes a message to send to Pushover.
type PushoverMessage struct {
	// Title is the Pushover notification title.
	Title string
	// Message is the notification body.
	Message string
	// AlertKey is used to de-duplicate notifications within the cooldown window.
	AlertKey string
}

// PushoverNotifier sends notifications to the Pushover service.
type PushoverNotifier struct {
	token    string
	userKey  string
	priority int
	cooldown time.Duration

	client *http.Client

	mu        sync.Mutex
	lastSent  map[string]time.Time
	lastError error
}

// NewPushoverNotifier creates a new notifier using the supplied config.
func NewPushoverNotifier(cfg PushoverConfig) (*PushoverNotifier, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("pushover token is required")
	}
	if strings.TrimSpace(cfg.UserKey) == "" {
		return nil, fmt.Errorf("pushover user key is required")
	}
	if cfg.Cooldown < 0 {
		return nil, fmt.Errorf("pushover cooldown must be non-negative")
	}

	return &PushoverNotifier{
		token:    cfg.Token,
		userKey:  cfg.UserKey,
		priority: cfg.Priority,
		cooldown: cfg.Cooldown,
		client: &http.Client{
			Timeout: defaultPushoverTimeout,
		},
		lastSent: make(map[string]time.Time),
	}, nil
}

// Notify sends a Pushover notification if it passes cooldown checks.
func (n *PushoverNotifier) Notify(ctx context.Context, msg PushoverMessage) error {
	alertKey := strings.TrimSpace(msg.AlertKey)
	if alertKey == "" {
		return fmt.Errorf("pushover alert key is required")
	}
	message := strings.TrimSpace(msg.Message)
	if message == "" {
		return fmt.Errorf("pushover message is required")
	}

	now := time.Now()
	if !n.shouldSend(alertKey, now) {
		return nil
	}

	if err := n.send(ctx, msg); err != nil {
		n.setLastError(err)
		return err
	}
	n.markSent(alertKey, now)
	return nil
}

// LastError returns the most recent send error, if any.
func (n *PushoverNotifier) LastError() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastError
}

// shouldSend returns whether a notification is allowed under cooldown rules.
func (n *PushoverNotifier) shouldSend(alertKey string, now time.Time) bool {
	if n.cooldown == 0 {
		return true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	last, ok := n.lastSent[alertKey]
	if !ok {
		return true
	}
	return now.Sub(last) >= n.cooldown
}

// markSent records a successful send time for a specific alert key.
func (n *PushoverNotifier) markSent(alertKey string, now time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastSent[alertKey] = now
	n.lastError = nil
}

// setLastError records the most recent send error.
func (n *PushoverNotifier) setLastError(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastError = err
}

// send performs the HTTP request to Pushover.
func (n *PushoverNotifier) send(ctx context.Context, msg PushoverMessage) error {
	form := url.Values{}
	form.Set("token", n.token)
	form.Set("user", n.userKey)
	form.Set("message", msg.Message)
	if title := strings.TrimSpace(msg.Title); title != "" {
		form.Set("title", title)
	}
	if n.priority != 0 {
		form.Set("priority", fmt.Sprintf("%d", n.priority))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushoverEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("pushover request build failed: %w", err)
	}
	req.Header.Set("Content-Type", pushoverContentType)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("pushover request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("pushover response read failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("pushover response %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
