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
// Accepts delight://terminal?<pubkey> and happy://terminal?<pubkey>.
func ParseTerminalURL(qrURL string) (string, error) {
	return parseTerminalURL(qrURL)
}

// AuthWithKeyPair performs challenge-response auth and stores the token.
func (c *Client) AuthWithKeyPair(publicKeyB64, privateKeyB64 string) (string, error) {
	return c.authWithKeyPairDispatch(publicKeyB64, privateKeyB64)
}

// ListSessions fetches sessions and caches data keys. Returns JSON response.
func (c *Client) ListSessions() (string, error) {
	return c.listSessionsDispatch()
}

// ListMachines fetches machines and decrypts metadata/daemon state when possible.
func (c *Client) ListMachines() (string, error) {
	return c.listMachinesDispatch()
}

// GetSessionMessages fetches session messages and decrypts message content.
func (c *Client) GetSessionMessages(sessionID string, limit int) (string, error) {
	return c.getSessionMessagesDispatch(sessionID, limit)
}
