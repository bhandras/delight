package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	// PermissionMode selects the upstream permission/sandbox preset.
	PermissionMode string
	// ReasoningEffort selects the Codex reasoning effort preset.
	ReasoningEffort string

	// ResumeToken is an engine-specific resume identifier used to resume an
	// existing upstream conversation when starting a session.
	//
	// This is intended to be populated by explicit `delight <agent> resume <id>`
	// commands and is not exposed as a CLI flag.
	ResumeToken string

	// Debug enables verbose logging.
	Debug bool
	// LogLevel controls the shared logger verbosity.
	LogLevel string
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

	// PushMode controls whether encrypted mobile push notifications are enabled.
	//
	// Supported values: "auto", "on", "off".
	PushMode string
	// PushCooldown is the minimum interval between push notifications per alert key.
	PushCooldown time.Duration
	// PushNotifyTurnComplete enables push notifications when a turn finishes.
	PushNotifyTurnComplete bool
	// PushNotifyAttention enables push notifications when attention is required.
	PushNotifyAttention bool

	// CodexExtraArgs are appended to local Codex invocations after Delight's
	// first-class arguments.
	CodexExtraArgs []string
	// ClaudeExtraArgs are appended to local Claude invocations after Delight's
	// first-class arguments.
	ClaudeExtraArgs []string

	// CodexModel stores the Codex-specific model loaded from TOML.
	CodexModel string
	// CodexPermissionMode stores the Codex-specific permission mode from TOML.
	CodexPermissionMode string
	// CodexReasoningEffort stores the Codex-specific reasoning effort from TOML.
	CodexReasoningEffort string
	// ClaudeModel stores the Claude-specific model loaded from TOML.
	ClaudeModel string
	// ClaudePermissionMode stores the Claude-specific permission mode from TOML.
	ClaudePermissionMode string
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

	// defaultLogLevel is the shared logger level used unless overridden.
	defaultLogLevel = "info"

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

	// pushModeAuto enables pushes when not explicitly disabled.
	pushModeAuto = "auto"
	// pushModeOn forces pushes on.
	pushModeOn = "on"
	// pushModeOff disables pushes.
	pushModeOff = "off"

	// defaultPushCooldown is the fallback cooldown between push alerts.
	defaultPushCooldown = 60 * time.Second
)

// Default returns the default CLI configuration without reading configuration
// files or environment variables.
//
// Callers that need the on-disk home directory to exist must call EnsureHome.
func Default() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	delightHome := filepath.Join(homeDir, ".delight")

	cfg := &Config{
		ServerURL:              defaultServerURL,
		ACPURL:                 "",
		ACPAgent:               "",
		ACPEnable:              false,
		DelightHome:            delightHome,
		AccessKey:              filepath.Join(delightHome, "access.key"),
		Model:                  "",
		PermissionMode:         "",
		ReasoningEffort:        "",
		ResumeToken:            "",
		Debug:                  false,
		LogLevel:               defaultLogLevel,
		SocketIOTransport:      defaultSocketIOTransport,
		Agent:                  defaultAgent,
		FakeAgent:              false,
		ForceNewSession:        false,
		StartingMode:           defaultStartingMode,
		PushoverMode:           pushoverModeAuto,
		PushoverPriority:       defaultPushoverPriority,
		PushoverCooldown:       defaultPushoverCooldown,
		PushMode:               pushModeAuto,
		PushCooldown:           defaultPushCooldown,
		PushNotifyTurnComplete: true,
		PushNotifyAttention:    true,
	}
	return cfg, nil
}

// EnsureHome creates the on-disk Delight home directory if needed.
func (c *Config) EnsureHome() error {
	if strings.TrimSpace(c.DelightHome) == "" {
		return fmt.Errorf("delight home is empty")
	}
	return os.MkdirAll(c.DelightHome, 0o700)
}

// SetHomeDir updates DelightHome and paths derived from it.
func (c *Config) SetHomeDir(homeDir string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	expanded, err := expandPath(homeDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(expanded) == "" {
		return fmt.Errorf("delight home is empty")
	}
	c.DelightHome = expanded
	c.AccessKey = filepath.Join(c.DelightHome, "access.key")
	return nil
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

// PushEnabled reports whether encrypted mobile push notifications are active.
func (c *Config) PushEnabled() bool {
	mode := strings.TrimSpace(c.PushMode)
	switch mode {
	case pushModeOff:
		return false
	case pushModeOn:
		return c.PushNotifyTurnComplete || c.PushNotifyAttention
	case "", pushModeAuto:
		return c.PushNotifyTurnComplete || c.PushNotifyAttention
	default:
		return false
	}
}

// ExtraArgsForAgent returns a copy of configured passthrough args for agent.
func (c *Config) ExtraArgsForAgent(agent string) []string {
	if c == nil {
		return nil
	}
	switch strings.TrimSpace(agent) {
	case "codex":
		return append([]string(nil), c.CodexExtraArgs...)
	case "claude":
		return append([]string(nil), c.ClaudeExtraArgs...)
	default:
		return nil
	}
}

// AppendExtraArgsForAgent appends passthrough args to the selected agent.
func (c *Config) AppendExtraArgsForAgent(agent string, args []string) {
	if c == nil || len(args) == 0 {
		return
	}
	switch strings.TrimSpace(agent) {
	case "codex":
		c.CodexExtraArgs = append(c.CodexExtraArgs, args...)
	case "claude":
		c.ClaudeExtraArgs = append(c.ClaudeExtraArgs, args...)
	}
}

// ApplyAgentDefaults overlays TOML agent-section defaults for the active agent.
func (c *Config) ApplyAgentDefaults() {
	if c == nil {
		return
	}
	switch strings.TrimSpace(c.Agent) {
	case "codex":
		if c.CodexModel != "" {
			c.Model = c.CodexModel
		}
		if c.CodexPermissionMode != "" {
			c.PermissionMode = c.CodexPermissionMode
		}
		if c.CodexReasoningEffort != "" {
			c.ReasoningEffort = c.CodexReasoningEffort
		}
	case "claude":
		if c.ClaudeModel != "" {
			c.Model = c.ClaudeModel
		}
		if c.ClaudePermissionMode != "" {
			c.PermissionMode = c.ClaudePermissionMode
		}
	}
}

// SetPushEvents enables encrypted push alerts from a comma-separated list.
func (c *Config) SetPushEvents(events string) {
	if c == nil {
		return
	}
	applyPushEvents(c, events)
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

// applyPushEvents enables push alerts based on a comma-separated list.
func applyPushEvents(cfg *Config, events string) {
	cfg.PushNotifyTurnComplete = false
	cfg.PushNotifyAttention = false

	for _, raw := range strings.Split(events, ",") {
		switch strings.TrimSpace(raw) {
		case "turn-complete":
			cfg.PushNotifyTurnComplete = true
		case "attention":
			cfg.PushNotifyAttention = true
		}
	}
}
