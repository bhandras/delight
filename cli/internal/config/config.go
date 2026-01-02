package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// ReasoningEffort selects the reasoning effort preset (engine-specific).
	//
	// For Codex, this maps to model_reasoning_effort: low|medium|high|xhigh.
	// An empty string means "use engine default".
	ReasoningEffort string
	// PermissionMode selects the approval/sandbox preset.
	//
	// Canonical Delight values are: default|read-only|safe-yolo|yolo.
	// An empty string means "default".
	PermissionMode string

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
}

const (
	// defaultServerURL is the default API base used by the CLI when no explicit
	// value is provided.
	defaultServerURL = "http://localhost:3005"

	// defaultSocketIOTransport is the default Socket.IO transport used for the
	// user and machine websocket clients.
	defaultSocketIOTransport = "websocket"

	// defaultAgent is the default agent backend used by `delight run` when not
	// overridden explicitly.
	defaultAgent = "codex"

	// defaultStartingMode is the default control mode when starting a session.
	//
	// Remote is the primary mode for the iOS harness; local is still supported
	// via an explicit flag.
	defaultStartingMode = "remote"
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

	return &Config{
		ServerURL:         defaultServerURL,
		ACPURL:            "",
		ACPAgent:          "",
		ACPEnable:         false,
		DelightHome:       delightHome,
		AccessKey:         filepath.Join(delightHome, "access.key"),
		Model:             "",
		ReasoningEffort:   "",
		PermissionMode:    "",
		Debug:             false,
		SocketIOTransport: defaultSocketIOTransport,
		Agent:             defaultAgent,
		FakeAgent:         false,
		ForceNewSession:   false,
		StartingMode:      defaultStartingMode,
	}, nil
}

// EnsureHome creates the on-disk Delight home directory if needed.
func (c *Config) EnsureHome() error {
	if strings.TrimSpace(c.DelightHome) == "" {
		return fmt.Errorf("delight home is empty")
	}
	return os.MkdirAll(c.DelightHome, 0o700)
}
