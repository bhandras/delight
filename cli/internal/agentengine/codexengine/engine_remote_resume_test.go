package codexengine

import (
	"errors"
	"testing"
)

func TestIsCodexRemoteSessionNotFound(t *testing.T) {
	if isCodexRemoteSessionNotFound(nil) {
		t.Fatalf("expected false for nil error")
	}
	if isCodexRemoteSessionNotFound(errors.New("something else")) {
		t.Fatalf("expected false for unrelated error")
	}
	if !isCodexRemoteSessionNotFound(errors.New("Session not found for conversation_id: abc")) {
		t.Fatalf("expected true for codex missing session error")
	}
}
