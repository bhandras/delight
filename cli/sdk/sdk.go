package sdk

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/websocket"
)

// Listener receives SDK events. Methods must be safe to call from any goroutine.
type Listener interface {
	OnConnected()
	OnDisconnected(reason string)
	OnUpdate(sessionID string, updateJSON string)
	OnError(message string)
}

// Client is a minimal mobile SDK client suitable for gomobile.
type Client struct {
	serverURL string
	token     string
	debug     bool

	mu           sync.Mutex
	masterSecret []byte
	dataKeys     map[string][]byte
	listener     Listener
	userSocket   *websocket.Client
	httpClient   *http.Client
}

// NewClient creates a new SDK client.
func NewClient(serverURL string) *Client {
	return &Client{
		serverURL:  serverURL,
		dataKeys:   make(map[string][]byte),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// SetListener registers the listener for SDK events.
func (c *Client) SetListener(listener Listener) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listener = listener
}

// SetDebug enables debug logging for underlying sockets.
func (c *Client) SetDebug(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.debug = enabled
	if c.userSocket != nil {
		c.userSocket.SetDebug(enabled)
	}
}

// SetToken configures the auth token.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// SetMasterKeyBase64 sets the 32-byte master key from base64.
func (c *Client) SetMasterKeyBase64(keyB64 string) error {
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode master key: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("master key must be 32 bytes, got %d", len(raw))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.masterSecret = raw
	return nil
}

// SetSessionDataKey stores a raw 32-byte data encryption key (base64).
func (c *Client) SetSessionDataKey(sessionID, keyB64 string) error {
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode data key: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("data key must be 32 bytes, got %d", len(raw))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataKeys[sessionID] = raw
	return nil
}

// SetEncryptedSessionDataKey decrypts and stores a session data key.
func (c *Client) SetEncryptedSessionDataKey(sessionID, encryptedB64 string) error {
	c.mu.Lock()
	master := c.masterSecret
	c.mu.Unlock()

	if len(master) == 0 {
		return fmt.Errorf("master key not set")
	}

	decrypted, err := crypto.DecryptDataEncryptionKey(encryptedB64, master)
	if err != nil {
		// Fallback: accept raw base64 data keys (legacy server behavior).
		raw, decodeErr := base64.StdEncoding.DecodeString(encryptedB64)
		if decodeErr != nil || len(raw) != 32 {
			return fmt.Errorf("decrypt data key: %w", err)
		}
		decrypted = raw
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataKeys[sessionID] = decrypted
	return nil
}

// Connect opens a user-scoped websocket connection and begins emitting updates.
func (c *Client) Connect() error {
	c.mu.Lock()
	token := c.token
	debug := c.debug
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("token not set")
	}

	socket := websocket.NewUserClient(c.serverURL, token, debug)
	socket.On(websocket.EventUpdate, c.handleUpdate)
	socket.On(websocket.EventEphemeral, c.handleEphemeral)

	if err := socket.Connect(); err != nil {
		c.emitError(fmt.Sprintf("connect failed: %v", err))
		return err
	}
	if !socket.WaitForConnect(10 * time.Second) {
		err := fmt.Errorf("connect timeout")
		c.emitError(err.Error())
		return err
	}

	c.mu.Lock()
	c.userSocket = socket
	listener := c.listener
	c.mu.Unlock()

	if listener != nil {
		listener.OnConnected()
	}
	return nil
}

// Disconnect closes the websocket connection.
func (c *Client) Disconnect() {
	c.mu.Lock()
	socket := c.userSocket
	listener := c.listener
	c.userSocket = nil
	c.mu.Unlock()

	if socket != nil {
		_ = socket.Close()
	}
	if listener != nil {
		listener.OnDisconnected("closed")
	}
}

// SendMessage encrypts and sends a raw record JSON payload to a session.
func (c *Client) SendMessage(sessionID string, rawRecordJSON string) error {
	c.mu.Lock()
	socket := c.userSocket
	c.mu.Unlock()

	if socket == nil {
		return fmt.Errorf("not connected")
	}

	encrypted, err := c.encryptPayload(sessionID, []byte(rawRecordJSON))
	if err != nil {
		return err
	}
	return socket.SendMessage(sessionID, encrypted)
}

// ListSessions fetches sessions and caches data keys. Returns JSON response.
func (c *Client) ListSessions() (string, error) {
	respBody, err := c.doRequest("GET", "/v1/sessions", nil)
	if err != nil {
		return "", err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(respBody, &decoded); err == nil {
		if sessions, ok := decoded["sessions"].([]interface{}); ok {
			for _, item := range sessions {
				session, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				sessionID, _ := session["id"].(string)
				dataKeyB64, _ := session["dataEncryptionKey"].(string)
				if sessionID != "" && dataKeyB64 != "" {
					_ = c.SetEncryptedSessionDataKey(sessionID, dataKeyB64)
				}
			}
		}
	}

	return string(respBody), nil
}

// GetSessionMessages fetches session messages and decrypts message content.
func (c *Client) GetSessionMessages(sessionID string, limit int) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sessionID required")
	}
	endpoint := fmt.Sprintf("/v1/sessions/%s/messages", url.PathEscape(sessionID))
	if limit > 0 {
		endpoint = fmt.Sprintf("%s?limit=%d", endpoint, limit)
	}

	respBody, err := c.doRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return string(respBody), nil
	}

	messages, ok := decoded["messages"].([]interface{})
	if !ok {
		return string(respBody), nil
	}

	for _, item := range messages {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := msg["content"].(map[string]interface{})
		if !ok {
			continue
		}
		decrypted, err := c.decryptEnvelope(sessionID, content)
		if err != nil || decrypted == nil {
			continue
		}
		msg["content"] = decrypted
	}

	encoded, err := json.Marshal(decoded)
	if err != nil {
		return string(respBody), nil
	}
	return string(encoded), nil
}

func (c *Client) handleEphemeral(data map[string]interface{}) {
	c.emitUpdate("", data)
}

func (c *Client) handleUpdate(data map[string]interface{}) {
	body, _ := data["body"].(map[string]interface{})
	if body == nil {
		c.emitUpdate("", data)
		return
	}

	updateType, _ := body["t"].(string)
	sessionID := ""

	switch updateType {
	case "new-message":
		sessionID, _ = body["sid"].(string)
		if msg, ok := body["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(map[string]interface{}); ok {
				decrypted, err := c.decryptEnvelope(sessionID, content)
				if err == nil && decrypted != nil {
					msg["content"] = decrypted
				}
			}
		}
	case "new-session":
		sessionID, _ = body["id"].(string)
		if dataKey, ok := body["dataEncryptionKey"].(string); ok && dataKey != "" {
			_ = c.SetEncryptedSessionDataKey(sessionID, dataKey)
		}
	case "update-session":
		sessionID, _ = body["id"].(string)
	}

	c.emitUpdate(sessionID, data)
}

func (c *Client) emitUpdate(sessionID string, payload map[string]interface{}) {
	listener := c.getListener()
	if listener == nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		listener.OnError(fmt.Sprintf("encode update: %v", err))
		return
	}
	listener.OnUpdate(sessionID, string(encoded))
}

func (c *Client) emitError(message string) {
	listener := c.getListener()
	if listener != nil {
		listener.OnError(message)
	}
}

func (c *Client) getListener() Listener {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listener
}

func (c *Client) encryptPayload(sessionID string, data []byte) (string, error) {
	c.mu.Lock()
	dataKey := c.dataKeys[sessionID]
	master := c.masterSecret
	c.mu.Unlock()

	var encrypted []byte
	var err error

	if len(dataKey) == 32 {
		encrypted, err = crypto.EncryptWithDataKey(json.RawMessage(data), dataKey)
	} else {
		if len(master) != 32 {
			return "", fmt.Errorf("master key not set")
		}
		var secretKey [32]byte
		copy(secretKey[:], master)
		encrypted, err = crypto.EncryptLegacy(json.RawMessage(data), &secretKey)
	}
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (c *Client) decryptEnvelope(sessionID string, content map[string]interface{}) (map[string]interface{}, error) {
	if content == nil {
		return nil, nil
	}
	t, _ := content["t"].(string)
	if t != "encrypted" {
		return nil, nil
	}
	cipherText, _ := content["c"].(string)
	if cipherText == "" {
		return nil, fmt.Errorf("empty encrypted content")
	}

	decrypted, err := c.decryptPayload(sessionID, cipherText)
	if err != nil {
		return nil, err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(decrypted, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func (c *Client) decryptPayload(sessionID, dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	if len(encrypted) == 0 {
		return nil, fmt.Errorf("empty encrypted data")
	}

	c.mu.Lock()
	dataKey := c.dataKeys[sessionID]
	master := c.masterSecret
	c.mu.Unlock()

	// AES-GCM format has version byte 0 and 12-byte nonce.
	if encrypted[0] == 0 && len(encrypted) >= 1+12+16 {
		key := dataKey
		if len(key) != 32 {
			if len(master) == 32 {
				key = master
			} else {
				return nil, fmt.Errorf("AES-GCM data but no key available")
			}
		}
		var result json.RawMessage
		if err := crypto.DecryptWithDataKey(encrypted, key, &result); err != nil {
			return nil, err
		}
		return []byte(result), nil
	}

	// Legacy SecretBox format: [nonce(24)][ciphertext]
	var secretKey [32]byte
	switch {
	case len(dataKey) == 32:
		copy(secretKey[:], dataKey)
	case len(master) == 32:
		copy(secretKey[:], master)
	default:
		return nil, fmt.Errorf("secret key not set")
	}

	var result json.RawMessage
	if err := crypto.DecryptLegacy(encrypted, &secretKey, &result); err != nil {
		return nil, err
	}
	return []byte(result), nil
}

func (c *Client) doRequest(method, path string, body []byte) ([]byte, error) {
	c.mu.Lock()
	token := c.token
	baseURL := c.serverURL
	client := c.httpClient
	c.mu.Unlock()

	if token == "" {
		return nil, fmt.Errorf("token not set")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("server URL not set")
	}

	fullURL := fmt.Sprintf("%s%s", baseURL, path)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, fullURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed: %s", string(respBody))
	}
	return respBody, nil
}
