package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/shared/logger"
	qrcode "github.com/skip2/go-qrcode"
)

// AuthCommand handles the QR code authentication flow using terminal auth
// This matches the flow expected by the mobile app's useConnectTerminal.ts
func AuthCommand(cfg *config.Config) error {
	logger.Infof("Starting terminal authentication...")

	// Generate crypto_box key pair for this device
	publicKey, privateKey, err := crypto.GenerateBoxKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate key pair: %w", err)
	}

	publicKeyB64 := base64.StdEncoding.EncodeToString(publicKey[:])

	// Create auth request on server using POST /v1/auth/request (terminal auth)
	reqBody := map[string]interface{}{
		"publicKey": publicKeyB64,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	authURL := fmt.Sprintf("%s/v1/auth/request", cfg.ServerURL)
	req, err := http.NewRequest("POST", authURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("auth request failed: %s - %s", resp.Status, string(respBody))
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	state, _ := result["state"].(string)
	requestID, _ := result["id"].(string)

	if cfg.Debug {
		logger.Debugf("Auth request created: state=%s, id=%s", state, requestID)
	}

	// Build QR code data - format: delight://terminal?[base64url-encoded-public-key]
	publicKeyB64url := toBase64URL(publicKeyB64)
	qrData := fmt.Sprintf("delight://terminal?%s", publicKeyB64url)

	// Generate QR code
	logger.Infof("\nScan this QR code with the Delight mobile app to link this device:")
	printQRCode(qrData)

	// Print URL for manual authentication
	logger.Infof("\nOr manually open this URL in the Delight app:\n%s", qrData)

	if requestID != "" {
		logger.Infof("\nWaiting for approval (Request ID: %s)...", requestID)
	} else {
		logger.Infof("\nWaiting for approval...")
	}

	// Poll for response using GET /v1/auth/request/status
	token, secret, err := pollForTerminalAuth(cfg, publicKeyB64, privateKey)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	logger.Infof("✓ Authentication successful!")

	// Save credentials
	if err := saveCredentials(cfg, secret, token); err != nil {
		return err
	}

	logger.Infof("Credentials saved to: %s", cfg.DelightHome)
	return nil
}

// printQRCode prints a QR code to the terminal
func printQRCode(data string) {
	// Generate QR code
	qr, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		logger.Warnf("Failed to generate QR code: %v", err)
		logger.Infof("Auth URL: %s", data)
		return
	}

	// Print as ASCII art
	fmt.Println(qr.ToSmallString(false))
}

// pollForTerminalAuth polls the server for terminal auth approval
// Uses GET /v1/auth/request/status?publicKey=<base64> as expected by mobile app
func pollForTerminalAuth(cfg *config.Config, publicKeyB64 string, privateKey *[32]byte) (string, []byte, error) {
	// URL encode the public key for query parameter (handles + and / in base64)
	baseURL := fmt.Sprintf("%s/v1/auth/request/status", cfg.ServerURL)
	pollURL := fmt.Sprintf("%s?publicKey=%s", baseURL, url.QueryEscape(publicKeyB64))
	client := &http.Client{Timeout: 5 * time.Second}

	// Poll every 2 seconds for up to 5 minutes
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return "", nil, fmt.Errorf("authentication timeout (5 minutes)")

		case <-ticker.C:
			req, err := http.NewRequest("GET", pollURL, nil)
			if err != nil {
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				if cfg.Debug {
					logger.Debugf("Poll error: %v", err)
				}
				continue
			}

			respBody, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				continue
			}

			if cfg.Debug {
				logger.Tracef("Poll response: status=%d body=%s", resp.StatusCode, string(respBody))
			}

			if resp.StatusCode == http.StatusNotFound {
				// Request not found or expired, keep polling
				continue
			}

			if resp.StatusCode != http.StatusOK {
				continue
			}

			var result struct {
				Status   string  `json:"status"`   // "pending" or "authorized"
				Token    *string `json:"token"`    // JWT token when authorized
				Response *string `json:"response"` // Encrypted secret when authorized
			}
			if err := json.Unmarshal(respBody, &result); err != nil {
				if cfg.Debug {
					logger.Debugf("Failed to parse response: %v", err)
				}
				continue
			}

			// Check if approved
			if result.Status == "authorized" {
				if result.Token == nil || result.Response == nil {
					return "", nil, fmt.Errorf("missing token or response in authorized status")
				}

				if cfg.Debug {
					logger.Debugf("Authorization received")
				}

				// Decrypt the secret
				encryptedSecret, err := base64.StdEncoding.DecodeString(*result.Response)
				if err != nil {
					return "", nil, fmt.Errorf("failed to decode encrypted secret: %w", err)
				}

				// Decrypt the response - handles both V1 and V2 formats
				secret, isV2, err := crypto.DecryptAuthResponse(encryptedSecret, privateKey)
				if err != nil {
					return "", nil, fmt.Errorf("failed to decrypt secret: %w", err)
				}

				if cfg.Debug {
					logger.Debugf("Decrypted secret: isV2=%v, len=%d", isV2, len(secret))
				}

				return *result.Token, secret, nil
			}
		}
	}
}

// saveCredentials saves the account secret and access token
func saveCredentials(cfg *config.Config, secret []byte, token string) error {
	// Ensure happy home directory exists
	if err := os.MkdirAll(cfg.DelightHome, 0755); err != nil {
		return fmt.Errorf("failed to create happy home directory: %w", err)
	}

	// Save the account secret (master.key) using storage package (which base64-encodes it)
	masterKeyPath := filepath.Join(cfg.DelightHome, "master.key")
	if err := storage.SaveSecretKey(masterKeyPath, secret); err != nil {
		return fmt.Errorf("failed to save master secret: %w", err)
	}

	// Save the access token
	if err := os.WriteFile(cfg.AccessKey, []byte(token), 0600); err != nil {
		return fmt.Errorf("failed to save access token: %w", err)
	}

	return nil
}

// toBase64URL converts standard base64 to base64url (RFC 4648)
func toBase64URL(s string) string {
	// Replace + with -, / with _, and remove padding
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.TrimRight(s, "=")
	return s
}
