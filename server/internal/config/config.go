package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port           int
	DatabasePath   string
	MasterSecret   string
	Debug          bool
	AllowedOrigins []string
}

func Load() (*Config, error) {
	masterSecret := os.Getenv("DELIGHT_MASTER_SECRET")
	if masterSecret == "" {
		return nil, fmt.Errorf("DELIGHT_MASTER_SECRET environment variable is required")
	}

	port := 3005
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./delight.db"
	}

	debug := false
	if debugStr := os.Getenv("DEBUG"); debugStr == "true" || debugStr == "1" {
		debug = true
	}

	return &Config{
		Port:           port,
		DatabasePath:   dbPath,
		MasterSecret:   masterSecret,
		Debug:          debug,
		AllowedOrigins: []string{"*"}, // For self-hosted, allow all origins
	}, nil
}
