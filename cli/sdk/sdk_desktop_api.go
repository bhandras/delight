//go:build !gomobile

package sdk

// Desktop-only wrappers around internal implementations.
//
// Rationale:
// gomobile/cgo generates packed argument/return structs for exported symbols.
// Returning Go pointer-bearing values (notably string/[]byte) across that
// boundary can crash at runtime (e.g. `bulkBarrierPreWrite: unaligned arguments`).
//
// The mobile surface should use Buffer-based APIs instead.

// GenerateMasterKeyBase64 creates a new 32-byte master key (base64).
func GenerateMasterKeyBase64() (string, error) {
	return generateMasterKeyBase64()
}

// ParseTerminalURL extracts the terminal public key from a QR URL.
// Accepts delight://terminal?<pubkey>.
func ParseTerminalURL(qrURL string) (string, error) {
	return parseTerminalURL(qrURL)
}

// AuthWithKeyPair performs challenge-response auth and stores the token.
func (c *Client) AuthWithKeyPair(publicKeyB64, privateKeyB64 string) (string, error) {
	return c.authWithKeyPairDispatch(publicKeyB64, privateKeyB64)
}

// AuthWithMasterKeyBase64 performs challenge-response auth using a
// deterministic Ed25519 key derived from the master key.
func (c *Client) AuthWithMasterKeyBase64(masterKeyB64 string) (string, error) {
	return c.authWithMasterKeyDispatch(masterKeyB64)
}

// ListSessions fetches sessions and caches data keys. Returns JSON response.
func (c *Client) ListSessions() (string, error) {
	return c.listSessionsDispatch()
}

// ListTerminals fetches terminals and decrypts metadata/daemon state when possible.
func (c *Client) ListTerminals() (string, error) {
	return c.listTerminalsDispatch()
}

// GetSessionMessages fetches session messages and decrypts message content.
func (c *Client) GetSessionMessages(sessionID string, limit int) (string, error) {
	return c.getSessionMessagesDispatch(sessionID, limit)
}

// GetSessionMessagesPage fetches a paginated session message page.
func (c *Client) GetSessionMessagesPage(sessionID string, limit int, beforeSeq int64) (string, error) {
	return c.getSessionMessagesPageDispatch(sessionID, limit, beforeSeq)
}

// DeleteTerminal deletes a terminal by id and returns JSON response.
func (c *Client) DeleteTerminal(terminalID string) (string, error) {
	return c.deleteTerminalDispatch(terminalID)
}

// CallRPC issues a websocket RPC method call and returns the raw ACK payload.
func (c *Client) CallRPC(method string, paramsJSON string) (string, error) {
	return c.callRPCDispatch(method, paramsJSON)
}
