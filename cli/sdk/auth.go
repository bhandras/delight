package sdk

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/bhandras/delight/cli/internal/crypto"
)

const (
	// authChallengeText is the fixed challenge string used for keypair auth.
	// The server verifies the signature for this challenge.
	authChallengeText = "delight-auth-challenge"

	// masterKeyBytes is the expected raw master key length.
	masterKeyBytes = 32
)

// generateMasterKeyBase64 generates a random 32-byte master key (base64).
func generateMasterKeyBase64() (string, error) {
	secret := make([]byte, masterKeyBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate master key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(secret), nil
}

// GenerateMasterKeyBase64Buffer returns a gomobile-safe Buffer containing a new
// 32-byte master key (base64).
func GenerateMasterKeyBase64Buffer() (*Buffer, error) {
	key, err := generateMasterKeyBase64()
	if err != nil {
		return nil, err
	}
	return newBufferFromString(key), nil
}

// generateEd25519KeyPairBase64 returns a new keypair as base64 strings.
func generateEd25519KeyPairBase64() (publicKeyB64 string, privateKeyB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 keypair: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}

// GenerateEd25519KeyPairBuffers returns a new keypair as base64 Buffers
// (gomobile-safe).
func GenerateEd25519KeyPairBuffers() (*KeyPairBuffers, error) {
	pub, priv, err := generateEd25519KeyPairBase64()
	if err != nil {
		return nil, err
	}
	return newKeyPairBuffers(pub, priv), nil
}

// AuthWithKeyPairBuffer authenticates via Ed25519 keypair and returns the token
// in a gomobile-safe Buffer.
func (c *Client) AuthWithKeyPairBuffer(publicKeyB64, privateKeyB64 string) (*Buffer, error) {
	token, err := c.authWithKeyPairDispatch(publicKeyB64, privateKeyB64)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(token), nil
}

// authWithKeyPairDispatch runs the keypair auth flow on the SDK dispatch queue.
func (c *Client) authWithKeyPairDispatch(publicKeyB64, privateKeyB64 string) (string, error) {
	value, err := c.dispatch.call(func() (interface{}, error) {
		return c.authWithKeyPair(publicKeyB64, privateKeyB64)
	})
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}

// authWithKeyPair authenticates with the server using an Ed25519 signature.
func (c *Client) authWithKeyPair(publicKeyB64, privateKeyB64 string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("AuthWithKeyPair", r)
		}
	}()

	priv, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid private key length")
	}

	challenge := []byte(authChallengeText)
	signature := ed25519.Sign(ed25519.PrivateKey(priv), challenge)

	reqBody := map[string]string{
		"publicKey": publicKeyB64,
		"challenge": base64.StdEncoding.EncodeToString(challenge),
		"signature": base64.StdEncoding.EncodeToString(signature),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal auth request: %w", err)
	}

	respBody, err := c.doRequest("POST", "/v1/auth", body)
	if err != nil {
		return "", err
	}

	var resp struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("parse auth response: %w", err)
	}
	if !resp.Success || resp.Token == "" {
		return "", fmt.Errorf("auth failed")
	}
	c.setToken(resp.Token)
	return resp.Token, nil
}

// parseTerminalURL parses a Delight terminal QR URL and extracts the terminal
// key used to approve terminal auth.
func parseTerminalURL(qrURL string) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ParseTerminalURL", r)
		}
	}()
	parsed, err := url.Parse(qrURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "delight" && parsed.Scheme != "happy" {
		return "", fmt.Errorf("unsupported scheme: %s", parsed.Scheme)
	}
	if parsed.Host != "terminal" {
		return "", fmt.Errorf("unsupported host: %s", parsed.Host)
	}
	raw := parsed.RawQuery
	if raw == "" {
		return "", fmt.Errorf("missing public key in URL")
	}
	pubBytes, err := decodeBase64URL(raw)
	if err != nil {
		return "", fmt.Errorf("decode public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pubBytes), nil
}

// ParseTerminalURLBuffer parses a Delight terminal QR URL and returns the
// terminal key in a gomobile-safe Buffer.
func ParseTerminalURLBuffer(qrURL string) (*Buffer, error) {
	key, err := parseTerminalURL(qrURL)
	if err != nil {
		return nil, err
	}
	return newBufferFromString(key), nil
}

// ApproveTerminalAuth encrypts and submits the master key to a terminal.
func (c *Client) ApproveTerminalAuth(terminalPublicKeyB64 string, masterKeyB64 string) error {
	_, err := c.dispatch.call(func() (interface{}, error) {
		return nil, c.approveTerminalAuth(terminalPublicKeyB64, masterKeyB64)
	})
	return err
}

// approveTerminalAuth submits the encrypted master key to the server.
func (c *Client) approveTerminalAuth(terminalPublicKeyB64 string, masterKeyB64 string) error {
	defer func() {
		if r := recover(); r != nil {
			logPanic("ApproveTerminalAuth", r)
		}
	}()

	terminalPub, err := decodeBase64Any(terminalPublicKeyB64)
	if err != nil {
		return fmt.Errorf("decode terminal public key: %w", err)
	}
	if len(terminalPub) != masterKeyBytes {
		return fmt.Errorf("invalid terminal public key length")
	}
	masterKey, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return fmt.Errorf("decode master key: %w", err)
	}
	if len(masterKey) != masterKeyBytes {
		return fmt.Errorf("master key must be %d bytes", masterKeyBytes)
	}

	var terminalPubKey [masterKeyBytes]byte
	copy(terminalPubKey[:], terminalPub)

	encrypted, err := crypto.EncryptBox(masterKey, &terminalPubKey)
	if err != nil {
		return fmt.Errorf("encrypt response: %w", err)
	}

	reqBody := map[string]string{
		"publicKey": terminalPublicKeyB64,
		"response":  base64.StdEncoding.EncodeToString(encrypted),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	_, err = c.doRequest("POST", "/v1/auth/response", body)
	return err
}
