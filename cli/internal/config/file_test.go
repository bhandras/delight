package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadFileAppliesProfileAndAgentDefaults covers base/profile TOML layering.
func TestLoadFileAppliesProfileAndAgentDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := []byte(`
server_url = "http://base"
agent = "codex"

[push]
mode = "off"
events = ["attention"]
cooldown_sec = 15

[codex]
model = "gpt-base"
reasoning_effort = "medium"
permission_mode = "read-only"
extra_args = ["-c", "base=true"]

[profiles.remote]
server_url = "https://remote"

[profiles.remote.push]
mode = "auto"
events = ["turn-complete", "attention"]

[profiles.remote.codex]
model = "gpt-remote"
reasoning_effort = "high"
extra_args = ["-c", "remote=true"]
`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadFile(cfg, FileOptions{Path: path, Profile: "remote"}); err != nil {
		t.Fatal(err)
	}
	cfg.ApplyAgentDefaults()

	if cfg.ServerURL != "https://remote" {
		t.Fatalf("ServerURL=%q", cfg.ServerURL)
	}
	if cfg.PushMode != "auto" {
		t.Fatalf("PushMode=%q", cfg.PushMode)
	}
	if !cfg.PushNotifyTurnComplete || !cfg.PushNotifyAttention {
		t.Fatalf("push events not both enabled: %#v", cfg)
	}
	if cfg.PushCooldown != 15*time.Second {
		t.Fatalf("PushCooldown=%s", cfg.PushCooldown)
	}
	if cfg.Model != "gpt-remote" {
		t.Fatalf("Model=%q", cfg.Model)
	}
	if cfg.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort=%q", cfg.ReasoningEffort)
	}
	if len(cfg.CodexExtraArgs) != 2 || cfg.CodexExtraArgs[1] != "remote=true" {
		t.Fatalf("CodexExtraArgs=%#v", cfg.CodexExtraArgs)
	}
}

// TestLoadFileReturnsMissingExplicitPath ensures explicit config typos fail.
func TestLoadFileReturnsMissingExplicitPath(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "missing.toml")
	err = LoadFile(cfg, FileOptions{Path: path, ExplicitPath: true})
	if err == nil {
		t.Fatal("expected missing explicit config path to fail")
	}
}
