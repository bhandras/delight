package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testMasterSecret = "0123456789abcdef0123456789abcdef"

// TestPushNotifierNotifySuccess verifies success-path response parsing.
func TestPushNotifierNotifySuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/push-notifications", r.URL.Path)
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushResponse{
			Success: true,
			Sent:    1,
			Failed:  0,
		})
	}))
	defer srv.Close()

	n, err := NewPushNotifier(PushConfig{
		ServerURL: srv.URL,
		TokenProvider: func() string {
			return "token"
		},
		MasterSecret: []byte(testMasterSecret),
		Cooldown:     0,
	})
	require.NoError(t, err)

	err = n.Notify(context.Background(), PushMessage{
		AlertKey:  "turn-complete",
		Event:     "turn-complete",
		Timestamp: time.Now().UnixMilli(),
	})
	require.NoError(t, err)
}

// TestPushNotifierNotifyNoTokens verifies non-2xx API errors are surfaced.
func TestPushNotifierNotifyNoTokens(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(pushErrorResponse{
			Error: "no registered push tokens for account",
		})
	}))
	defer srv.Close()

	n, err := NewPushNotifier(PushConfig{
		ServerURL: srv.URL,
		TokenProvider: func() string {
			return "token"
		},
		MasterSecret: []byte(testMasterSecret),
		Cooldown:     0,
	})
	require.NoError(t, err)

	err = n.Notify(context.Background(), PushMessage{
		AlertKey:  "turn-complete",
		Event:     "turn-complete",
		Timestamp: time.Now().UnixMilli(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no registered push tokens for account")
}

// TestPushNotifierNotifyReportedFailure verifies 2xx bodies are validated.
func TestPushNotifierNotifyReportedFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushResponse{
			Success: false,
			Sent:    0,
			Failed:  0,
		})
	}))
	defer srv.Close()

	n, err := NewPushNotifier(PushConfig{
		ServerURL: srv.URL,
		TokenProvider: func() string {
			return "token"
		},
		MasterSecret: []byte(testMasterSecret),
		Cooldown:     0,
	})
	require.NoError(t, err)

	err = n.Notify(context.Background(), PushMessage{
		AlertKey:  "turn-complete",
		Event:     "turn-complete",
		Timestamp: time.Now().UnixMilli(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "sent=0")
}

// TestFormatPushErrorFallback verifies plain-text non-JSON errors are preserved.
func TestFormatPushErrorFallback(t *testing.T) {
	t.Parallel()

	got := formatPushError("502 Bad Gateway", []byte("proxy error"))
	require.True(t, strings.Contains(got, "proxy error"))
}
