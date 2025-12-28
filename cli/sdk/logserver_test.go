package sdk

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetTokenStoresValue(t *testing.T) {
	client := NewClient("http://example.com")
	client.SetToken("abc123")

	_, _ = client.dispatch.call(func() (interface{}, error) { return nil, nil })

	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, "abc123", client.token)
}

func TestSetLogDirectoryAndLogLineWritesFile(t *testing.T) {
	client := NewClient("http://example.com")
	dir := t.TempDir()

	require.NoError(t, client.SetLogDirectory(dir))
	client.LogLine("hello")

	_, _ = client.dispatch.call(func() (interface{}, error) { return nil, nil })

	logPath := filepath.Join(dir, "sdk.log")
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(logPath)
		require.NoError(t, err)
		if strings.Contains(string(data), "hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for log write; got: %q", string(data))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEmitErrorDeliversToListener(t *testing.T) {
	client := NewClient("http://example.com")
	listener := newCaptureListener()
	client.SetListener(listener)

	client.emitError("boom")
	select {
	case <-listener.errorCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for error")
	}

	listener.mu.Lock()
	defer listener.mu.Unlock()
	require.Equal(t, "boom", listener.lastError)
}

func TestQueuedHandlersSerializeAndDeliver(t *testing.T) {
	client := NewClient("http://example.com")
	listener := newCaptureListener()
	client.SetListener(listener)

	client.handleEphemeralQueued(map[string]interface{}{"t": "ephemeral"})
	client.handleUpdateQueued(map[string]interface{}{
		"body": map[string]interface{}{
			"t":  "update-session",
			"id": "s1",
		},
	})

	listener.waitUpdate(t)
}

func TestStartLogServerServesFiles(t *testing.T) {
	client := NewClient("http://example.com")
	require.NoError(t, client.SetLogDirectory(t.TempDir()))
	client.LogLine("one")
	_, _ = client.dispatch.call(func() (interface{}, error) { return nil, nil })

	baseURL, err := client.StartLogServer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.StopLogServer() })

	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Host)

	res, err := http.Get(baseURL + "/")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	logRes, err := http.Get(baseURL + "/sdk.log")
	require.NoError(t, err)
	defer logRes.Body.Close()
	require.Equal(t, http.StatusOK, logRes.StatusCode)
	bodyBytes, err := io.ReadAll(logRes.Body)
	require.NoError(t, err)
	require.Contains(t, string(bodyBytes), "one")
}

func TestDecodeBase64AnyAcceptsStdAndURL(t *testing.T) {
	raw := []byte("hello world")

	std := "aGVsbG8gd29ybGQ="
	decoded, err := decodeBase64Any(std)
	require.NoError(t, err)
	require.Equal(t, raw, decoded)

	urlEnc := "aGVsbG8gd29ybGQ"
	decoded, err = decodeBase64Any(urlEnc)
	require.NoError(t, err)
	require.Equal(t, raw, decoded)

	_, err = decodeBase64Any("%%%")
	require.Error(t, err)
}

func TestFormatLineAddsTimestamp(t *testing.T) {
	got := formatLine("hello\n")
	require.Contains(t, got, "hello")
	require.True(t, strings.HasSuffix(got, "\n"))
}
