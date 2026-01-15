package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// ServerURL is the base URL of the Delight server API.
	ServerURL string
	// ACPURL is the base URL for the ACP HTTP API.
	ACPURL string
	// ACPAgent is the ACP agent name to use for runs.
	ACPAgent string
	// ACPEnable indicates whether ACP is configured and enabled.
	ACPEnable bool

	// DelightHome is the directory where Delight stores local state.
	DelightHome string
	// AccessKey is the path to the access key file.
	AccessKey string

	// Model selects the upstream model identifier for the session.
	//
	// This value is engine-specific; an empty string means "use engine default".
	Model string

	// ResumeToken is an engine-specific resume identifier used to resume an
	// existing upstream conversation when starting a session.
	//
	// This is intended to be populated by explicit `delight <agent> resume <id>`
	// commands and is not exposed as a CLI flag.
	ResumeToken string

	// Debug enables verbose logging.
	Debug bool
	// SocketIOTransport selects the Socket.IO transport mode ("websocket" or "polling").
	SocketIOTransport string
	// Agent selects the local agent backend (acp|claude|codex).
	Agent string
	// FakeAgent enables a stub agent for integration tests.
	FakeAgent bool
	// ForceNewSession forces creating a new session tag on every run.
	ForceNewSession bool

	// StartingMode controls the initial Claude control mode ("local" or "remote").
	// When set to "remote", the CLI starts the remote Claude bridge immediately.
	StartingMode string

	// PushoverMode controls whether Pushover notifications are enabled.
	//
	// Supported values: "auto", "on", "off".
	PushoverMode string
	// PushoverToken is the API token for Pushover notifications.
	PushoverToken string
	// PushoverUserKey is the user key for Pushover notifications.
	PushoverUserKey string
	// PushoverPriority is the Pushover priority value to use in notifications.
	PushoverPriority int
	// PushoverCooldown is the minimum interval between notifications per alert key.
	PushoverCooldown time.Duration
	// PushoverNotifyTurnComplete enables notifications when a turn finishes.
	PushoverNotifyTurnComplete bool
	// PushoverNotifyAttention enables notifications when attention is required.
	PushoverNotifyAttention bool
}

const (
	// defaultServerURL is the default API base used by the CLI when no explicit
	// value is provided.
	defaultServerURL = "http://localhost:3005"

	// defaultSocketIOTransport is the default Socket.IO transport used for the
	// user and terminal websocket clients.
	defaultSocketIOTransport = "websocket"

	// defaultAgent is the default agent backend used by `delight run` when not
	// overridden explicitly.
	defaultAgent = "codex"

	// defaultStartingMode is the default control mode when starting a session.
	//
	// Remote is the primary mode for the iOS harness; local is still supported
	// via an explicit flag.
	defaultStartingMode = "remote"

	// pushoverModeAuto enables notifications only when credentials are present.
	pushoverModeAuto = "auto"
	// pushoverModeOn forces notifications on (requires credentials).
	pushoverModeOn = "on"
	// pushoverModeOff disables notifications even if credentials are present.
	pushoverModeOff = "off"

	// defaultPushoverCooldown is the fallback cooldown between Pushover alerts.
	defaultPushoverCooldown = 60 * time.Second

	// defaultPushoverPriority is the default priority for Pushover notifications.
	defaultPushoverPriority = 0
)

// Default returns the default CLI configuration without reading environment
// variables.
//
// Callers that need the on-disk home directory to exist must call EnsureHome.
func Default() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	delightHome := filepath.Join(homeDir, ".delight")

	cfg := &Config{
		ServerURL:         defaultServerURL,
		ACPURL:            "",
		ACPAgent:          "",
		ACPEnable:         false,
		DelightHome:       delightHome,
		AccessKey:         filepath.Join(delightHome, "access.key"),
		Model:             "",
		ResumeToken:       "",
		Debug:             false,
		SocketIOTransport: defaultSocketIOTransport,
		Agent:             defaultAgent,
		FakeAgent:         false,
		ForceNewSession:   false,
		StartingMode:      defaultStartingMode,
		PushoverMode:      pushoverModeAuto,
		PushoverPriority:  defaultPushoverPriority,
		PushoverCooldown:  defaultPushoverCooldown,
	}
	applyPushoverEnv(cfg)
	return cfg, nil
}

// EnsureHome creates the on-disk Delight home directory if needed.
func (c *Config) EnsureHome() error {
	if strings.TrimSpace(c.DelightHome) == "" {
		return fmt.Errorf("delight home is empty")
	}
	return os.MkdirAll(c.DelightHome, 0o700)
}

// PushoverEnabled reports whether Pushover notifications are configured.
func (c *Config) PushoverEnabled() bool {
	mode := strings.TrimSpace(c.PushoverMode)
	switch mode {
	case pushoverModeOff:
		return false
	case pushoverModeOn:
		return strings.TrimSpace(c.PushoverToken) != "" &&
			strings.TrimSpace(c.PushoverUserKey) != "" &&
			(c.PushoverNotifyTurnComplete || c.PushoverNotifyAttention)
	case "", pushoverModeAuto:
		return strings.TrimSpace(c.PushoverToken) != "" &&
			strings.TrimSpace(c.PushoverUserKey) != "" &&
			(c.PushoverNotifyTurnComplete || c.PushoverNotifyAttention)
	default:
		return false
	}
}

// applyPushoverEnv applies Pushover settings from environment variables.
func applyPushoverEnv(cfg *Config) {
	token := strings.TrimSpace(os.Getenv("DELIGHT_PUSHOVER_TOKEN"))
	userKey := strings.TrimSpace(os.Getenv("DELIGHT_PUSHOVER_USER_KEY"))
	if token == "" || userKey == "" {
		return
	}

	cfg.PushoverToken = token
	cfg.PushoverUserKey = userKey

	if priority := strings.TrimSpace(os.Getenv("DELIGHT_PUSHOVER_PRIORITY")); priority != "" {
		if parsed, err := strconv.Atoi(priority); err == nil {
			cfg.PushoverPriority = parsed
		}
	}

	if cooldown := strings.TrimSpace(os.Getenv("DELIGHT_PUSHOVER_COOLDOWN_SEC")); cooldown != "" {
		if parsed, err := strconv.Atoi(cooldown); err == nil && parsed > 0 {
			cfg.PushoverCooldown = time.Duration(parsed) * time.Second
		}
	}

	if events := strings.TrimSpace(os.Getenv("DELIGHT_PUSHOVER_EVENTS")); events != "" {
		applyPushoverEvents(cfg, events)
		return
	}

	cfg.PushoverNotifyTurnComplete = true
	cfg.PushoverNotifyAttention = true
}

// applyPushoverEvents enables Pushover alerts based on a comma-separated list.
func applyPushoverEvents(cfg *Config, events string) {
	cfg.PushoverNotifyTurnComplete = false
	cfg.PushoverNotifyAttention = false

	for _, raw := range strings.Split(events, ",") {
		switch strings.TrimSpace(raw) {
		case "turn-complete":
			cfg.PushoverNotifyTurnComplete = true
		case "attention":
			cfg.PushoverNotifyAttention = true
		}
	}
}
