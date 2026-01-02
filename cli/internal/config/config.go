package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// Load loads configuration from environment and defaults
func Load() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Check for dev mode
	delightHome := os.Getenv("DELIGHT_HOME_DIR")
	if delightHome == "" {
		delightHome = filepath.Join(homeDir, ".delight")
	}

	// Ensure delight home exists
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		return nil, fmt.Errorf("failed to create delight home: %w", err)
	}

	serverURL := os.Getenv("DELIGHT_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:3005"
	}

	acpURL := os.Getenv("DELIGHT_ACP_URL")
	acpAgent := os.Getenv("DELIGHT_ACP_AGENT")
	acpEnable := acpURL != "" && acpAgent != ""

	fakeAgent := os.Getenv("DELIGHT_FAKE_AGENT") == "true" ||
		os.Getenv("DELIGHT_FAKE_AGENT") == "1"
	agent := os.Getenv("DELIGHT_AGENT")
	if agent == "" {
		if acpEnable {
			agent = "acp"
		} else {
			agent = "claude"
		}
	}
	if agent != "acp" && agent != "claude" && agent != "codex" {
		return nil, fmt.Errorf("invalid DELIGHT_AGENT %q (expected acp, claude, or codex)", agent)
	}

	startingMode := os.Getenv("DELIGHT_STARTING_MODE")
	if startingMode == "" {
		startingMode = "local"
	}
	if startingMode != "local" && startingMode != "remote" {
		return nil, fmt.Errorf("invalid DELIGHT_STARTING_MODE %q (expected local or remote)", startingMode)
	}

	return &Config{
		ServerURL:         serverURL,
		ACPURL:            acpURL,
		ACPAgent:          acpAgent,
		ACPEnable:         acpEnable,
		DelightHome:       delightHome,
		AccessKey:         filepath.Join(delightHome, "access.key"),
		Debug:             false,
		SocketIOTransport: "websocket",
		Agent:             agent,
		FakeAgent:         fakeAgent,
		StartingMode:      startingMode,
	}, nil
}

// Save saves configuration to disk (currently just creates directories)
func (c *Config) Save() error {
	return os.MkdirAll(c.DelightHome, 0700)
}
