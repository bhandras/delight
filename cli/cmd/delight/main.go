package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bhandras/delight/cli/internal/cli"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/session"
)

const (
	authChallenge = "delight-auth-challenge"
)

type AuthRequest struct {
	PublicKey string `json:"publicKey"`
	Challenge string `json:"challenge"`
	Signature string `json:"signature"`
}

type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
	Error   string `json:"error,omitempty"`
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	args, err := parseFlags(cfg, os.Args[1:])
	if err != nil {
		return err
	}

	if cfg.Debug {
		log.Printf("Config: ServerURL=%s, DelightHome=%s", cfg.ServerURL, cfg.DelightHome)
	}

	// Check for subcommands
	if len(args) > 0 {
		switch args[0] {
		case "claude":
			cfg.Agent = "claude"
			args = args[1:]
		case "codex":
			cfg.Agent = "codex"
			args = args[1:]
		case "auth":
			return cli.AuthCommand(cfg)
		case "help", "--help", "-h":
			printUsage()
			return nil
		case "version", "--version", "-v":
			fmt.Println("delight-cli-go v1.0.0")
			return nil
		case "daemon":
			if len(args) > 1 && args[1] == "stop" {
				return cli.StopDaemonCommand(cfg)
			}
			fmt.Println("Usage: delight daemon stop")
			return nil
		case "stop-daemon":
			return cli.StopDaemonCommand(cfg)
		}
	}

	// Check if we have account credentials (master.key and access.key)
	masterKeyPath := filepath.Join(cfg.DelightHome, "master.key")
	if _, err := os.Stat(masterKeyPath); os.IsNotExist(err) {
		// No account credentials - trigger QR code authentication
		log.Println("No account credentials found. Starting authentication...")
		log.Println("")
		if err := cli.AuthCommand(cfg); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
		log.Println("")
	}

	// Load access token
	tokenData, err := os.ReadFile(cfg.AccessKey)
	if err != nil {
		return fmt.Errorf("failed to read access token: %w", err)
	}
	token := string(tokenData)

	if cfg.Debug {
		log.Printf("Access token: %s...", token[:20])
	}

	log.Println("Authentication successful!")
	log.Printf("Delight home: %s", cfg.DelightHome)
	log.Printf("Server: %s", cfg.ServerURL)

	// TODO: Parse command-line args for subcommands
	// For MVP, just start a session in current directory
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	log.Printf("Starting Delight session in: %s", workDir)
	log.Printf("Agent: %s", cfg.Agent)

	// Create and start session manager
	sessionMgr, err := session.NewManager(cfg, token, cfg.Debug)
	if err != nil {
		return fmt.Errorf("failed to create session manager: %w", err)
	}
	defer sessionMgr.Close()

	if err := sessionMgr.Start(workDir); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	log.Println("Delight session started! Press Ctrl+C to exit.")

	// Wait for Claude to exit
	if err := sessionMgr.Wait(); err != nil {
		log.Printf("Session ended with error: %v", err)
	}

	return nil
}

func parseFlags(cfg *config.Config, args []string) ([]string, error) {
	fs := flag.NewFlagSet("delight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	acpURL := fs.String("acp-url", "", "ACP server URL")
	acpAgent := fs.String("acp-agent", "", "ACP agent name")
	agent := fs.String("agent", "", "Agent backend (claude|codex)")
	forceNewSession := fs.Bool("new-session", false, "Force creation of a new session")
	showHelp := fs.Bool("help", false, "Show help")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if *showHelp {
		printUsage()
		return nil, nil
	}

	if *acpURL != "" {
		cfg.ACPURL = *acpURL
	}
	if *acpAgent != "" {
		cfg.ACPAgent = *acpAgent
	}
	cfg.ACPEnable = cfg.ACPURL != "" && cfg.ACPAgent != ""
	if *agent != "" {
		if *agent != "claude" && *agent != "codex" {
			return nil, fmt.Errorf("invalid --agent %q (expected claude or codex)", *agent)
		}
		cfg.Agent = *agent
	}
	if *forceNewSession {
		cfg.ForceNewSession = true
	}

	return fs.Args(), nil
}

func getOrCreateAccessToken(cfg *config.Config, publicKey, privateKey []byte) (string, error) {
	// Try to load existing token
	if data, err := os.ReadFile(cfg.AccessKey); err == nil {
		token := string(data)
		if token != "" {
			// TODO: Validate token isn't expired
			return token, nil
		}
	}

	// Authenticate with server
	signature, err := crypto.Sign(privateKey, []byte(authChallenge))
	if err != nil {
		return "", fmt.Errorf("failed to sign challenge: %w", err)
	}

	authReq := AuthRequest{
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Challenge: base64.StdEncoding.EncodeToString([]byte(authChallenge)),
		Signature: base64.StdEncoding.EncodeToString(signature),
	}

	body, err := json.Marshal(authReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal auth request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/auth", cfg.ServerURL)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send auth request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth failed: %s - %s", resp.Status, string(respBody))
	}

	var authResp AuthResponse
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if !authResp.Success {
		return "", fmt.Errorf("auth failed: %s", authResp.Error)
	}

	return authResp.Token, nil
}

func printUsage() {
	fmt.Println(`delight - Delight CLI for Claude Code session sync

Usage:
  delight              Start a new Delight session (spawns Claude by default)
  delight claude       Start a Claude session
  delight codex        Start a Codex session
  delight auth         Authenticate with QR code for mobile pairing
  delight help         Show this help message
  delight version      Show version information
  delight daemon stop  Stop the running daemon (sessions stay alive)
  delight stop-daemon  Stop the running daemon (sessions stay alive)

Environment Variables:
  DELIGHT_SERVER_URL Server URL (default: https://happy-api.slopus.com)
  DELIGHT_HOME_DIR   Config directory (default: ~/.delight)
  DELIGHT_AGENT      Agent backend (claude|codex, default: claude)
  DELIGHT_ACP_URL    ACP server URL (enables ACP mode when set)
  DELIGHT_ACP_AGENT  ACP agent name (required with DELIGHT_ACP_URL)
  DEBUG              Enable debug logging (true/1)

Flags:
  --acp-url           ACP server URL
  --acp-agent         ACP agent name
  --agent            Agent backend (claude|codex)
  --new-session       Force creation of a new session

Examples:
  # Authenticate with QR code
  delight auth

  # Start a Claude session with default server
  delight

  # Start a Claude session with custom server
  DELIGHT_SERVER_URL=http://localhost:3005 delight`)
}
