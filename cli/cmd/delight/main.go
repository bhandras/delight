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
	"strings"
	"syscall"
	"time"

	"github.com/bhandras/delight/cli/internal/cli"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/session"
	"github.com/bhandras/delight/cli/internal/version"
	"github.com/bhandras/delight/shared/logger"
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
	rawArgs := os.Args[1:]
	if wantsHelp(rawArgs) {
		printUsage()
		return nil
	}
	if wantsVersion(rawArgs) {
		fmt.Printf("delight v%s\n", version.RichVersion())
		return nil
	}

	cmdIndex, cmd := findCommand(rawArgs)
	if cmd == "" {
		printUsage()
		return nil
	}

	cfg, err := config.Default()
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}

	leadingFlags := rawArgs[:cmdIndex]
	trailingArgs := rawArgs[cmdIndex+1:]

	switch cmd {
	case "help":
		printUsage()
		return nil
	case "auth":
		if err := parseFlags(cfg, append([]string(nil), leadingFlags...)); err != nil {
			return err
		}
		if err := parseFlags(cfg, append([]string(nil), trailingArgs...)); err != nil {
			return err
		}
		if err := cfg.EnsureHome(); err != nil {
			return err
		}
		logClose, err := setupLogging(".")
		if err != nil {
			return err
		}
		defer logClose()
		return cli.AuthCommand(cfg)
	case "daemon":
		printUsage()
		return nil
	case "run":
		if err := parseFlags(cfg, append([]string(nil), leadingFlags...)); err != nil {
			return err
		}
		if err := parseFlags(cfg, append([]string(nil), trailingArgs...)); err != nil {
			return err
		}
		return runSession(cfg)
	case "acp", "claude", "codex":
		cfg.Agent = cmd
		subCmdIndex, subCmd := findCommand(trailingArgs)
		if subCmd == "" {
			printUsage()
			return nil
		}
		if subCmd != "run" {
			return fmt.Errorf("unknown command: %s %s", cmd, subCmd)
		}
		agentLeading := append([]string(nil), leadingFlags...)
		agentTrailing := append([]string(nil), trailingArgs[:subCmdIndex]...)
		agentRunArgs := append([]string(nil), trailingArgs[subCmdIndex+1:]...)
		if err := parseFlags(cfg, agentLeading); err != nil {
			return err
		}
		if err := parseFlags(cfg, agentTrailing); err != nil {
			return err
		}
		if err := parseFlags(cfg, agentRunArgs); err != nil {
			return err
		}
		return runSession(cfg)
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

// setupLogging configures log output to a file under workDir.
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

// parseFlags applies CLI flags to cfg and configures the shared logger.
func parseFlags(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("delight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	serverURL := fs.String("server-url", "", "Delight server URL")
	homeDir := fs.String("home-dir", "", "Delight home directory")
	acpURL := fs.String("acp-url", "", "ACP server URL")
	acpAgent := fs.String("acp-agent", "", "ACP agent name")
	agent := fs.String("agent", "", "Agent backend (acp|claude|codex)")
	model := fs.String("model", "", "Model identifier (engine-specific)")
	forceNewSession := fs.Bool("new-session", false, "Force creation of a new session")
	logLevel := fs.String("log-level", "info", "Log level (trace|debug|info|warn|error)")
	socketIOTransport := fs.String("socketio-transport", "websocket", "Socket.IO transport (websocket|polling)")
	startingMode := fs.String("mode", "", "Starting mode (local|remote)")

	if err := fs.Parse(args); err != nil {
		return err
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
		if *agent != "acp" && *agent != "claude" && *agent != "codex" && *agent != "fake" {
			return fmt.Errorf("invalid --agent %q (expected acp, claude, or codex)", *agent)
		}
		cfg.Agent = *agent
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *startingMode != "" {
		switch *startingMode {
		case "local", "remote":
			cfg.StartingMode = *startingMode
		default:
			return fmt.Errorf("invalid --mode %q (expected local or remote)", *startingMode)
		}
	}
	if *forceNewSession {
		cfg.ForceNewSession = true
	}
	switch *socketIOTransport {
	case "websocket", "polling":
		cfg.SocketIOTransport = *socketIOTransport
	default:
		return fmt.Errorf("invalid --socketio-transport %q (expected websocket or polling)", *socketIOTransport)
	}
	level, err := logger.ParseLevel(*logLevel)
	if err != nil {
		return err
	}
	logger.SetLevel(level)
	cfg.Debug = level <= logger.LevelDebug

	return nil
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
	fmt.Println(`delight - Delight CLI for agent session sync

Usage:
  delight [command]

Commands:
  run                  Start a new session (default: Codex)
  claude run           Start a session using Claude
  codex run            Start a session using Codex
  acp run              Start a session using ACP
  auth                 Authenticate with QR code for mobile pairing
  help                 Show this help message
  version              Show version information

Flags:
  --server-url        Delight server URL
  --home-dir          Delight home directory
  --acp-url           ACP server URL
  --acp-agent         ACP agent name
  --agent             Agent backend (acp|claude|codex)
  --mode              Starting mode (local|remote)
  --model             Model identifier (engine-specific)
  --new-session       Force creation of a new session
  --socketio-transport Socket.IO transport (websocket|polling)
  --log-level         Log level (trace|debug|info|warn|error)

Examples:
  # Authenticate with QR code (required before running sessions)
  delight auth

  # Start a session (Codex by default)
  delight run

  # Start a Claude session
  delight claude run

  # Start a session with custom server and model
  delight run --server-url=http://localhost:3005 --model=gpt-5.2-codex`)
}

// wantsHelp reports whether args request usage output.
func wantsHelp(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			return true
		}
	}
	_, cmd := findCommand(args)
	return cmd == "help"
}

// wantsVersion reports whether args request version output.
func wantsVersion(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--version", "-v":
			return true
		}
	}
	_, cmd := findCommand(args)
	return cmd == "version"
}

// findCommand returns the first non-flag token in args.
func findCommand(args []string) (int, string) {
	for i, arg := range args {
		if arg == "" {
			continue
		}
		if arg == "-" || arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return i, arg
	}
	return len(args), ""
}

// runSession starts a Delight session with the provided configuration.
func runSession(cfg *config.Config) error {
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	logClose, err := setupLogging(workDir)
	if err != nil {
		return err
	}
	defer logClose()

	if err := cfg.EnsureHome(); err != nil {
		return err
	}

	// Require explicit authentication. `delight run` should never prompt for QR
	// pairing implicitly so it's safe to invoke in scripts.
	masterKeyPath := filepath.Join(cfg.DelightHome, "master.key")
	if _, err := os.Stat(masterKeyPath); os.IsNotExist(err) {
		return fmt.Errorf(
			"not authenticated (missing %s); run `delight auth` first",
			masterKeyPath,
		)
	}

	// Load access token
	tokenData, err := os.ReadFile(cfg.AccessKey)
	if err != nil {
		return fmt.Errorf(
			"not authenticated (missing %s); run `delight auth` first",
			cfg.AccessKey,
		)
	}
	token := string(tokenData)

	if cfg.Debug {
		logger.Debugf("Access token: %s...", token[:20])
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
