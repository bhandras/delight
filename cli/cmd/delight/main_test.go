package main

import "testing"

// TestFindCommandSkipsValueFlags covers flags placed before the command.
func TestFindCommandSkipsValueFlags(t *testing.T) {
	args := []string{
		"--config", "/tmp/delight.toml",
		"--profile=remote",
		"--server-url", "https://example.invalid",
		"codex",
		"run",
	}

	idx, cmd := findCommand(args)
	if cmd != "codex" {
		t.Fatalf("cmd=%q idx=%d", cmd, idx)
	}
}

// TestSplitBackendArgsSeparatesPassthrough covers raw backend args after --.
func TestSplitBackendArgsSeparatesPassthrough(t *testing.T) {
	delightArgs, backendArgs := splitBackendArgs([]string{
		"codex", "run", "--profile", "remote", "--", "-c", "x=true",
	})

	if len(delightArgs) != 4 || delightArgs[0] != "codex" {
		t.Fatalf("delightArgs=%#v", delightArgs)
	}
	if len(backendArgs) != 2 || backendArgs[1] != "x=true" {
		t.Fatalf("backendArgs=%#v", backendArgs)
	}
}

// TestDiscoverHomeDirSupportsLeadingFlag covers early default config lookup.
func TestDiscoverHomeDirSupportsLeadingFlag(t *testing.T) {
	got := discoverHomeDir([]string{"--home-dir", "/tmp/delight", "codex", "run"})
	if got != "/tmp/delight" {
		t.Fatalf("home dir=%q", got)
	}

	got = discoverHomeDir([]string{"codex", "run", "--home-dir=/tmp/other"})
	if got != "/tmp/other" {
		t.Fatalf("home dir=%q", got)
	}
}
