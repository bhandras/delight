package sdk

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/stretchr/testify/require"
)

func TestGenerateMasterKeyBase64Produces32Bytes(t *testing.T) {
	keyB64, err := GenerateMasterKeyBase64()
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	require.NoError(t, err)
	require.Len(t, raw, 32)
}

func TestGenerateEd25519KeyPairAccessors(t *testing.T) {
	kp, err := GenerateEd25519KeyPair()
	require.NoError(t, err)

	pubB64 := kp.PublicKey()
	privB64 := kp.PrivateKey()
	require.NotEmpty(t, pubB64)
	require.NotEmpty(t, privB64)

	pub, err := base64.StdEncoding.DecodeString(pubB64)
	require.NoError(t, err)
	priv, err := base64.StdEncoding.DecodeString(privB64)
	require.NoError(t, err)
	require.Len(t, pub, ed25519.PublicKeySize)
	require.Len(t, priv, ed25519.PrivateKeySize)
}

func TestSetServerURLStoresValue(t *testing.T) {
	client := NewClient("http://example.com")
	client.SetServerURL("http://new.example.com")
	_, _ = client.dispatch.call(func() (interface{}, error) { return nil, nil })

	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, "http://new.example.com", client.serverURL)
}

func TestAuthWithKeyPairRoundTrip(t *testing.T) {
	kp, err := GenerateEd25519KeyPair()
	require.NoError(t, err)

	publicKeyB64 := kp.PublicKey()
	privateKeyB64 := kp.PrivateKey()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/auth", r.URL.Path)
		require.Equal(t, "POST", r.Method)

		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, publicKeyB64, payload["publicKey"])

		challengeRaw, err := base64.StdEncoding.DecodeString(payload["challenge"])
		require.NoError(t, err)
		require.Equal(t, []byte("delight-auth-challenge"), challengeRaw)

		sigRaw, err := base64.StdEncoding.DecodeString(payload["signature"])
		require.NoError(t, err)
		pubRaw, err := base64.StdEncoding.DecodeString(publicKeyB64)
		require.NoError(t, err)
		require.True(t, ed25519.Verify(pubRaw, challengeRaw, sigRaw))

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"token":   "token-123",
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	token, err := client.AuthWithKeyPair(publicKeyB64, privateKeyB64)
	require.NoError(t, err)
	require.Equal(t, "token-123", token)

	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, "token-123", client.token)
}

func TestApproveTerminalAuthEncryptsMasterKey(t *testing.T) {
	terminalPub, terminalPriv, err := crypto.GenerateBoxKeyPair()
	require.NoError(t, err)

	master := bytes.Repeat([]byte{0xAB}, 32)
	masterB64 := base64.StdEncoding.EncodeToString(master)
	terminalPubB64 := base64.StdEncoding.EncodeToString(terminalPub[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/auth/response", r.URL.Path)
		require.Equal(t, "POST", r.Method)

		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, terminalPubB64, payload["publicKey"])

		encryptedRaw, err := base64.StdEncoding.DecodeString(payload["response"])
		require.NoError(t, err)
		decrypted, err := crypto.DecryptBox(encryptedRaw, terminalPriv)
		require.NoError(t, err)
		require.Equal(t, master, decrypted)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	client.SetToken("t")
	require.NoError(t, client.ApproveTerminalAuth(terminalPubB64, masterB64))
}

func TestConnectWithoutTokenErrors(t *testing.T) {
	client := NewClient("http://example.com")
	err := client.Connect()
	require.Error(t, err)
	require.Contains(t, err.Error(), "token not set")
}

type disconnectListener struct {
	ch     chan string
	reason string
}

func (d *disconnectListener) OnConnected()                 {}
func (d *disconnectListener) OnUpdate(string, string)      {}
func (d *disconnectListener) OnError(string)               {}
func (d *disconnectListener) OnDisconnected(reason string) { d.ch <- reason }

func TestDisconnectNotifiesListener(t *testing.T) {
	client := NewClient("http://example.com")
	listener := &disconnectListener{ch: make(chan string, 1)}
	client.SetListener(listener)
	client.Disconnect()

	select {
	case reason := <-listener.ch:
		require.Equal(t, "closed", reason)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for disconnect")
	}
}

func TestLogPanicWritesToLogManager(t *testing.T) {
	// Avoid affecting the global logger state by using a fresh logManager.
	m := newLogManager()
	require.NoError(t, m.setDir(t.TempDir()))
	m.appendPanic("GO PANIC: ctx: oops")
	snapshot := string(m.getLogsSnapshot())
	require.Contains(t, snapshot, "GO PANIC: ctx: oops")
}

func TestLogRotationCreatesPriorFile(t *testing.T) {
	m := newLogManager()
	dir := t.TempDir()
	require.NoError(t, m.setDir(dir))
	m.appendLogLine("line 1")

	m.mu.Lock()
	require.NoError(t, m.rotateLocked())
	m.mu.Unlock()

	rotated := filepath.Join(dir, "sdk.log.1")
	_, err := os.Stat(rotated)
	require.NoError(t, err)
}
