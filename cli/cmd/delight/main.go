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
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bhandras/delight/cli/internal/cli"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/session"
	"github.com/bhandras/delight/cli/pkg/logger"
)

const (
	authChallenge = "delight-auth-challenge"
)

const (
	// signalShutdownTimeout bounds how long we wait after the first Ctrl+C for
	// the session manager to stop runners and restore terminal state.
	signalShutdownTimeout = 3 * time.Second

	// signalChannelDepth allows capturing a second Ctrl+C while a graceful
	// shutdown is in progress.
	signalChannelDepth = 2
)

// AuthRequest is the request payload for the Delight auth endpoint.
type AuthRequest struct {
	PublicKey string `json:"publicKey"`
	Challenge string `json:"challenge"`
	Signature string `json:"signature"`
}

// AuthResponse is the response payload from the Delight auth endpoint.
type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
	Error   string `json:"error,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	logClose, err := setupLogging(workDir)
	if err != nil {
		return err
	}
	defer logClose()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	args, err := parseFlags(cfg, os.Args[1:])
	if err != nil {
		return err
	}

	// Check for subcommands
	if len(args) > 0 {
		switch args[0] {
		case "acp":
			cfg.Agent = "acp"
			args = args[1:]
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
		logger.Infof("No account credentials found. Starting authentication...")
		logger.Infof("")
		if err := cli.AuthCommand(cfg); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
		logger.Infof("")
	}

	// Load access token
	tokenData, err := os.ReadFile(cfg.AccessKey)
	if err != nil {
		return fmt.Errorf("failed to read access token: %w", err)
	}
	token := string(tokenData)

	if cfg.Debug {
		logger.Debugf("Access token: %s...", token[:20])
	}

	if cfg.Debug {
		logger.Debugf("Config: ServerURL=%s, DelightHome=%s", cfg.ServerURL, cfg.DelightHome)
	}

	logger.Infof("Authentication successful!")
	logger.Infof("Delight home: %s", cfg.DelightHome)
	logger.Infof("Server: %s", cfg.ServerURL)

	logger.Infof("Starting Delight session in: %s", workDir)
	logger.Infof("Agent: %s", cfg.Agent)

	// Create and start session manager
	sessionMgr, err := session.NewManager(cfg, token, cfg.Debug)
	if err != nil {
		return fmt.Errorf("failed to create session manager: %w", err)
	}
	defer sessionMgr.Close()

	if err := sessionMgr.Start(workDir); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	logger.Infof("Delight session started! Press Ctrl+C to exit.")

	sigCh := make(chan os.Signal, signalChannelDepth)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- sessionMgr.Wait()
	}()

	select {
	case err := <-waitCh:
		if err != nil {
			logger.Warnf("Session ended with error: %v", err)
		}
	case <-sigCh:
		// Graceful shutdown on first Ctrl+C so:
		// - agent subprocesses (Codex/Claude) are stopped,
		// - terminal state (raw mode, process groups) is restored.
		//
		// If shutdown hangs, a second Ctrl+C forces exit.
		go func() {
			<-sigCh
			os.Exit(1)
		}()
		_ = sessionMgr.Close()
		select {
		case err := <-waitCh:
			if err != nil {
				logger.Warnf("Session ended with error: %v", err)
			}
		case <-time.After(signalShutdownTimeout):
			os.Exit(1)
		}
		fmt.Fprint(os.Stdout, "\r\n")
	}

	return nil
}

func setupLogging(workDir string) (func(), error) {
	logPath := filepath.Join(workDir, "delight.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.SetOutput(io.Discard)
		logger.SetOutput(io.Discard)
		return func() {}, fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}
	flags := log.LstdFlags | log.Lmicroseconds
	log.SetFlags(flags)
	log.SetOutput(file)
	logger.SetFlags(flags)
	logger.SetOutput(file)
	return func() { _ = file.Close() }, nil
}

func parseFlags(cfg *config.Config, args []string) ([]string, error) {
	fs := flag.NewFlagSet("delight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	serverURL := fs.String("server-url", "", "Delight server URL")
	homeDir := fs.String("home-dir", "", "Delight home directory")
	acpURL := fs.String("acp-url", "", "ACP server URL")
	acpAgent := fs.String("acp-agent", "", "ACP agent name")
	agent := fs.String("agent", "", "Agent backend (acp|claude|codex)")
	forceNewSession := fs.Bool("new-session", false, "Force creation of a new session")
	logLevel := fs.String("log-level", "info", "Log level (trace|debug|info|warn|error)")
	socketIOTransport := fs.String("socketio-transport", "websocket", "Socket.IO transport (websocket|polling)")
	showHelp := fs.Bool("help", false, "Show help")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if *showHelp {
		printUsage()
		return nil, nil
	}

	if *serverURL != "" {
		cfg.ServerURL = *serverURL
	}
	if *homeDir != "" {
		cfg.DelightHome = *homeDir
		cfg.AccessKey = filepath.Join(cfg.DelightHome, "access.key")
	}
	if *acpURL != "" {
		cfg.ACPURL = *acpURL
	}
	if *acpAgent != "" {
		cfg.ACPAgent = *acpAgent
	}
	cfg.ACPEnable = cfg.ACPURL != "" && cfg.ACPAgent != ""
	if *agent != "" {
		if *agent != "acp" && *agent != "claude" && *agent != "codex" {
			return nil, fmt.Errorf("invalid --agent %q (expected acp, claude, or codex)", *agent)
		}
		cfg.Agent = *agent
	}
	if *forceNewSession {
		cfg.ForceNewSession = true
	}
	switch *socketIOTransport {
	case "websocket", "polling":
		cfg.SocketIOTransport = *socketIOTransport
	default:
		return nil, fmt.Errorf("invalid --socketio-transport %q (expected websocket or polling)", *socketIOTransport)
	}
	level, err := logger.ParseLevel(*logLevel)
	if err != nil {
		return nil, err
	}
	logger.SetLevel(level)
	cfg.Debug = level <= logger.LevelDebug

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

Flags:
  --server-url        Delight server URL
  --home-dir          Delight home directory
  --acp-url           ACP server URL
  --acp-agent         ACP agent name
  --agent            Agent backend (claude|codex)
  --new-session       Force creation of a new session
  --socketio-transport Socket.IO transport (websocket|polling)
  --log-level         Log level (trace|debug|info|warn|error)

Examples:
  # Authenticate with QR code
  delight auth

  # Start a Claude session with default server
  delight

  # Start a Claude session with custom server
  delight --server-url=http://localhost:3005`)
}
