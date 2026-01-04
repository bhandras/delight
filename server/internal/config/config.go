package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds server configuration.
type Config struct {
	// Addr is the listen address for the HTTP(S) server.
	Addr string
	DatabasePath   string
	MasterSecret   string
	Debug          bool
	AllowedOrigins []string
	// TLS holds HTTPS configuration. If nil, the server runs in plain HTTP mode.
	TLS *TLSConfig
}

// TLSConfig holds file paths for serving HTTPS directly from the server.
type TLSConfig struct {
	// CertFile is a PEM-encoded certificate chain.
	CertFile string
	// KeyFile is a PEM-encoded private key.
	KeyFile string
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

	return &Config{
		Addr:           addr,
		DatabasePath:   dbPath,
		MasterSecret:   masterSecret,
		Debug:          debug,
		AllowedOrigins: []string{"*"}, // For self-hosted, allow all origins
		TLS:            overrides.TLS,
	}, nil
}
