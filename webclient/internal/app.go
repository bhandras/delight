package webclient

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/sdk"
)

// App hosts the web bridge runtime, HTTP API, and stream fan-out.
type App struct {
	cfg       Config
	statePath string

	mu    sync.Mutex
	state persistentState

	sdkClient *sdk.Client
	events    *eventHub
	logs      *logBuffer
	server    *http.Server

	streamEpoch  string
	sdkConnected bool

	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewApp constructs a configured bridge instance.
func NewApp(cfg Config) (*App, error) {
	statePath, err := resolveStatePath(cfg)
	if err != nil {
		return nil, err
	}
	state, err := loadState(statePath, cfg.ServerURL)
	if err != nil {
		return nil, err
	}
	if trimmed := strings.TrimSpace(cfg.ServerURL); trimmed != "" {
		state.ServerURL = strings.TrimRight(trimmed, "/")
	}

	client := sdk.NewClient(state.ServerURL)
	if state.Token != "" {
		client.SetToken(state.Token)
	}
	if state.MasterKey != "" {
		if err := client.SetMasterKeyBase64(state.MasterKey); err != nil {
			return nil, fmt.Errorf("load persisted master key: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	app := &App{
		cfg:       cfg,
		statePath: statePath,
		state:     state,
		sdkClient: client,
		events:    newEventHub(maxEventBuffer),
		logs:      newLogBuffer(maxLogEntries),
		streamEpoch: fmt.Sprintf(
			"%d-%08x",
			time.Now().UnixNano(),
			rand.Uint32(),
		),
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
	client.SetListener(newSDKListener(app))

	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.server = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: app.wrapMiddleware(mux),
	}

	app.logInfo("web bridge initialized")
	app.maybeAutoConnect()
	return app, nil
}

// maybeAutoConnect reconnects the SDK websocket if we have persisted credentials.
func (a *App) maybeAutoConnect() {
	state := a.stateSnapshot()
	if strings.TrimSpace(state.Token) == "" {
		return
	}

	go func() {
		a.logInfo("attempting auto-connect")
		if a.sdkConnectionSnapshot() {
			a.sdkClient.Disconnect()
		}
		if err := a.sdkClient.Connect(); err != nil {
			a.logError("auto-connect failed: " + err.Error())
			return
		}
	}()
}

// Serve starts the HTTP server and blocks until it exits.
func (a *App) Serve() error {
	a.logInfo("listening on " + a.server.Addr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	a.logInfo("shutting down")
	if a.shutdownCancel != nil {
		a.shutdownCancel()
	}
	return a.server.Shutdown(ctx)
}

// stateSnapshot returns a copy of the current persisted state.
func (a *App) stateSnapshot() persistentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// sdkConnectionSnapshot returns whether the SDK websocket is currently connected.
func (a *App) sdkConnectionSnapshot() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sdkConnected
}

// setSDKConnected updates the in-memory connection state.
func (a *App) setSDKConnected(connected bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sdkConnected = connected
}

// updateState applies a mutation and persists the resulting state.
func (a *App) updateState(mutator func(*persistentState)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	mutator(&a.state)
	if err := saveState(a.statePath, a.state); err != nil {
		return err
	}
	return nil
}

// publish emits a stream event with JSON payload.
func (a *App) publish(kind string, payload interface{}) {
	a.events.publishJSON(kind, payload)
}

// logInfo records an informational log line.
func (a *App) logInfo(message string) {
	a.logs.add("info", message)
	a.publish("log", map[string]string{"level": "info", "message": message})
}

// logError records an error log line.
func (a *App) logError(message string) {
	a.logs.add("error", message)
	a.publish("error", map[string]string{"message": message})
}

// streamEpochSnapshot returns the bridge stream epoch used for client cursor resets.
func (a *App) streamEpochSnapshot() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.streamEpoch
}

// callSessionRPC sends a session-scoped RPC and decodes a best-effort JSON response.
func (a *App) callSessionRPC(sessionID, suffix string, params interface{}) (json.RawMessage, error) {
	method := strings.TrimSpace(sessionID) + ":" + strings.TrimSpace(suffix)
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	paramsJSON := "{}"
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode rpc params: %w", err)
		}
		paramsJSON = string(encoded)
	}
	resp, err := a.sdkClient.CallRPC(method, paramsJSON)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp) == "" {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(resp), nil
}

// callTerminalRPC sends a terminal-scoped RPC and decodes a best-effort JSON response.
func (a *App) callTerminalRPC(terminalID, suffix string) (json.RawMessage, error) {
	method := strings.TrimSpace(terminalID) + ":" + strings.TrimSpace(suffix)
	if strings.TrimSpace(terminalID) == "" {
		return nil, fmt.Errorf("terminal id is required")
	}
	resp, err := a.sdkClient.CallRPC(method, "{}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp) == "" {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(resp), nil
}
