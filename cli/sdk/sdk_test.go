package sdk

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/stretchr/testify/require"
)

type captureListener struct {
	mu sync.Mutex

	lastSession string
	lastUpdate  string
	lastError   string

	updateCh chan struct{}
	errorCh  chan struct{}
}

func newCaptureListener() *captureListener {
	return &captureListener{
		updateCh: make(chan struct{}, 16),
		errorCh:  make(chan struct{}, 16),
	}
}

func (c *captureListener) OnConnected()                 {}
func (c *captureListener) OnDisconnected(reason string) {}
func (c *captureListener) OnUpdate(sessionID string, updateJSON string) {
	c.mu.Lock()
	c.lastSession = sessionID
	c.lastUpdate = updateJSON
	c.mu.Unlock()
	select {
	case c.updateCh <- struct{}{}:
	default:
	}
}
func (c *captureListener) OnError(message string) {
	c.mu.Lock()
	c.lastError = message
	c.mu.Unlock()
	select {
	case c.errorCh <- struct{}{}:
	default:
	}
}

func (c *captureListener) waitUpdate(t *testing.T) {
	t.Helper()
	select {
	case <-c.updateCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for update")
	}
}

func TestDecryptLegacyString(t *testing.T) {
	client := NewClient("http://example.com")
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	if err := client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(secret)); err != nil {
		require.NoError(t, err)
	}

	payload := map[string]interface{}{
		"host": "m2.local",
		"pid":  123,
	}
	var key [32]byte
	copy(key[:], secret)
	encrypted, err := crypto.EncryptLegacy(payload, &key)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(encrypted)

	decoded, err := client.decryptLegacyString(encoded)
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(decoded), &result))
	require.Equal(t, "m2.local", result["host"])
}

func TestListSessionsDecryptsMetadata(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(9)
	}
	var key [32]byte
	copy(key[:], secret)
	metadata := map[string]interface{}{"host": "m2.local", "path": "/work/project"}
	encrypted, err := crypto.EncryptLegacy(metadata, &key)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(encrypted)
	dataKey := base64.StdEncoding.EncodeToString(make([]byte, 32))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := map[string]interface{}{
			"sessions": []map[string]interface{}{
				{
					"id":               "s1",
					"updatedAt":        1,
					"active":           true,
					"activeAt":         1,
					"metadata":         encoded,
					"dataEncryptionKey": dataKey,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(secret)))

	result, err := client.ListSessions()
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &decoded))
	items, ok := decoded["sessions"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	session := items[0].(map[string]interface{})
	metadataJSON, _ := session["metadata"].(string)
	var meta map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(metadataJSON), &meta))
	require.Equal(t, "m2.local", meta["host"])
}

func TestListMachinesDecryptsMetadataAndDaemonState(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(7)
	}
	var key [32]byte
	copy(key[:], secret)
	metadata := map[string]interface{}{"host": "m2.local", "platform": "darwin"}
	daemon := map[string]interface{}{"pid": 123, "status": "ok"}
	metadataEnc, err := crypto.EncryptLegacy(metadata, &key)
	require.NoError(t, err)
	daemonEnc, err := crypto.EncryptLegacy(daemon, &key)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/machines" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := []map[string]interface{}{
			{
				"id":              "m1",
				"active":          true,
				"metadata":        base64.StdEncoding.EncodeToString(metadataEnc),
				"daemonState":     base64.StdEncoding.EncodeToString(daemonEnc),
				"daemonStateVersion": 4,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(secret)))

	result, err := client.ListMachines()
	require.NoError(t, err)

	var decoded []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &decoded))
	require.Len(t, decoded, 1)
	machine := decoded[0]
	metadataJSON, _ := machine["metadata"].(string)
	var meta map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(metadataJSON), &meta))
	require.Equal(t, "m2.local", meta["host"])
	daemonJSON, _ := machine["daemonState"].(string)
	var daemonState map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(daemonJSON), &daemonState))
	require.Equal(t, float64(123), daemonState["pid"])
}

func TestGetSessionMessagesDecryptsContent(t *testing.T) {
	sessionID := "s1"
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(1 + i)
	}
	encrypted, err := crypto.EncryptWithDataKey(map[string]interface{}{
		"type": "text",
		"text": "hello",
	}, dataKey)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(encrypted)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/sessions/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := map[string]interface{}{
			"messages": []map[string]interface{}{
				{
					"id": "m1",
					"content": map[string]interface{}{
						"t": "encrypted",
						"c": encoded,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	require.NoError(t, client.SetSessionDataKey(sessionID, base64.StdEncoding.EncodeToString(dataKey)))

	result, err := client.GetSessionMessages(sessionID, 10)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &decoded))
	items := decoded["messages"].([]interface{})
	msg := items[0].(map[string]interface{})
	content := msg["content"].(map[string]interface{})
	require.Equal(t, "hello", content["text"])
}

func TestHandleUpdateDecryptsNewMessage(t *testing.T) {
	client := NewClient("http://example.com")
	listener := newCaptureListener()
	client.SetListener(listener)

	sessionID := "s1"
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(9)
	}
	require.NoError(t, client.SetSessionDataKey(sessionID, base64.StdEncoding.EncodeToString(dataKey)))
	encrypted, err := crypto.EncryptWithDataKey(map[string]interface{}{
		"type": "text",
		"text": "hi",
	}, dataKey)
	require.NoError(t, err)
	update := map[string]interface{}{
		"body": map[string]interface{}{
			"t":   "new-message",
			"sid": sessionID,
			"message": map[string]interface{}{
				"content": map[string]interface{}{
					"t": "encrypted",
					"c": base64.StdEncoding.EncodeToString(encrypted),
				},
			},
		},
	}
	client.handleUpdate(update)

	listener.waitUpdate(t)
	listener.mu.Lock()
	defer listener.mu.Unlock()
	require.Equal(t, sessionID, listener.lastSession)
	require.NotEmpty(t, listener.lastUpdate)
	require.Contains(t, listener.lastUpdate, "\"text\":\"hi\"")
}

func TestSetEncryptedSessionDataKeyDecrypts(t *testing.T) {
	client := NewClient("http://example.com")
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(2)
	}
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))
	pub, _, err := crypto.DeriveContentKeyPair(master)
	require.NoError(t, err)
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(8)
	}
	encrypted, err := crypto.EncryptBox(dataKey, pub)
	require.NoError(t, err)
	payload := append([]byte{0x00}, encrypted...)
	encoded := base64.StdEncoding.EncodeToString(payload)

	require.NoError(t, client.SetEncryptedSessionDataKey("s1", encoded))

	client.mu.Lock()
	stored := client.dataKeys["s1"]
	client.mu.Unlock()
	require.Len(t, stored, 32)
	require.Equal(t, byte(8), stored[0])
}

func TestSetEncryptedSessionDataKeyFallbackRaw(t *testing.T) {
	client := NewClient("http://example.com")
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(5)
	}
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(3)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	require.NoError(t, client.SetEncryptedSessionDataKey("s1", encoded))
	client.mu.Lock()
	stored := client.dataKeys["s1"]
	client.mu.Unlock()
	require.Len(t, stored, 32)
	require.Equal(t, byte(3), stored[0])
}

func TestParseTerminalURL(t *testing.T) {
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(4)
	}
	query := base64.RawURLEncoding.EncodeToString(pub)
	parsed, err := ParseTerminalURL("delight://terminal?" + query)
	require.NoError(t, err)
	decoded, _ := base64.StdEncoding.DecodeString(parsed)
	require.Len(t, decoded, 32)

	_, err = ParseTerminalURL("http://terminal?" + query)
	require.Error(t, err)
	_, err = ParseTerminalURL("delight://nope?" + query)
	require.Error(t, err)
	_, err = ParseTerminalURL("delight://terminal")
	require.Error(t, err)

	_, err = ParseTerminalURL("delight://terminal?%%%")
	require.Error(t, err)
}

func TestDecryptPayloadLegacyWithDataKey(t *testing.T) {
	client := NewClient("http://example.com")
	sessionID := "s1"
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(11)
	}
	var key [32]byte
	copy(key[:], dataKey)
	encrypted, err := crypto.EncryptLegacy(json.RawMessage(`{"ok":true}`), &key)
	require.NoError(t, err)
	require.NoError(t, client.SetSessionDataKey(sessionID, base64.StdEncoding.EncodeToString(dataKey)))

	decrypted, err := client.decryptPayload(sessionID, base64.StdEncoding.EncodeToString(encrypted))
	require.NoError(t, err)
	require.Contains(t, string(decrypted), "\"ok\":true")
}

func TestDecryptPayloadLegacyWithMaster(t *testing.T) {
	client := NewClient("http://example.com")
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(12)
	}
	var key [32]byte
	copy(key[:], master)
	encrypted, err := crypto.EncryptLegacy(json.RawMessage(`{"ok":true}`), &key)
	require.NoError(t, err)
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))

	decrypted, err := client.decryptPayload("s1", base64.StdEncoding.EncodeToString(encrypted))
	require.NoError(t, err)
	require.Contains(t, string(decrypted), "\"ok\":true")
}

func TestDecryptPayloadAESUsesMasterFallback(t *testing.T) {
	client := NewClient("http://example.com")
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(13)
	}
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))

	encrypted, err := crypto.EncryptWithDataKey(json.RawMessage(`{"ok":true}`), master)
	require.NoError(t, err)
	decrypted, err := client.decryptPayload("s1", base64.StdEncoding.EncodeToString(encrypted))
	require.NoError(t, err)
	require.Contains(t, string(decrypted), "\"ok\":true")
}

func TestDecryptEnvelopeNonEncrypted(t *testing.T) {
	client := NewClient("http://example.com")
	result, err := client.decryptEnvelope("s1", map[string]interface{}{"t": "text"})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestEncryptPayloadPaths(t *testing.T) {
	client := NewClient("http://example.com")
	sessionID := "s1"
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(14)
	}
	require.NoError(t, client.SetSessionDataKey(sessionID, base64.StdEncoding.EncodeToString(dataKey)))
	encoded, err := client.encryptPayload(sessionID, []byte(`{"ok":true}`))
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.Equal(t, byte(0), raw[0])

	client = NewClient("http://example.com")
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(15)
	}
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))
	encoded, err = client.encryptPayload("s2", []byte(`{"ok":true}`))
	require.NoError(t, err)
	raw, err = base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.NotEqual(t, byte(0), raw[0])
}

func TestHandleUpdateNewSessionStoresDataKey(t *testing.T) {
	client := NewClient("http://example.com")
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(16)
	}
	require.NoError(t, client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))
	pub, _, err := crypto.DeriveContentKeyPair(master)
	require.NoError(t, err)
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(17)
	}
	encrypted, err := crypto.EncryptBox(dataKey, pub)
	require.NoError(t, err)
	payload := append([]byte{0x00}, encrypted...)
	encoded := base64.StdEncoding.EncodeToString(payload)

	update := map[string]interface{}{
		"body": map[string]interface{}{
			"t":                 "new-session",
			"id":                "s1",
			"dataEncryptionKey": encoded,
		},
	}
	client.handleUpdate(update)

	client.mu.Lock()
	stored := client.dataKeys["s1"]
	client.mu.Unlock()
	require.Len(t, stored, 32)
	require.Equal(t, byte(17), stored[0])
}

func TestHandleUpdateUpdateSessionEmits(t *testing.T) {
	client := NewClient("http://example.com")
	listener := newCaptureListener()
	client.SetListener(listener)
	update := map[string]interface{}{
		"body": map[string]interface{}{
			"t":  "update-session",
			"id": "s1",
		},
	}
	client.handleUpdate(update)
	listener.waitUpdate(t)
	listener.mu.Lock()
	defer listener.mu.Unlock()
	require.Equal(t, "s1", listener.lastSession)
	require.NotEmpty(t, listener.lastUpdate)
}

func TestDoRequestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	_, err := client.doRequest("GET", "/v1/bad", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nope")
}

func TestSetMasterKeyBase64Invalid(t *testing.T) {
	client := NewClient("http://example.com")
	err := client.SetMasterKeyBase64("not-base64")
	require.Error(t, err)
	err = client.SetMasterKeyBase64(base64.StdEncoding.EncodeToString([]byte("short")))
	require.Error(t, err)
}

func TestSetSessionDataKeyInvalid(t *testing.T) {
	client := NewClient("http://example.com")
	err := client.SetSessionDataKey("s1", "not-base64")
	require.Error(t, err)
	err = client.SetSessionDataKey("s1", base64.StdEncoding.EncodeToString([]byte("short")))
	require.Error(t, err)
}

func TestGetSessionMessagesMissingSessionID(t *testing.T) {
	client := NewClient("http://example.com")
	_, err := client.GetSessionMessages("", 10)
	require.Error(t, err)
}

func TestDecryptEnvelopeEmptyCipher(t *testing.T) {
	client := NewClient("http://example.com")
	_, err := client.decryptEnvelope("s1", map[string]interface{}{"t": "encrypted", "c": ""})
	require.Error(t, err)
}

func TestDecryptPayloadAESMissingKey(t *testing.T) {
	client := NewClient("http://example.com")
	key := make([]byte, 32)
	encrypted, err := crypto.EncryptWithDataKey(json.RawMessage(`{"ok":true}`), key)
	require.NoError(t, err)
	_, err = client.decryptPayload("s1", base64.StdEncoding.EncodeToString(encrypted))
	require.Error(t, err)
}

func TestEncryptPayloadMissingKeys(t *testing.T) {
	client := NewClient("http://example.com")
	_, err := client.encryptPayload("s1", []byte(`{"ok":true}`))
	require.Error(t, err)
}

func TestHandleEphemeralEmitsUpdate(t *testing.T) {
	client := NewClient("http://example.com")
	listener := newCaptureListener()
	client.SetListener(listener)
	client.handleEphemeral(map[string]interface{}{"t": "ping"})
	listener.waitUpdate(t)
	listener.mu.Lock()
	defer listener.mu.Unlock()
	require.NotEmpty(t, listener.lastUpdate)
}

func TestHandleUpdateNilBodyEmitsUpdate(t *testing.T) {
	client := NewClient("http://example.com")
	listener := newCaptureListener()
	client.SetListener(listener)
	client.handleUpdate(map[string]interface{}{})
	listener.waitUpdate(t)
	listener.mu.Lock()
	defer listener.mu.Unlock()
	require.NotEmpty(t, listener.lastUpdate)
	require.Equal(t, "", listener.lastSession)
}

func TestListSessionsMalformedJSONReturnsRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	result, err := client.ListSessions()
	require.NoError(t, err)
	require.Equal(t, "not-json", result)
}

func TestListMachinesMalformedJSONReturnsRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"machines\":123}"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	result, err := client.ListMachines()
	require.NoError(t, err)
	require.Equal(t, "{\"machines\":123}", result)
}
