package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds server configuration.
type Config struct {
	// Addr is the listen address for the HTTP(S) server.
	Addr           string
	DatabasePath   string
	MasterSecret   string
	Debug          bool
	AllowedOrigins []string
	// TLS holds HTTPS configuration. If nil, the server runs in plain HTTP mode.
	TLS *TLSConfig
	// Push holds push-delivery backend configuration.
	Push *PushConfig
}

// TLSConfig holds file paths for serving HTTPS directly from the server.
type TLSConfig struct {
	// CertFile is a PEM-encoded certificate chain.
	CertFile string
	// KeyFile is a PEM-encoded private key.
	KeyFile string
}

// PushConfig holds push backend configuration.
type PushConfig struct {
	// Backend selects which push sender to use (currently "gorush").
	Backend string
	// GorushURL is the internal gorush endpoint (usually /api/push).
	GorushURL string
	// Topic is the APNs topic (app bundle identifier) for iOS push delivery.
	Topic string
}

// Overrides optionally overrides values from environment variables.
//
// A nil pointer means "use the environment/default value".
type Overrides struct {
	Addr         *string
	DatabasePath *string
	MasterSecret *string
	Debug        *bool
	TLS          *TLSConfig
	Push         *PushConfig
}

// Load loads server configuration from environment variables and applies any
// explicit overrides.
func Load(overrides Overrides) (*Config, error) {
	port := 3005
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	addr := fmt.Sprintf(":%d", port)
	if overrides.Addr != nil {
		addr = *overrides.Addr
	}

	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./delight.db"
	}
	if overrides.DatabasePath != nil {
		dbPath = *overrides.DatabasePath
	}

	masterSecret := os.Getenv("DELIGHT_MASTER_SECRET")
	if overrides.MasterSecret != nil {
		masterSecret = *overrides.MasterSecret
	}
	if masterSecret == "" {
		return nil, fmt.Errorf("DELIGHT_MASTER_SECRET environment variable is required")
	}

	debug := false
	if debugStr := os.Getenv("DEBUG"); debugStr == "true" || debugStr == "1" {
		debug = true
	}
	if overrides.Debug != nil {
		debug = *overrides.Debug
	}

	pushConfig, err := loadPushConfig()
	if err != nil {
		return nil, err
	}
	if overrides.Push != nil {
		pushConfig = overrides.Push
	}

	return &Config{
		Addr:           addr,
		DatabasePath:   dbPath,
		MasterSecret:   masterSecret,
		Debug:          debug,
		AllowedOrigins: []string{"*"}, // For self-hosted, allow all origins
		TLS:            overrides.TLS,
		Push:           pushConfig,
	}, nil
}

// loadPushConfig returns push backend config from environment variables.
func loadPushConfig() (*PushConfig, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("DELIGHT_PUSH_BACKEND")))
	gorushURL := strings.TrimSpace(os.Getenv("DELIGHT_GORUSH_URL"))
	topic := strings.TrimSpace(os.Getenv("DELIGHT_PUSH_TOPIC"))

	if backend == "" && gorushURL == "" {
		return nil, nil
	}
	if backend == "" {
		backend = "gorush"
	}
	if backend != "gorush" {
		return nil, fmt.Errorf("unsupported DELIGHT_PUSH_BACKEND: %s", backend)
	}
	if topic == "" {
		return nil, fmt.Errorf("DELIGHT_PUSH_TOPIC is required when push is enabled")
	}

	return &PushConfig{
		Backend:   backend,
		GorushURL: gorushURL,
		Topic:     topic,
	}, nil
}
