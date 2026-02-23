package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGorushSenderSendEncrypted verifies request shape and response parsing.
func TestGorushSenderSendEncrypted(t *testing.T) {
	t.Parallel()

	var received gorushPushRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/push", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"counts":{"success":2,"failure":1}}`))
	}))
	defer srv.Close()

	sender, err := NewGorushSender(GorushConfig{URL: srv.URL + "/api/push", Topic: "com.example.app"})
	require.NoError(t, err)
	result, err := sender.SendEncrypted(context.Background(), []string{"token-a", "", "token-b"}, "ciphertext")
	require.NoError(t, err)
	require.Equal(t, Result{Sent: 2, Failed: 1}, result)

	require.Len(t, received.Notifications, 1)
	n := received.Notifications[0]
	require.Equal(t, []string{"token-a", "token-b"}, n.Tokens)
	require.Equal(t, gorushPlatformIOS, n.Platform)
	require.Equal(t, "com.example.app", n.Topic)
	require.Equal(t, fallbackPushTitle, n.Title)
	require.Equal(t, fallbackPushMessage, n.Message)
	require.True(t, n.ContentAvailable)
	require.Equal(t, "high", n.Priority)
	delightRaw, ok := n.Data["delight"]
	require.True(t, ok)
	delight, ok := delightRaw.(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "ciphertext", delight["ciphertext"])
}

// TestGorushSenderSendEncryptedHTTPError verifies non-2xx handling.
func TestGorushSenderSendEncryptedHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	sender, err := NewGorushSender(GorushConfig{URL: srv.URL, Topic: "com.example.app"})
	require.NoError(t, err)
	_, err = sender.SendEncrypted(context.Background(), []string{"token-a"}, "ciphertext")
	require.Error(t, err)
}

// TestNewGorushSenderMissingTopic verifies constructor validation.
func TestNewGorushSenderMissingTopic(t *testing.T) {
	t.Parallel()

	_, err := NewGorushSender(GorushConfig{})
	require.Error(t, err)
}
