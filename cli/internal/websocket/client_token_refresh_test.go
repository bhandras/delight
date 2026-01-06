package websocket

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_MaybeRefreshToken_AuthErrorTriggersRefresher(t *testing.T) {
	t.Parallel()

	var refreshed atomic.Int32
	var reconnected atomic.Int32
	done := make(chan struct{}, 1)

	c := NewClient("http://example", "old", "s1", TransportWebSocket, false)
	c.SetTokenRefresher(func() (string, error) {
		refreshed.Add(1)
		return "new-token", nil
	})
	c.reconnectFn = func() error {
		reconnected.Add(1)
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	}
	c.mu.Lock()
	c.lastRefreshAt = time.Now().Add(-time.Hour)
	c.mu.Unlock()

	c.maybeRefreshToken([]any{"401 unauthorized"})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for reconnect")
	}

	if refreshed.Load() != 1 {
		t.Fatalf("refreshed=%d, want 1", refreshed.Load())
	}
	if reconnected.Load() != 1 {
		t.Fatalf("reconnected=%d, want 1", reconnected.Load())
	}
	if got := c.token; got != "new-token" {
		t.Fatalf("token=%q, want new-token", got)
	}
}

func TestClient_MaybeRefreshToken_NonAuthErrorDoesNothing(t *testing.T) {
	t.Parallel()

	var refreshed atomic.Int32

	c := NewClient("http://example", "old", "s1", TransportWebSocket, false)
	c.SetTokenRefresher(func() (string, error) {
		refreshed.Add(1)
		return "new-token", nil
	})
	c.mu.Lock()
	c.lastRefreshAt = time.Now().Add(-time.Hour)
	c.mu.Unlock()

	c.maybeRefreshToken([]any{"some other error"})
	time.Sleep(50 * time.Millisecond)

	if refreshed.Load() != 0 {
		t.Fatalf("refreshed=%d, want 0", refreshed.Load())
	}
}
