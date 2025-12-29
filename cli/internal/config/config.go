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
	// Agent selects the local agent backend (acp|claude|codex).
	Agent string
	// FakeAgent enables a stub agent for integration tests.
	FakeAgent bool
	// ForceNewSession forces creating a new session tag on every run.
	ForceNewSession bool
}

// Load loads configuration from environment and defaults
func Load() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Check for dev mode
	delightHome := getenvFirst("DELIGHT_HOME_DIR", "HAPPY_HOME_DIR")
	if delightHome == "" {
		delightHome = filepath.Join(homeDir, ".delight")
	}

	// Ensure delight home exists
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		return nil, fmt.Errorf("failed to create delight home: %w", err)
	}

	serverURL := getenvFirst("DELIGHT_SERVER_URL", "HAPPY_SERVER_URL")
	if serverURL == "" {
		serverURL = "https://happy-api.slopus.com" // Default to official server
	}

	acpURL := getenvFirst("DELIGHT_ACP_URL", "HAPPY_ACP_URL")
	acpAgent := getenvFirst("DELIGHT_ACP_AGENT", "HAPPY_ACP_AGENT")
	acpEnable := acpURL != "" && acpAgent != ""

	debug := os.Getenv("DEBUG") == "true" || os.Getenv("DEBUG") == "1"
	if !debug {
		debug = getenvFirst("DELIGHT_DEBUG", "HAPPY_DEBUG") == "true" ||
			getenvFirst("DELIGHT_DEBUG", "HAPPY_DEBUG") == "1"
	}
	fakeAgent := getenvFirst("DELIGHT_FAKE_AGENT", "HAPPY_FAKE_AGENT") == "true" ||
		getenvFirst("DELIGHT_FAKE_AGENT", "HAPPY_FAKE_AGENT") == "1"
	agent := getenvFirst("DELIGHT_AGENT", "HAPPY_AGENT")
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

	return &Config{
		ServerURL:   serverURL,
		ACPURL:      acpURL,
		ACPAgent:    acpAgent,
		ACPEnable:   acpEnable,
		DelightHome: delightHome,
		AccessKey:   filepath.Join(delightHome, "access.key"),
		Debug:       debug,
		Agent:       agent,
		FakeAgent:   fakeAgent,
	}, nil
}

// Save saves configuration to disk (currently just creates directories)
func (c *Config) Save() error {
	return os.MkdirAll(c.DelightHome, 0700)
}

func getenvFirst(primary, fallback string) string {
	if val := os.Getenv(primary); val != "" {
		return val
	}
	return os.Getenv(fallback)
}
