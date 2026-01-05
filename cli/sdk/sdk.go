package sdk

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// userSocketConnectTimeout is the maximum time we wait for the user-scoped
	// Socket.IO connection to be established.
	userSocketConnectTimeout = 10 * time.Second

	// rpcAckTimeout is the maximum time we wait for a Socket.IO RPC ACK.
	rpcAckTimeout = 10 * time.Second

	// sessionKeyHydrateMinInterval is a throttle window that prevents repeated
	// ListSessions calls from hot update streams.
	sessionKeyHydrateMinInterval = 2 * time.Second

	// httpSuccessMin and httpSuccessMaxExclusive define the inclusive/exclusive
	// range for successful HTTP status codes.
	httpSuccessMin          = 200
	httpSuccessMaxExclusive = 300

	// encryptedPayloadFormatV0 is the version byte used for the current AES-GCM
	// message envelope format.
	encryptedPayloadFormatV0 = 0

	// AES-GCM wire framing constants: [version(1)][nonce(12)][ciphertext+tag].
	aesGCMVersionBytes = 1
	aesGCMNonceBytes   = 12
	aesGCMTagBytes     = 16

	// tokenRefreshWindow is how soon before JWT expiry we proactively refresh.
	tokenRefreshWindow = 10 * time.Minute

	// tokenRefreshMinInterval bounds how frequently we attempt refreshing.
	// This avoids stampedes when multiple calls see an expired token.
	tokenRefreshMinInterval = 30 * time.Second
)

type httpError struct {
	statusCode int
	body       []byte
}

func (e *httpError) Error() string {
	if len(e.body) == 0 {
		return fmt.Sprintf("request failed: status=%d", e.statusCode)
	}
	return fmt.Sprintf("request failed: status=%d body=%s", e.statusCode, string(e.body))
}

// refreshTokenIfNeeded refreshes the JWT using the master secret.
//
// This implements "Option A" rotation: re-auth using /v1/auth/challenge and
// /v1/auth. It does not require a refresh-token subsystem.
//
// Must be called from the SDK dispatch queue.
func (c *Client) refreshTokenIfNeeded(force bool) error {
	c.mu.Lock()
	token := strings.TrimSpace(c.token)
	master := append([]byte(nil), c.masterSecret...)
	lastRefresh := c.lastTokenRefreshAt
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("token not set")
	}
	if len(master) == 0 {
		// Without the master key we can't re-auth. Let the server reject if needed.
		return nil
	}

	expiring, err := isTokenExpiringSoon(token, tokenRefreshWindow)
	if err != nil {
		return err
	}
	if !force && !expiring {
		return nil
	}
	if !force && !lastRefresh.IsZero() && time.Since(lastRefresh) < tokenRefreshMinInterval {
		return nil
	}

	// Re-auth using deterministic identity derived from the master secret.
	masterB64 := base64.StdEncoding.EncodeToString(master)
	newToken, err := c.authWithMasterKey(masterB64)
	if err != nil {
		return err
	}
	if strings.TrimSpace(newToken) == "" {
		return fmt.Errorf("empty token returned from auth")
	}

	c.mu.Lock()
	c.token = newToken
	c.lastTokenRefreshAt = time.Now()
	c.mu.Unlock()

	return nil
}

// SetDebug enables debug logging for underlying sockets.
func (c *Client) SetDebug(enabled bool) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.debug = enabled
		if c.userSocket != nil {
			c.userSocket.SetDebug(enabled)
		}
		return nil, nil
	})
}

// SetToken configures the auth token.
func (c *Client) SetToken(token string) {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.setToken(token)
		return nil, nil
	})
}

// setToken updates the auth token without leaving the SDK dispatch queue.
func (c *Client) setToken(token string) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetToken", r)
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// SetLogDirectory configures the log directory for SDK logs.
func (c *Client) SetLogDirectory(path string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setLogDirectory(path)
	})
	return err
}

// setLogDirectory configures the SDK log directory without leaving the dispatch queue.
func (c *Client) setLogDirectory(path string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetLogDirectory", r)
		}
	}()
	return sdkLogs.setDir(path)
}

// LogLine forwards a log line into the SDK log buffer.
func (c *Client) LogLine(line string) {
	_ = c.dispatch.do(func() { c.logLine(line) })
}

// logLine writes a log line without leaving the dispatch queue.
func (c *Client) logLine(line string) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("LogLine", r)
		}
	}()
	logLine(line)
}

// SetMasterKeyBase64 sets the master key from base64.
func (c *Client) SetMasterKeyBase64(keyB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setMasterKeyBase64(keyB64)
	})
	return err
}

// setMasterKeyBase64 stores the master key without leaving the dispatch queue.
func (c *Client) setMasterKeyBase64(keyB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetMasterKeyBase64", r)
		}
	}()
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode master key: %w", err)
	}
	if len(raw) != masterKeyBytes {
		return fmt.Errorf("master key must be %d bytes, got %d", masterKeyBytes, len(raw))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.masterSecret = raw
	return nil
}

// SetSessionDataKey stores a raw data encryption key (base64).
func (c *Client) SetSessionDataKey(sessionID, keyB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setSessionDataKey(sessionID, keyB64)
	})
	return err
}

// setSessionDataKey stores the session data key without leaving the dispatch queue.
func (c *Client) setSessionDataKey(sessionID, keyB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetSessionDataKey", r)
		}
	}()
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode data key: %w", err)
	}
	if len(raw) != masterKeyBytes {
		return fmt.Errorf("data key must be %d bytes, got %d", masterKeyBytes, len(raw))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataKeys[sessionID] = raw
	return nil
}

// SetEncryptedSessionDataKey decrypts and stores a session data key.
func (c *Client) SetEncryptedSessionDataKey(sessionID, encryptedB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.setEncryptedSessionDataKey(sessionID, encryptedB64)
	})
	return err
}

// setEncryptedSessionDataKey decrypts and stores a session data key without
// leaving the dispatch queue.
func (c *Client) setEncryptedSessionDataKey(sessionID, encryptedB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SetEncryptedSessionDataKey", r)
		}
	}()
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
		if decodeErr != nil || len(raw) != masterKeyBytes {
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
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.connect()
	})
	return err
}

// connect establishes the websocket connection without leaving the dispatch queue.
func (c *Client) connect() error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("Connect", r)
		}
	}()

	// Proactively refresh the token if it's expired/near expiry.
	_ = c.refreshTokenIfNeeded(false)

	c.mu.Lock()
	token := c.token
	debug := c.debug
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("token not set")
	}

	socket := websocket.NewUserClient(c.serverURL, token, websocket.TransportWebSocket, debug)
	socket.On(websocket.EventUpdate, c.handleUpdateQueued)
	socket.On(websocket.EventEphemeral, c.handleEphemeralQueued)

	if err := socket.Connect(); err != nil {
		c.emitError(fmt.Sprintf("connect failed: %v", err))
		// If we might be failing due to an expired token, try one forced refresh.
		if refreshErr := c.refreshTokenIfNeeded(true); refreshErr == nil {
			c.mu.Lock()
			token = c.token
			c.mu.Unlock()
			socket = websocket.NewUserClient(c.serverURL, token, websocket.TransportWebSocket, debug)
			socket.On(websocket.EventUpdate, c.handleUpdateQueued)
			socket.On(websocket.EventEphemeral, c.handleEphemeralQueued)
			if err2 := socket.Connect(); err2 == nil && socket.WaitForConnect(userSocketConnectTimeout) {
				goto connected
			}
		}
		return err
	}
	if !socket.WaitForConnect(userSocketConnectTimeout) {
		err := fmt.Errorf("connect timeout")
		c.emitError(err.Error())
		// If we might be failing due to an expired token, try one forced refresh.
		if refreshErr := c.refreshTokenIfNeeded(true); refreshErr == nil {
			_ = socket.Close()
			c.mu.Lock()
			token = c.token
			c.mu.Unlock()
			socket = websocket.NewUserClient(c.serverURL, token, websocket.TransportWebSocket, debug)
			socket.On(websocket.EventUpdate, c.handleUpdateQueued)
			socket.On(websocket.EventEphemeral, c.handleEphemeralQueued)
			if err2 := socket.Connect(); err2 == nil && socket.WaitForConnect(userSocketConnectTimeout) {
				goto connected
			}
		}
		return err
	}

connected:
	c.mu.Lock()
	c.userSocket = socket
	listener := c.listener
	c.mu.Unlock()

	if listener != nil {
		_ = c.callbacks.do(func() { listener.OnConnected() })
	}

	// Proactively hydrate per-session data keys so encrypted message updates can be
	// decrypted immediately, even if the app hasn't called ListSessions yet.
	_ = c.dispatch.do(func() { _, _ = c.listSessions() })
	return nil
}

// Disconnect closes the websocket connection.
func (c *Client) Disconnect() {
	_, _ = c.dispatch.call(func() (interface{}, error) {
		c.disconnect()
		return nil, nil
	})
}

// disconnect closes the websocket connection without leaving the dispatch queue.
func (c *Client) disconnect() {
	defer func() {
		if r := recover(); r != nil {
			logPanic("Disconnect", r)
		}
	}()
	c.mu.Lock()
	socket := c.userSocket
	listener := c.listener
	c.userSocket = nil
	c.mu.Unlock()

	if socket != nil {
		_ = socket.Close()
	}
	if listener != nil {
		_ = c.callbacks.do(func() { listener.OnDisconnected("closed") })
	}
}

// SendMessage encrypts and sends a raw record JSON payload to a session.
func (c *Client) SendMessage(sessionID string, rawRecordJSON string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.sendMessageWithLocalID(sessionID, "", rawRecordJSON)
	})
	return err
}

// SendMessageWithLocalID encrypts and sends a raw record JSON payload to a session with a
// client-generated idempotency key ("localId"). The server will echo the localId back in
// message updates so callers can reconcile optimistic UI entries.
func (c *Client) SendMessageWithLocalID(sessionID string, localID string, rawRecordJSON string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.sendMessageWithLocalID(sessionID, localID, rawRecordJSON)
	})
	return err
}

// CallRPCBuffer issues an RPC call via the user-scoped websocket connection and
// returns the raw ACK payload as JSON.
func (c *Client) CallRPCBuffer(method string, paramsJSON string) (*Buffer, error) {
	resp, err := c.callRPCDispatch(method, paramsJSON)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// callRPCDispatch runs callRPC on the SDK dispatch queue.
func (c *Client) callRPCDispatch(method string, paramsJSON string) (string, error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.callRPC(method, paramsJSON)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "{}", nil
	}
	if s, ok := value.(string); ok {
		return s, nil
	}
	return "{}", nil
}

// callRPC emits an RPC call over Socket.IO and returns the ACK as JSON.
func (c *Client) callRPC(method string, paramsJSON string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("CallRPC", r)
		}
	}()
	c.mu.Lock()
	socket := c.userSocket
	c.mu.Unlock()

	if socket == nil {
		return "", fmt.Errorf("not connected")
	}

	// Enforce control FSM for the control-switch RPC and keep the mobile UI dumb.
	// Phone can only take control if the CLI is online and currently in local mode.
	if strings.HasSuffix(method, ":switch") {
		sessionID := strings.TrimSuffix(method, ":switch")
		var params map[string]any
		_ = json.Unmarshal([]byte(paramsJSON), &params)
		mode, _ := params["mode"].(string)

		// Mark the session as "switching" immediately so UI layers can show a
		// spinner/disabled controls without implementing transition logic.
		if mode == "remote" || mode == "local" {
			transition := "to-" + mode
			// Best-effort: only mark if we have a recent session snapshot.
			if state, ok, _ := c.ensureSessionFSM(sessionID); ok {
				c.mu.Lock()
				prev := state
				prev.switching = true
				prev.transition = transition
				prev.switchingAt = time.Now().UnixMilli()
				c.sessionFSM[sessionID] = prev
				c.mu.Unlock()

				// Emit a synthetic session-ui update so clients can re-render
				// immediately without polling ListSessions.
				fsm, ui := deriveSessionUI(time.Now().UnixMilli(), prev.connected, prev.active, "", &prev)
				uiJSON := sessionUIJSON(ui)
				c.mu.Lock()
				prevCache := c.sessionFSM[sessionID]
				if prevCache.uiJSON != uiJSON {
					fsm.uiJSON = uiJSON
					c.sessionFSM[sessionID] = fsm
					c.mu.Unlock()
					c.emitUpdate(sessionID, buildSessionUIUpdate(time.Now().UnixMilli(), sessionID, ui))
				} else {
					c.mu.Unlock()
				}
			}
		}

		if mode == "remote" {
			state, ok, err := c.ensureSessionFSM(sessionID)
			if err != nil {
				return "", err
			}
			if ok {
				// Allow idempotent "take control" when already remote.
				if state.state == "remote" {
					// ok
				} else if state.state != "local" {
					return "", fmt.Errorf("cannot take control: session %s", state.state)
				}
			}
		}
	}

	resp, err := socket.EmitWithAck("rpc-call", wire.RPCCallPayload{
		Method: method,
		Params: paramsJSON,
	}, rpcAckTimeout)
	if err != nil {
		// Clear switching on failure so UI doesn't get stuck.
		if strings.HasSuffix(method, ":switch") {
			sessionID := strings.TrimSuffix(method, ":switch")
			c.clearSessionSwitching(sessionID)
		}
		return "", err
	}
	if resp == nil {
		if strings.HasSuffix(method, ":switch") {
			sessionID := strings.TrimSuffix(method, ":switch")
			c.clearSessionSwitching(sessionID)
		}
		return "", fmt.Errorf("missing rpc ack")
	}

	if ok, _ := resp["ok"].(bool); !ok {
		if strings.HasSuffix(method, ":switch") {
			sessionID := strings.TrimSuffix(method, ":switch")
			c.clearSessionSwitching(sessionID)
		}
		if msg, _ := resp["error"].(string); msg != "" {
			return "", fmt.Errorf("rpc call failed: %s", msg)
		}
		return "", fmt.Errorf("rpc call failed")
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		return "{}", nil
	}

	// Update cached control FSM after a successful switch call so the UI can reflect
	// control immediately, even if agentState propagation is delayed or legacy-encrypted.
	if strings.HasSuffix(method, ":switch") {
		sessionID := strings.TrimSuffix(method, ":switch")
		mode := ""
		if result, _ := resp["result"].(map[string]any); result != nil {
			mode, _ = result["mode"].(string)
		}
		if mode == "local" || mode == "remote" {
			// Preserve active/connected bits if we have them, defaulting to optimistic "online".
			c.mu.Lock()
			prev := c.sessionFSM[sessionID]
			active := prev.active
			connected := prev.connected
			if prev.state == "" {
				active = true
				connected = true
			}
			controlledByUser := mode == "local"
			next := computeSessionFSM(connected, active, controlledByUser)
			next.updatedAt = prev.updatedAt
			next.fetchedAt = time.Now().UnixMilli()
			next.switching = false
			next.transition = ""
			next.switchingAt = 0
			_, ui := deriveSessionUI(time.Now().UnixMilli(), connected, active, "", &next)
			next.uiJSON = sessionUIJSON(ui)
			c.sessionFSM[sessionID] = next
			c.mu.Unlock()

			// Emit a synthetic session-ui update for immediate client re-rendering.
			c.emitUpdate(sessionID, buildSessionUIUpdate(time.Now().UnixMilli(), sessionID, ui))
		}
	}

	return string(encoded), nil
}

// sendMessageWithLocalID is the underlying implementation for SendMessage and
// SendMessageWithLocalID.
func (c *Client) sendMessageWithLocalID(sessionID string, localID string, rawRecordJSON string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("SendMessage", r)
		}
	}()
	c.mu.Lock()
	socket := c.userSocket
	state := c.sessionFSM[sessionID]
	c.mu.Unlock()

	if socket == nil {
		return fmt.Errorf("not connected")
	}

	// Enforce phone-send rules:
	// - phone can only send while the session is online and phone-controlled (remote mode)
	if state.state != "remote" {
		// Refresh state once if we don't have a current snapshot.
		refreshed, ok, err := c.ensureSessionFSM(sessionID)
		if err != nil {
			return err
		}
		if ok && refreshed.state != "remote" {
			return fmt.Errorf("cannot send: session %s", refreshed.state)
		}
		if !ok {
			return fmt.Errorf("cannot send: unknown session")
		}
	}

	encrypted, err := c.encryptPayload(sessionID, []byte(rawRecordJSON))
	if err != nil {
		return err
	}
	if localID != "" {
		return socket.SendMessageWithLocalID(sessionID, encrypted, localID)
	}
	return socket.SendMessage(sessionID, encrypted)
}

// ListSessionsBuffer returns ListSessions JSON as a gomobile-safe Buffer.
func (c *Client) ListSessionsBuffer() (*Buffer, error) {
	resp, err := c.listSessionsDispatch()
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// listSessionsDispatch runs listSessions on the SDK dispatch queue.
func (c *Client) listSessionsDispatch() (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.listSessions()
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

// listSessions fetches the current session list, caches keys, and injects UI state.
func (c *Client) listSessions() (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ListSessions", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	respBody, err := c.doRequest("GET", "/v1/sessions", nil)
	if err != nil {
		return "", err
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(respBody, &decoded); err == nil {
		if sessions, ok := decoded["sessions"].([]interface{}); ok {
			c.mu.Lock()
			socket := c.userSocket
			cachedFSM := make(map[string]sessionFSMState, len(c.sessionFSM))
			for k, v := range c.sessionFSM {
				cachedFSM[k] = v
			}
			c.mu.Unlock()
			connected := socket != nil && socket.IsConnected()
			now := time.Now().UnixMilli()

			for _, item := range sessions {
				session, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				sessionID, _ := session["id"].(string)
				dataKeyB64, _ := session["dataEncryptionKey"].(string)
				if sessionID != "" && dataKeyB64 != "" {
					_ = c.setEncryptedSessionDataKey(sessionID, dataKeyB64)
				}
				metadataB64, _ := session["metadata"].(string)
				if metadataB64 != "" {
					if decodedJSON, err := decodeBase64JSONString(metadataB64); err == nil {
						session["metadata"] = decodedJSON
					}
				}

				agentState, _ := session["agentState"].(string)
				active, _ := session["active"].(bool)
				updatedAt := int64(0)
				switch v := session["updatedAt"].(type) {
				case float64:
					updatedAt = int64(v)
				case int64:
					updatedAt = v
				case int:
					updatedAt = int64(v)
				}
				var cached *sessionFSMState
				if prev, ok := cachedFSM[sessionID]; ok {
					tmp := prev
					cached = &tmp
				}
				fsm, ui := deriveSessionUI(now, connected, active, agentState, cached)
				fsm.updatedAt = updatedAt

				// Inject a derived UI state so the iOS app can be a pure view layer.
				session["ui"] = ui

				if sessionID != "" {
					c.mu.Lock()
					fsm.uiJSON = sessionUIJSON(ui)
					c.sessionFSM[sessionID] = fsm
					c.mu.Unlock()
				}
			}
		}
		if encoded, err := json.Marshal(decoded); err == nil {
			return string(encoded), nil
		}
	}

	return string(respBody), nil
}

// ListTerminalsBuffer returns ListTerminals JSON as a gomobile-safe Buffer.
func (c *Client) ListTerminalsBuffer() (*Buffer, error) {
	resp, err := c.listTerminalsDispatch()
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// listTerminalsDispatch runs listTerminals on the SDK dispatch queue.
func (c *Client) listTerminalsDispatch() (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.listTerminals()
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

// listTerminals fetches terminals and best-effort decrypts encrypted fields.
func (c *Client) listTerminals() (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ListTerminals", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	respBody, err := c.doRequest("GET", "/v1/terminals", nil)
	if err != nil {
		return "", err
	}

	var decoded []interface{}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return string(respBody), nil
	}

	for _, item := range decoded {
		terminal, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		metadataB64, _ := terminal["metadata"].(string)
		if metadataB64 != "" {
			if decryptedJSON, err := c.decryptTerminalString(metadataB64); err == nil {
				terminal["metadata"] = decryptedJSON
			}
		}
		daemonStateB64, _ := terminal["daemonState"].(string)
		if daemonStateB64 != "" {
			if decryptedJSON, err := c.decryptTerminalString(daemonStateB64); err == nil {
				terminal["daemonState"] = decryptedJSON
			}
		}
	}

	encoded, err := json.Marshal(decoded)
	if err != nil {
		return string(respBody), nil
	}
	return string(encoded), nil
}

// DeleteTerminalBuffer deletes a terminal by id and returns the response JSON as
// a gomobile-safe Buffer.
func (c *Client) DeleteTerminalBuffer(terminalID string) (*Buffer, error) {
	resp, err := c.deleteTerminalDispatch(terminalID)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// deleteTerminalDispatch runs deleteTerminal on the SDK dispatch queue.
func (c *Client) deleteTerminalDispatch(terminalID string) (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.deleteTerminal(terminalID)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

// deleteTerminal issues HTTP DELETE /v1/terminals/:id.
func (c *Client) deleteTerminal(terminalID string) (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("DeleteTerminal", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return "", fmt.Errorf("terminal id is required")
	}
	endpoint := fmt.Sprintf("/v1/terminals/%s", url.PathEscape(terminalID))
	respBody, err := c.doRequest("DELETE", endpoint, nil)
	if err != nil {
		return "", err
	}
	return string(respBody), nil
}

// decodeBase64JSONString decodes a base64(JSON) string into a JSON string.
func decodeBase64JSONString(payload string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("decoded payload is not valid JSON")
	}
	return string(raw), nil
}

// decryptTerminalString decrypts an AES-GCM encrypted JSON payload using the
// master key and returns a JSON string.
func (c *Client) decryptTerminalString(payload string) (string, error) {
	c.mu.Lock()
	secret := make([]byte, len(c.masterSecret))
	copy(secret, c.masterSecret)
	c.mu.Unlock()
	if len(secret) != masterKeyBytes {
		return "", fmt.Errorf("master key not configured")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	if len(raw) < aesGCMVersionBytes+aesGCMNonceBytes+aesGCMTagBytes || raw[0] != encryptedPayloadFormatV0 {
		return "", fmt.Errorf("unsupported encrypted payload format")
	}
	var decoded json.RawMessage
	if err := crypto.DecryptWithDataKey(raw, secret, &decoded); err != nil {
		return "", fmt.Errorf("decrypt payload: %w", err)
	}
	if !json.Valid(decoded) {
		return "", fmt.Errorf("decrypted payload is not valid JSON")
	}
	return string(decoded), nil
}

// GetSessionMessagesBuffer returns GetSessionMessages JSON as a gomobile-safe Buffer.
func (c *Client) GetSessionMessagesBuffer(sessionID string, limit int) (*Buffer, error) {
	resp, err := c.getSessionMessagesDispatch(sessionID, limit)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// GetSessionMessagesPageBuffer returns a page of session messages for infinite scroll.
//
// If beforeSeq > 0, it fetches messages with seq < beforeSeq.
// If beforeSeq <= 0, it fetches the most recent page.
func (c *Client) GetSessionMessagesPageBuffer(sessionID string, limit int, beforeSeq int64) (*Buffer, error) {
	resp, err := c.getSessionMessagesPageDispatch(sessionID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(resp), nil
}

// getSessionMessagesDispatch runs getSessionMessages on the SDK dispatch queue.
func (c *Client) getSessionMessagesDispatch(sessionID string, limit int) (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.getSessionMessages(sessionID, limit)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

// getSessionMessagesPageDispatch runs getSessionMessagesPage on the SDK dispatch queue.
func (c *Client) getSessionMessagesPageDispatch(sessionID string, limit int, beforeSeq int64) (resp string, err error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.getSessionMessagesPage(sessionID, limit, beforeSeq)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

// getSessionMessages returns the newest page of messages for a session.
func (c *Client) getSessionMessages(sessionID string, limit int) (resp string, err error) {
	return c.getSessionMessagesPage(sessionID, limit, 0)
}

// getSessionMessagesPage returns a paginated slice of messages for a session.
func (c *Client) getSessionMessagesPage(sessionID string, limit int, beforeSeq int64) (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("GetSessionMessages", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	if sessionID == "" {
		return "", fmt.Errorf("sessionID required")
	}
	logLine(fmt.Sprintf("GetSessionMessages sessionID=%s limit=%d beforeSeq=%d", sessionID, limit, beforeSeq))

	endpoint := fmt.Sprintf("/v1/sessions/%s/messages", url.PathEscape(sessionID))
	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if beforeSeq > 0 {
		values.Set("beforeSeq", strconv.FormatInt(beforeSeq, 10))
	}
	if len(values) > 0 {
		endpoint = endpoint + "?" + values.Encode()
	}

	respBody, err := c.doRequest("GET", endpoint, nil)
	if err != nil {
		logLine(fmt.Sprintf("GetSessionMessages request error: %v", err))
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

// handleEphemeralQueued enqueues a websocket ephemeral event onto the dispatch queue.
func (c *Client) handleEphemeralQueued(data map[string]interface{}) {
	_ = c.dispatch.do(func() { c.handleEphemeral(data) })
}

// handleUpdateQueued enqueues a websocket update event onto the dispatch queue.
func (c *Client) handleUpdateQueued(data map[string]interface{}) {
	_ = c.dispatch.do(func() { c.handleUpdate(data) })
}

// handleEphemeral forwards an ephemeral payload to listeners.
func (c *Client) handleEphemeral(data map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("handleEphemeral", r)
		}
	}()
	c.emitUpdate("", data)
}

// handleUpdate decrypts message payloads and maintains derived UI state before emitting.
func (c *Client) handleUpdate(data map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("handleUpdate", r)
		}
	}()
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
				if err != nil {
					// Likely missing the per-session data key (race with initial ListSessions).
					// Refresh sessions (debounced) and retry once.
					c.maybeHydrateSessionKeys()
					if retry, retryErr := c.decryptEnvelope(sessionID, content); retryErr == nil && retry != nil {
						msg["content"] = retry
					}
				} else if decrypted != nil {
					msg["content"] = decrypted
				}
			}
		}
	case "new-session":
		sessionID, _ = body["id"].(string)
		if dataKey, ok := body["dataEncryptionKey"].(string); ok && dataKey != "" {
			_ = c.setEncryptedSessionDataKey(sessionID, dataKey)
		}
	case "update-session":
		sessionID, _ = body["id"].(string)
		// Keep the derived session control FSM up to date without requiring the UI
		// to poll ListSessions.
		//
		// The server emits agentState changes as part of update-session events, so
		// we can update our per-session cached UI state immediately.
		if sessionID != "" {
			if agentState, ok := body["agentState"].(map[string]interface{}); ok {
				if value, _ := agentState["value"].(string); value != "" {
					c.applyAgentStateToSessionFSM(sessionID, value)
				}
			}
		}
	}

	c.emitUpdate(sessionID, data)
}

// applyAgentStateToSessionFSM updates the cached session FSM from agentState.
func (c *Client) applyAgentStateToSessionFSM(sessionID string, agentState string) {
	now := time.Now().UnixMilli()

	c.mu.Lock()
	prev := c.sessionFSM[sessionID]
	socket := c.userSocket
	c.mu.Unlock()

	connected := prev.connected
	if socket != nil {
		connected = socket.IsConnected()
	}
	active := prev.active
	if prev.state == "" {
		// If we don't have a previous snapshot, receiving an update implies the
		// session is at least "online-ish".
		active = true
		connected = true
	}

	fsm, ui := deriveSessionUI(now, connected, active, agentState, &prev)
	fsm.updatedAt = prev.updatedAt
	fsm.switching = false
	fsm.transition = ""
	fsm.switchingAt = 0
	fsm.uiJSON = sessionUIJSON(ui)

	c.mu.Lock()
	prevUI := prev.uiJSON
	c.sessionFSM[sessionID] = fsm
	c.mu.Unlock()

	if prevUI != fsm.uiJSON {
		c.emitUpdate(sessionID, buildSessionUIUpdate(now, sessionID, ui))
	}
}

// maybeHydrateSessionKeys refreshes the session list to hydrate data keys, but
// throttles the refresh to avoid spamming the server.
func (c *Client) maybeHydrateSessionKeys() {
	now := time.Now().UnixMilli()

	c.mu.Lock()
	last := c.lastSessionHydrateAt
	if last != 0 && time.Duration(now-last)*time.Millisecond < sessionKeyHydrateMinInterval {
		c.mu.Unlock()
		return
	}
	c.lastSessionHydrateAt = now
	c.mu.Unlock()

	// We are already executing on the SDK dispatch queue when called from handleUpdate.
	// Calling listSessions directly avoids re-entrancy into the dispatcher.
	_, _ = c.listSessions()
}

// emitUpdate sends a server (or synthetic) update payload to the listener.
func (c *Client) emitUpdate(sessionID string, payload map[string]interface{}) {
	listener := c.getListener()
	if listener == nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		_ = c.callbacks.do(func() { listener.OnError(fmt.Sprintf("encode update: %v", err)) })
		return
	}
	_ = c.callbacks.do(func() { listener.OnUpdate(sessionID, string(encoded)) })
}

// emitError sends an error string to the listener.
func (c *Client) emitError(message string) {
	listener := c.getListener()
	if listener != nil {
		_ = c.callbacks.do(func() { listener.OnError(message) })
	}
}

// getListener reads the current listener under lock.
func (c *Client) getListener() Listener {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listener
}

// encryptPayload encrypts a raw record JSON string for sending over the wire.
func (c *Client) encryptPayload(sessionID string, data []byte) (string, error) {
	c.mu.Lock()
	dataKey := c.dataKeys[sessionID]
	c.mu.Unlock()

	if len(dataKey) != masterKeyBytes {
		return "", fmt.Errorf("session data key not set (call ListSessions or SetSessionDataKey)")
	}
	encrypted, err := crypto.EncryptWithDataKey(json.RawMessage(data), dataKey)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// decryptEnvelope decrypts a content envelope in-place if it is encrypted.
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

// decryptPayload decrypts an encrypted wire payload using the session data key.
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
	c.mu.Unlock()

	if len(dataKey) != masterKeyBytes {
		return nil, fmt.Errorf("session data key not set (call ListSessions or SetSessionDataKey)")
	}

	// AES-GCM format: [version(1)] [nonce(12)] [ciphertext+tag(16+)].
	if encrypted[0] != encryptedPayloadFormatV0 || len(encrypted) < aesGCMVersionBytes+aesGCMNonceBytes+aesGCMTagBytes {
		return nil, fmt.Errorf("unsupported encrypted payload format")
	}

	var result json.RawMessage
	if err := crypto.DecryptWithDataKey(encrypted, dataKey, &result); err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// decodeBase64URL decodes base64 in URL-safe variants and tolerates padding.
func decodeBase64URL(input string) ([]byte, error) {
	if input == "" {
		return nil, fmt.Errorf("empty base64url")
	}
	if data, err := base64.URLEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.RawURLEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.StdEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(input)
}

// decodeBase64Any decodes base64 in either standard or URL-safe variants.
func decodeBase64Any(input string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(input); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(input)
}

// doRequest performs a single HTTP request against the configured server URL.
func (c *Client) doRequest(method, path string, body []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("doRequest", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	// Best-effort proactive refresh for authenticated endpoints.
	if !strings.HasPrefix(path, "/v1/auth") {
		_ = c.refreshTokenIfNeeded(false)
	}

	c.mu.Lock()
	token := strings.TrimSpace(c.token)
	baseURL := c.serverURL
	client := c.httpClient
	c.mu.Unlock()

	if baseURL == "" {
		return nil, fmt.Errorf("server URL not set")
	}

	doOnce := func(token string) (int, []byte, error) {
		fullURL := fmt.Sprintf("%s%s", baseURL, path)
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}

		req, err := http.NewRequest(method, fullURL, reader)
		if err != nil {
			return 0, nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		httpResp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer httpResp.Body.Close()

		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return 0, nil, err
		}
		return httpResp.StatusCode, respBody, nil
	}

	status, respBody, err := doOnce(token)
	if err != nil {
		return nil, err
	}

	// If auth failed, try a forced refresh once and retry.
	if status == http.StatusUnauthorized && !strings.HasPrefix(path, "/v1/auth") {
		if refreshErr := c.refreshTokenIfNeeded(true); refreshErr == nil {
			c.mu.Lock()
			token = strings.TrimSpace(c.token)
			c.mu.Unlock()
			status, respBody, err = doOnce(token)
			if err != nil {
				return nil, err
			}
		}
	}

	if status < httpSuccessMin || status >= httpSuccessMaxExclusive {
		return nil, &httpError{statusCode: status, body: respBody}
	}

	return respBody, nil
}
