package storage

import (
	"path/filepath"
	"testing"
)

func TestLocalSessionInfoRoundTrip(t *testing.T) {
	home := t.TempDir()
	sessionID := "s1"

	in := LocalSessionInfo{
		SessionID:   sessionID,
		AgentType:   "codex",
		ResumeToken: "thread-1",
		RolloutPath: "/tmp/rollout.jsonl",
	}
	if err := SaveLocalSessionInfo(home, in); err != nil {
		t.Fatalf("SaveLocalSessionInfo returned error: %v", err)
	}

	got, ok, err := LoadLocalSessionInfo(home, sessionID)
	if err != nil {
		t.Fatalf("LoadLocalSessionInfo returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got.SessionID != sessionID {
		t.Fatalf("expected session id avoid mutation, got %q", got.SessionID)
	}
	if got.AgentType != "codex" {
		t.Fatalf("expected agent type codex, got %q", got.AgentType)
	}
	if got.ResumeToken != "thread-1" {
		t.Fatalf("expected resume token thread-1, got %q", got.ResumeToken)
	}
	if got.RolloutPath != "/tmp/rollout.jsonl" {
		t.Fatalf("expected rollout path /tmp/rollout.jsonl, got %q", got.RolloutPath)
	}
	if got.UpdatedAtMs == 0 {
		t.Fatalf("expected UpdatedAtMs to be set")
	}
}

func TestLocalSessionInfoPathIsScoped(t *testing.T) {
	home := t.TempDir()
	path, err := localSessionInfoPath(home, "a/b")
	if err != nil {
		t.Fatalf("localSessionInfoPath returned error: %v", err)
	}
	if filepath.Base(filepath.Dir(path)) != "a_b" {
		t.Fatalf("expected session id to be sanitized in path, got %q", path)
	}
}
