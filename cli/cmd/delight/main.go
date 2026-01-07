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
	"github.com/bhandras/delight/cli/internal/session"
	"github.com/bhandras/delight/cli/internal/version"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
)

const (
	// signalShutdownTimeout bounds how long we wait after the first Ctrl+C for
	// the session manager to stop runners and restore terminal state.
	signalShutdownTimeout = 3 * time.Second

	// signalChannelDepth allows capturing a second Ctrl+C while a graceful
	// shutdown is in progress.
	signalChannelDepth = 2
)

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
		logClose, err := setupLogging(cfg.DelightHome)
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
	case "sessions":
		if err := parseFlags(cfg, append([]string(nil), leadingFlags...)); err != nil {
			return err
		}
		if err := parseFlags(cfg, append([]string(nil), trailingArgs...)); err != nil {
			return err
		}
		return listSessions(cfg)
	case "acp", "claude", "codex":
		cfg.Agent = cmd
		subCmdIndex, subCmd := findCommand(trailingArgs)
		if subCmd == "" {
			printUsage()
			return nil
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
		switch subCmd {
		case "run":
			return runSession(cfg)
		case "resume":
			if len(agentRunArgs) == 0 || strings.TrimSpace(agentRunArgs[0]) == "" {
				return fmt.Errorf("usage: delight %s resume <session_id>", cmd)
			}
			// Resume should always start in remote mode so the phone can control
			// the session immediately after reattaching.
			cfg.ResumeToken = strings.TrimSpace(agentRunArgs[0])
			cfg.StartingMode = "remote"
			return runSession(cfg)
		default:
			return fmt.Errorf("unknown command: %s %s", cmd, subCmd)
		}
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

type listSessionsResponse struct {
	Sessions []listSessionItem `json:"sessions"`
}

type listSessionItem struct {
	ID         string  `json:"id"`
	TerminalID string  `json:"terminalId"`
	Active     bool    `json:"active"`
	ActiveAt   int64   `json:"activeAt"`
	UpdatedAt  int64   `json:"updatedAt"`
	Metadata   string  `json:"metadata"`
	AgentState *string `json:"agentState"`
}

// listSessions prints all known sessions for the currently authenticated user.
func listSessions(cfg *config.Config) error {
	if err := cfg.EnsureHome(); err != nil {
		return err
	}

	// Require explicit authentication. `delight sessions` should never prompt
	// for QR pairing implicitly so it's safe to invoke in scripts.
	masterKeyPath := filepath.Join(cfg.DelightHome, "master.key")
	if _, err := os.Stat(masterKeyPath); os.IsNotExist(err) {
		return fmt.Errorf(
			"not authenticated (missing %s); run `delight auth` first",
			masterKeyPath,
		)
	}

	token, err := cli.EnsureAccessToken(cfg)
	if err != nil {
		return fmt.Errorf("not authenticated: %w", err)
	}

	req, err := http.NewRequest("GET", strings.TrimRight(cfg.ServerURL, "/")+"/v1/sessions", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("failed to list sessions: %s", msg)
	}

	var decoded listSessionsResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	var out bytes.Buffer
	for _, sess := range decoded.Sessions {
		meta := types.Metadata{}
		if sess.Metadata != "" {
			if raw, err := base64.StdEncoding.DecodeString(sess.Metadata); err == nil {
				_ = json.Unmarshal(raw, &meta)
			}
		}

		state := types.AgentState{}
		if sess.AgentState != nil && strings.TrimSpace(*sess.AgentState) != "" {
			_ = json.Unmarshal([]byte(*sess.AgentState), &state)
		}

		agentType := strings.TrimSpace(state.AgentType)
		if agentType == "" {
			agentType = "unknown"
		}
		dir := strings.TrimSpace(meta.Path)
		host := strings.TrimSpace(meta.Host)

		lastActive := ""
		if sess.ActiveAt > 0 {
			lastActive = time.UnixMilli(sess.ActiveAt).Format(time.RFC3339)
		}

		fmt.Fprintf(&out, "session=%s agent=%s active=%t last_active=%s\n", sess.ID, agentType, sess.Active, lastActive)
		if host != "" || dir != "" {
			fmt.Fprintf(&out, "  host=%s dir=%s terminal=%s\n", host, dir, sess.TerminalID)
		}
		if token := strings.TrimSpace(state.ResumeToken); token != "" && (agentType == "codex" || agentType == "claude") {
			fmt.Fprintf(&out, "  resume: delight %s resume %s\n", agentType, token)
		}
	}

	_, _ = io.Copy(os.Stdout, &out)
	return nil
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

func printUsage() {
	fmt.Println(`delight - Delight CLI for agent session sync

Usage:
  delight [command]

Commands:
  run                  Start a new session (default: Codex)
  claude run           Start a session using Claude
  codex run            Start a session using Codex
  acp run              Start a session using ACP
  claude resume        Resume a Claude session by id
  codex resume         Resume a Codex session by id
  sessions             List all sessions for this account
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

  # List sessions and their resume commands
  delight sessions

  # Resume an upstream session locally
  delight codex resume <id>
  delight claude resume <id>

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
	if err := cfg.EnsureHome(); err != nil {
		return err
	}

	logClose, err := setupLogging(cfg.DelightHome)
	if err != nil {
		return err
	}
	defer logClose()

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
	token, err := cli.EnsureAccessToken(cfg)
	if err != nil {
		return fmt.Errorf("not authenticated: %w", err)
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
