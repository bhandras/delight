package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	// defaultConfigFileName is the TOML file loaded from Delight home.
	defaultConfigFileName = "config.toml"
)

// FileOptions controls TOML config file loading.
type FileOptions struct {
	// Path is an explicit config file path. Empty means use the default path.
	Path string
	// Profile selects a named profile under [profiles.<name>].
	Profile string
	// ExplicitPath reports whether Path came from a CLI flag.
	ExplicitPath bool
}

type fileConfig struct {
	ServerURL         *string `toml:"server_url"`
	HomeDir           *string `toml:"home_dir"`
	ACPURL            *string `toml:"acp_url"`
	ACPAgent          *string `toml:"acp_agent"`
	Agent             *string `toml:"agent"`
	Mode              *string `toml:"mode"`
	StartingMode      *string `toml:"starting_mode"`
	Model             *string `toml:"model"`
	PermissionMode    *string `toml:"permission_mode"`
	ReasoningEffort   *string `toml:"reasoning_effort"`
	SocketIOTransport *string `toml:"socketio_transport"`
	LogLevel          *string `toml:"log_level"`

	Push     *pushFileConfig       `toml:"push"`
	Pushover *pushoverFileConfig   `toml:"pushover"`
	Codex    *agentFileConfig      `toml:"codex"`
	Claude   *agentFileConfig      `toml:"claude"`
	Profiles map[string]fileConfig `toml:"profiles"`
}

type pushFileConfig struct {
	Mode        *string  `toml:"mode"`
	Events      []string `toml:"events"`
	CooldownSec *int     `toml:"cooldown_sec"`
}

type pushoverFileConfig struct {
	Mode        *string  `toml:"mode"`
	Token       *string  `toml:"token"`
	TokenEnv    *string  `toml:"token_env"`
	UserKey     *string  `toml:"user_key"`
	UserKeyEnv  *string  `toml:"user_key_env"`
	Events      []string `toml:"events"`
	CooldownSec *int     `toml:"cooldown_sec"`
	Priority    *int     `toml:"priority"`
}

type agentFileConfig struct {
	Model           *string  `toml:"model"`
	PermissionMode  *string  `toml:"permission_mode"`
	ReasoningEffort *string  `toml:"reasoning_effort"`
	ExtraArgs       []string `toml:"extra_args"`
}

// LoadFile applies TOML configuration and an optional profile to cfg.
func LoadFile(cfg *Config, opts FileOptions) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	path, explicit, err := resolveConfigPath(cfg, opts)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var decoded fileConfig
	if err := toml.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	applyFileConfig(cfg, decoded)
	if profile := strings.TrimSpace(opts.Profile); profile != "" {
		selected, ok := decoded.Profiles[profile]
		if !ok {
			return fmt.Errorf("profile %q not found in %s", profile, path)
		}
		applyFileConfig(cfg, selected)
	}
	return nil
}

// DefaultPath returns the default TOML config path for cfg.
func DefaultPath(cfg *Config) string {
	if cfg == nil || strings.TrimSpace(cfg.DelightHome) == "" {
		return ""
	}
	return filepath.Join(cfg.DelightHome, defaultConfigFileName)
}

// resolveConfigPath returns the effective TOML path and whether it was explicit.
func resolveConfigPath(cfg *Config, opts FileOptions) (string, bool, error) {
	if path := strings.TrimSpace(opts.Path); path != "" {
		expanded, err := expandPath(path)
		if err != nil {
			return "", false, err
		}
		return expanded, true, nil
	}
	if strings.TrimSpace(cfg.DelightHome) == "" {
		return "", false, nil
	}
	return filepath.Join(cfg.DelightHome, defaultConfigFileName), opts.ExplicitPath, nil
}

// applyFileConfig overlays one TOML config object onto cfg.
func applyFileConfig(cfg *Config, file fileConfig) {
	applyString(file.ServerURL, &cfg.ServerURL)
	applyHomeDir(cfg, file.HomeDir)
	applyString(file.ACPURL, &cfg.ACPURL)
	applyString(file.ACPAgent, &cfg.ACPAgent)
	applyString(file.Agent, &cfg.Agent)
	applyString(file.Model, &cfg.Model)
	applyString(file.PermissionMode, &cfg.PermissionMode)
	applyString(file.ReasoningEffort, &cfg.ReasoningEffort)
	applyString(file.SocketIOTransport, &cfg.SocketIOTransport)
	applyString(file.LogLevel, &cfg.LogLevel)
	if file.Mode != nil {
		applyString(file.Mode, &cfg.StartingMode)
	}
	if file.StartingMode != nil {
		applyString(file.StartingMode, &cfg.StartingMode)
	}
	cfg.ACPEnable = cfg.ACPURL != "" && cfg.ACPAgent != ""

	applyPushFileConfig(cfg, file.Push)
	applyPushoverFileConfig(cfg, file.Pushover)
	applyAgentFileConfig(cfg, "codex", file.Codex)
	applyAgentFileConfig(cfg, "claude", file.Claude)
}

// applyPushFileConfig overlays encrypted mobile push settings from TOML.
func applyPushFileConfig(cfg *Config, push *pushFileConfig) {
	if push == nil {
		return
	}
	applyString(push.Mode, &cfg.PushMode)
	if push.CooldownSec != nil && *push.CooldownSec > 0 {
		cfg.PushCooldown = time.Duration(*push.CooldownSec) * time.Second
	}
	if push.Events != nil {
		applyPushEvents(cfg, strings.Join(push.Events, ","))
	}
}

// applyPushoverFileConfig overlays Pushover settings from TOML.
func applyPushoverFileConfig(cfg *Config, pushover *pushoverFileConfig) {
	if pushover == nil {
		return
	}
	applyString(pushover.Mode, &cfg.PushoverMode)
	applyString(pushover.Token, &cfg.PushoverToken)
	applyString(pushover.UserKey, &cfg.PushoverUserKey)
	if pushover.TokenEnv != nil {
		cfg.PushoverToken = os.Getenv(strings.TrimSpace(*pushover.TokenEnv))
	}
	if pushover.UserKeyEnv != nil {
		cfg.PushoverUserKey = os.Getenv(strings.TrimSpace(*pushover.UserKeyEnv))
	}
	if pushover.Priority != nil {
		cfg.PushoverPriority = *pushover.Priority
	}
	if pushover.CooldownSec != nil && *pushover.CooldownSec > 0 {
		cfg.PushoverCooldown = time.Duration(*pushover.CooldownSec) * time.Second
	}
	if pushover.Events != nil {
		applyPushoverEvents(cfg, strings.Join(pushover.Events, ","))
	}
}

// applyAgentFileConfig stores agent-specific defaults from TOML.
func applyAgentFileConfig(cfg *Config, agent string, agentCfg *agentFileConfig) {
	if agentCfg == nil {
		return
	}
	switch agent {
	case "codex":
		applyString(agentCfg.Model, &cfg.CodexModel)
		applyString(agentCfg.PermissionMode, &cfg.CodexPermissionMode)
		applyString(agentCfg.ReasoningEffort, &cfg.CodexReasoningEffort)
		if agentCfg.ExtraArgs != nil {
			cfg.CodexExtraArgs = append([]string(nil), agentCfg.ExtraArgs...)
		}
	case "claude":
		applyString(agentCfg.Model, &cfg.ClaudeModel)
		applyString(agentCfg.PermissionMode, &cfg.ClaudePermissionMode)
		if agentCfg.ExtraArgs != nil {
			cfg.ClaudeExtraArgs = append([]string(nil), agentCfg.ExtraArgs...)
		}
	}
}

// applyString overlays src onto dst when src is present.
func applyString(src *string, dst *string) {
	if src == nil {
		return
	}
	*dst = strings.TrimSpace(*src)
}

// applyHomeDir expands and applies the Delight home directory.
func applyHomeDir(cfg *Config, homeDir *string) {
	if homeDir == nil {
		return
	}
	_ = cfg.SetHomeDir(*homeDir)
}

// expandPath expands leading ~ in user-facing filesystem paths.
func expandPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || trimmed == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if trimmed == "~" {
			return home, nil
		}
		return "", nil
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/")), nil
	}
	return trimmed, nil
}
