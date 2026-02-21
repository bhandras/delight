package webclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/sdk"
	"github.com/bhandras/delight/shared/webapi"
)

const (
	// bearerPrefix is the Authorization header prefix for token auth.
	bearerPrefix = "Bearer "
	// maxRequestBodyBytes limits request body size for API handlers.
	maxRequestBodyBytes = 1 << 20
)

// registerRoutes wires API and UI routes.
func (a *App) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/config", a.handleConfigGet)
	mux.HandleFunc("POST /api/config/server", a.handleConfigServerSet)
	mux.HandleFunc("POST /api/account/key/generate", a.handleAccountKeyGenerate)
	mux.HandleFunc("POST /api/account/key/reset", a.handleAccountKeyReset)
	mux.HandleFunc("POST /api/account/create", a.handleAccountCreate)
	mux.HandleFunc("POST /api/account/connect", a.handleAccountConnect)
	mux.HandleFunc("POST /api/account/disconnect", a.handleAccountDisconnect)
	mux.HandleFunc("POST /api/account/logout", a.handleAccountLogout)

	mux.HandleFunc("GET /api/sessions", a.handleSessionsList)
	mux.HandleFunc("GET /api/sessions/{id}/messages", a.handleSessionMessages)
	mux.HandleFunc("POST /api/sessions/{id}/send", a.handleSessionSend)
	mux.HandleFunc("POST /api/sessions/{id}/switch", a.handleSessionSwitch)
	mux.HandleFunc("POST /api/sessions/{id}/abort", a.handleSessionAbort)
	mux.HandleFunc("POST /api/sessions/{id}/permission", a.handleSessionPermission)
	mux.HandleFunc("POST /api/sessions/{id}/agent-config", a.handleSessionAgentConfig)
	mux.HandleFunc("POST /api/sessions/{id}/agent-capabilities", a.handleSessionAgentCapabilities)

	mux.HandleFunc("GET /api/terminals", a.handleTerminalsList)
	mux.HandleFunc("POST /api/terminals/pair", a.handleTerminalsPair)
	mux.HandleFunc("POST /api/terminals/approve", a.handleTerminalsApprove)
	mux.HandleFunc("DELETE /api/terminals/{id}", a.handleTerminalDelete)
	mux.HandleFunc("POST /api/terminals/{id}/stop-daemon", a.handleTerminalStopDaemon)
	mux.HandleFunc("POST /api/terminals/{id}/restart-daemon", a.handleTerminalRestartDaemon)

	mux.HandleFunc("GET /api/preferences", a.handlePreferencesGet)
	mux.HandleFunc("POST /api/preferences", a.handlePreferencesSet)

	mux.HandleFunc("GET /api/debug/logs", a.handleDebugLogs)
	mux.HandleFunc("POST /api/debug/logs/clear", a.handleDebugLogsClear)
	mux.HandleFunc("POST /api/debug/log-server/start", a.handleDebugLogServerStart)
	mux.HandleFunc("POST /api/debug/log-server/stop", a.handleDebugLogServerStop)
	mux.HandleFunc("GET /api/stream", a.handleStream)

	mux.HandleFunc("GET /", a.handleUI)
	mux.HandleFunc("GET /app.js", a.handleUI)
	mux.HandleFunc("GET /styles.css", a.handleUI)
}

// wrapMiddleware applies auth, origin checks, and CORS handling.
func (a *App) wrapMiddleware(next http.Handler) http.Handler {
	allowlist := parseOriginAllowlist(a.cfg.OriginsCSV)
	apiToken := strings.TrimSpace(a.cfg.APIToken)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !isOriginAllowed(origin, allowlist) {
				writeAPIError(w, http.StatusForbidden, "origin_forbidden", "origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			if apiToken != "" && !isAuthorizedRequest(r, apiToken) {
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isAuthorizedRequest validates API auth via header or stream query token.
func isAuthorizedRequest(r *http.Request, expectedToken string) bool {
	if hasBearerToken(r.Header.Get("Authorization"), expectedToken) {
		return true
	}
	queryToken := strings.TrimSpace(r.URL.Query().Get("access_token"))
	return queryToken != "" && queryToken == expectedToken
}

// parseOriginAllowlist converts a CSV allowlist into a lookup map.
func parseOriginAllowlist(originsCSV string) map[string]struct{} {
	parts := strings.Split(originsCSV, ",")
	out := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out[trimmed] = struct{}{}
	}
	return out
}

// isOriginAllowed reports whether the request origin is allowed.
func isOriginAllowed(origin string, allowlist map[string]struct{}) bool {
	if len(allowlist) == 0 {
		return true
	}
	_, ok := allowlist[origin]
	return ok
}

// hasBearerToken validates an Authorization header against a static token.
func hasBearerToken(headerValue, expectedToken string) bool {
	headerValue = strings.TrimSpace(headerValue)
	if !strings.HasPrefix(headerValue, bearerPrefix) {
		return false
	}
	actual := strings.TrimSpace(strings.TrimPrefix(headerValue, bearerPrefix))
	return actual != "" && actual == expectedToken
}

// handleHealth returns a bridge health response.
func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	state := a.stateSnapshot()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"serverURL": state.ServerURL,
	})
}

// handleConfigGet returns current bridge runtime config for the web UI.
func (a *App) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	state := a.stateSnapshot()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"serverURL":           state.ServerURL,
		"masterKey":           state.MasterKey,
		"hasMasterKey":        strings.TrimSpace(state.MasterKey) != "",
		"hasToken":            strings.TrimSpace(state.Token) != "",
		"connected":           a.sdkConnectionSnapshot(),
		"preferences":         state.Preferences,
		"streamEpoch":         a.streamEpochSnapshot(),
		"streamLatestEventID": a.events.latestEventID(),
	})
}

// handleConfigServerSet updates the configured Delight server URL.
func (a *App) handleConfigServerSet(w http.ResponseWriter, r *http.Request) {
	var req serverConfigRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	serverURL := strings.TrimRight(strings.TrimSpace(req.ServerURL), "/")
	if serverURL == "" {
		writeAPIError(w, http.StatusBadRequest, "server_url_required", "server URL is required")
		return
	}
	if err := a.updateState(func(s *persistentState) {
		s.ServerURL = serverURL
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	a.sdkClient.SetServerURL(serverURL)
	writeJSON(w, http.StatusOK, map[string]string{"serverURL": serverURL})
}

// handleAccountKeyGenerate creates a new master key.
func (a *App) handleAccountKeyGenerate(w http.ResponseWriter, _ *http.Request) {
	masterKey, err := sdk.GenerateMasterKeyBase64()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "key_generate_failed", err.Error())
		return
	}
	if err := a.updateState(func(s *persistentState) {
		s.MasterKey = masterKey
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	if err := a.sdkClient.SetMasterKeyBase64(masterKey); err != nil {
		writeAPIError(w, http.StatusBadRequest, "key_set_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"masterKey": masterKey})
}

// handleAccountKeyReset clears local auth and key state.
func (a *App) handleAccountKeyReset(w http.ResponseWriter, _ *http.Request) {
	a.sdkClient.Disconnect()
	a.sdkClient.SetToken("")
	if err := a.updateState(func(s *persistentState) {
		s.Token = ""
		s.MasterKey = ""
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	a.publish("disconnected", streamPublishPayload{Reason: "key-reset"})
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleAccountCreate authenticates with a master key and stores credentials.
func (a *App) handleAccountCreate(w http.ResponseWriter, r *http.Request) {
	var req accountCreateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state := a.stateSnapshot()
	serverURL := strings.TrimRight(strings.TrimSpace(req.ServerURL), "/")
	if serverURL == "" {
		serverURL = state.ServerURL
	}
	masterKey := strings.TrimSpace(req.MasterKey)
	generated := false
	if masterKey == "" {
		masterKey = strings.TrimSpace(state.MasterKey)
	}
	if masterKey == "" {
		generatedKey, err := sdk.GenerateMasterKeyBase64()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "key_generate_failed", err.Error())
			return
		}
		masterKey = generatedKey
		generated = true
	}
	if err := a.sdkClient.SetMasterKeyBase64(masterKey); err != nil {
		writeAPIError(w, http.StatusBadRequest, "key_set_failed", err.Error())
		return
	}
	a.sdkClient.SetServerURL(serverURL)
	token, err := a.sdkClient.AuthWithMasterKeyBase64(masterKey)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "auth_failed", err.Error())
		return
	}
	if a.sdkConnectionSnapshot() {
		a.sdkClient.Disconnect()
	}
	if err := a.sdkClient.Connect(); err != nil {
		a.logError("connect after account create failed: " + err.Error())
	}
	if err := a.updateState(func(s *persistentState) {
		s.ServerURL = serverURL
		s.MasterKey = masterKey
		s.Token = token
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	a.publish("connected", streamPublishPayload{Reason: "account-created"})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     token,
		"masterKey": masterKey,
		"generated": generated,
	})
}

// handleAccountConnect authenticates and connects using stored or provided key.
func (a *App) handleAccountConnect(w http.ResponseWriter, r *http.Request) {
	var req accountConnectRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state := a.stateSnapshot()
	serverURL := strings.TrimRight(strings.TrimSpace(req.ServerURL), "/")
	if serverURL == "" {
		serverURL = state.ServerURL
	}
	masterKey := strings.TrimSpace(req.MasterKey)
	if masterKey == "" {
		masterKey = strings.TrimSpace(state.MasterKey)
	}
	if masterKey == "" {
		writeAPIError(w, http.StatusBadRequest, "master_key_required", "master key is required")
		return
	}
	if err := a.sdkClient.SetMasterKeyBase64(masterKey); err != nil {
		writeAPIError(w, http.StatusBadRequest, "key_set_failed", err.Error())
		return
	}
	a.sdkClient.SetServerURL(serverURL)
	token, err := a.sdkClient.AuthWithMasterKeyBase64(masterKey)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "auth_failed", err.Error())
		return
	}
	if a.sdkConnectionSnapshot() {
		a.sdkClient.Disconnect()
	}
	if err := a.sdkClient.Connect(); err != nil {
		writeAPIError(w, http.StatusBadGateway, "connect_failed", err.Error())
		return
	}
	if err := a.updateState(func(s *persistentState) {
		s.ServerURL = serverURL
		s.MasterKey = masterKey
		s.Token = token
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	a.publish("connected", streamPublishPayload{Reason: "account-connect"})
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleAccountDisconnect closes the SDK websocket connection.
func (a *App) handleAccountDisconnect(w http.ResponseWriter, _ *http.Request) {
	a.sdkClient.Disconnect()
	a.publish("disconnected", streamPublishPayload{Reason: "manual-disconnect"})
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleAccountLogout disconnects and clears the persisted token.
func (a *App) handleAccountLogout(w http.ResponseWriter, _ *http.Request) {
	a.sdkClient.Disconnect()
	a.sdkClient.SetToken("")
	if err := a.updateState(func(s *persistentState) {
		s.Token = ""
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	a.publish("disconnected", streamPublishPayload{Reason: "logout"})
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleSessionsList returns the decrypted SDK session list payload.
func (a *App) handleSessionsList(w http.ResponseWriter, _ *http.Request) {
	payload, err := a.sdkClient.ListSessions()
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "list_sessions_failed", err.Error())
		return
	}
	writeJSONRaw(w, payload)
}

// handleSessionMessages returns a latest or paged transcript payload.
func (a *App) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "session_required", "session id is required")
		return
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), defaultMessagePageLimit)
	beforeSeq := parseInt64Default(r.URL.Query().Get("beforeSeq"), 0)
	var (
		payload string
		err     error
	)
	if beforeSeq > 0 {
		payload, err = a.sdkClient.GetSessionMessagesPage(sessionID, limit, beforeSeq)
	} else {
		payload, err = a.sdkClient.GetSessionMessages(sessionID, limit)
	}
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "list_messages_failed", err.Error())
		return
	}
	writeJSONRaw(w, payload)
}

// handleTerminalsList returns the decrypted terminal list payload.
func (a *App) handleTerminalsList(w http.ResponseWriter, _ *http.Request) {
	payload, err := a.sdkClient.ListTerminals()
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "list_terminals_failed", err.Error())
		return
	}
	writeJSONRaw(w, payload)
}

// handleTerminalsPair parses a pairing URL and approves terminal auth.
func (a *App) handleTerminalsPair(w http.ResponseWriter, r *http.Request) {
	var req pairTerminalRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	qrURL := strings.TrimSpace(req.QRURL)
	if qrURL == "" {
		writeAPIError(w, http.StatusBadRequest, "qr_required", "pairing URL is required")
		return
	}
	terminalKey, err := sdk.ParseTerminalURL(qrURL)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "pair_parse_failed", err.Error())
		return
	}
	state := a.stateSnapshot()
	if strings.TrimSpace(state.MasterKey) == "" {
		writeAPIError(w, http.StatusBadRequest, "master_key_required", "master key is required")
		return
	}
	if err := a.sdkClient.ApproveTerminalAuth(terminalKey, state.MasterKey); err != nil {
		writeAPIError(w, http.StatusBadGateway, "pair_approve_failed", err.Error())
		return
	}
	host, terminalID := parseTerminalURLMetadata(qrURL)
	receipt := webapi.PairTerminalReceipt{
		ServerURL:   state.ServerURL,
		Host:        host,
		TerminalID:  terminalID,
		TerminalKey: terminalKey,
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "receipt": receipt})
}

// handleTerminalsApprove approves terminal auth for a known terminal public key.
func (a *App) handleTerminalsApprove(w http.ResponseWriter, r *http.Request) {
	var req approveTerminalRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	terminalKey := strings.TrimSpace(req.TerminalPublicKey)
	if terminalKey == "" {
		writeAPIError(w, http.StatusBadRequest, "terminal_key_required", "terminal public key is required")
		return
	}
	state := a.stateSnapshot()
	if strings.TrimSpace(state.MasterKey) == "" {
		writeAPIError(w, http.StatusBadRequest, "master_key_required", "master key is required")
		return
	}
	if err := a.sdkClient.ApproveTerminalAuth(terminalKey, state.MasterKey); err != nil {
		writeAPIError(w, http.StatusBadGateway, "approve_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleTerminalDelete deletes a terminal by ID.
func (a *App) handleTerminalDelete(w http.ResponseWriter, r *http.Request) {
	terminalID := strings.TrimSpace(r.PathValue("id"))
	if terminalID == "" {
		writeAPIError(w, http.StatusBadRequest, "terminal_required", "terminal id is required")
		return
	}
	payload, err := a.sdkClient.DeleteTerminal(terminalID)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "delete_terminal_failed", err.Error())
		return
	}
	writeJSONRaw(w, payload)
}

// handleSessionSend sends a user message with optional optimistic local ID.
func (a *App) handleSessionSend(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "session_required", "session id is required")
		return
	}
	var req sendMessageRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	raw := strings.TrimSpace(req.RawRecordJSON)
	if raw == "" {
		text := strings.TrimSpace(req.Text)
		if text == "" {
			writeAPIError(w, http.StatusBadRequest, "message_required", "text or rawRecordJSON is required")
			return
		}
		record := rawUserMessageRecord{
			Role: "user",
			Content: rawUserMessageRecordBody{
				Type: "text",
				Text: text,
			},
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "encode_message_failed", err.Error())
			return
		}
		raw = string(encoded)
	}
	localID := strings.TrimSpace(req.LocalID)
	var err error
	if localID != "" {
		err = a.sdkClient.SendMessageWithLocalID(sessionID, localID, raw)
	} else {
		err = a.sdkClient.SendMessage(sessionID, raw)
	}
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "send_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleSessionSwitch requests control mode switch for a session.
func (a *App) handleSessionSwitch(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	var req switchRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if mode := strings.TrimSpace(req.Mode); mode != "remote" && mode != "local" {
		writeAPIError(w, http.StatusBadRequest, "invalid_mode", "mode must be local or remote")
		return
	}
	resp, err := a.callSessionRPC(sessionID, "switch", req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "switch_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handleSessionAbort requests abort for an in-flight turn.
func (a *App) handleSessionAbort(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	resp, err := a.callSessionRPC(sessionID, "abort", map[string]any{})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "abort_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handleSessionPermission submits a permission decision.
func (a *App) handleSessionPermission(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	var req permissionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		writeAPIError(w, http.StatusBadRequest, "request_id_required", "requestId is required")
		return
	}
	resp, err := a.callSessionRPC(sessionID, "permission", req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "permission_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handleSessionAgentConfig updates model, effort, and permission mode.
func (a *App) handleSessionAgentConfig(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	var req agentConfigRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	resp, err := a.callSessionRPC(sessionID, "agent-config", req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "agent_config_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handleSessionAgentCapabilities fetches supported agent settings.
func (a *App) handleSessionAgentCapabilities(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	var req agentCapabilitiesRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	resp, err := a.callSessionRPC(sessionID, "agent-capabilities", req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "agent_capabilities_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handleTerminalStopDaemon requests a daemon stop for one terminal.
func (a *App) handleTerminalStopDaemon(w http.ResponseWriter, r *http.Request) {
	terminalID := strings.TrimSpace(r.PathValue("id"))
	resp, err := a.callTerminalRPC(terminalID, "stop-daemon")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "stop_daemon_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handleTerminalRestartDaemon requests a daemon restart for one terminal.
func (a *App) handleTerminalRestartDaemon(w http.ResponseWriter, r *http.Request) {
	terminalID := strings.TrimSpace(r.PathValue("id"))
	resp, err := a.callTerminalRPC(terminalID, "restart-daemon")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "restart_daemon_failed", err.Error())
		return
	}
	writeJSONRaw(w, string(resp))
}

// handlePreferencesGet returns persisted UI preferences.
func (a *App) handlePreferencesGet(w http.ResponseWriter, _ *http.Request) {
	state := a.stateSnapshot()
	writeJSON(w, http.StatusOK, state.Preferences)
}

// handlePreferencesSet updates persisted UI preferences.
func (a *App) handlePreferencesSet(w http.ResponseWriter, r *http.Request) {
	var pref webapi.Preferences
	if err := decodeJSONBody(r, &pref); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	pref = normalizePreferences(pref)
	if err := a.updateState(func(s *persistentState) {
		s.Preferences = pref
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pref)
}

// handleDebugLogs returns buffered bridge log lines.
func (a *App) handleDebugLogs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": a.logs.list()})
}

// handleDebugLogsClear clears buffered bridge log lines.
func (a *App) handleDebugLogsClear(w http.ResponseWriter, _ *http.Request) {
	a.logs.clear()
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleDebugLogServerStart starts the SDK log server and returns its URL.
func (a *App) handleDebugLogServerStart(w http.ResponseWriter, _ *http.Request) {
	url, err := a.sdkClient.StartLogServer()
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "log_server_start_failed", err.Error())
		return
	}
	a.logInfo("sdk log server started: " + url)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "url": url})
}

// handleDebugLogServerStop stops the SDK log server.
func (a *App) handleDebugLogServerStop(w http.ResponseWriter, _ *http.Request) {
	if err := a.sdkClient.StopLogServer(); err != nil {
		writeAPIError(w, http.StatusBadGateway, "log_server_stop_failed", err.Error())
		return
	}
	a.logInfo("sdk log server stopped")
	writeJSON(w, http.StatusOK, apiSuccess{Success: true})
}

// handleStream serves real-time stream events over SSE with replay support.
func (a *App) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "sse_unsupported", "streaming is not supported")
		return
	}
	since := parseInt64Default(r.URL.Query().Get("since"), 0)
	replay, resyncRequired := a.events.replaySince(since)

	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	if resyncRequired {
		_ = writeSSE(w, a.events.publishJSON("resync-required", streamPublishPayload{Reason: "cursor-too-old"}))
		flusher.Flush()
	}
	for _, event := range replay {
		if err := writeSSE(w, event); err != nil {
			return
		}
	}
	flusher.Flush()

	subID, subCh := a.events.subscribe()
	defer a.events.unsubscribe(subID)
	keepAlive := time.NewTicker(time.Duration(sseKeepAlivePeriod) * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.shutdownCtx.Done():
			return
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-subCh:
			if !ok {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// parseTerminalURLMetadata extracts host and terminal id hints from pairing URL.
func parseTerminalURLMetadata(rawURL string) (string, string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", ""
	}
	values := parsed.Query()
	host := strings.TrimSpace(values.Get("host"))
	terminalID := strings.TrimSpace(values.Get("terminalId"))
	if terminalID == "" {
		terminalID = strings.TrimSpace(values.Get("terminal_id"))
	}
	if terminalID == "" {
		terminalID = strings.TrimSpace(values.Get("machineId"))
	}
	if terminalID == "" {
		terminalID = strings.TrimSpace(values.Get("machine_id"))
	}
	return host, terminalID
}

// decodeJSONBody decodes JSON request body into dst with size limits.
func decodeJSONBody(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// writeJSON writes a JSON response with a status code.
func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// writeJSONRaw writes an already encoded JSON payload as the response body.
func writeJSONRaw(w http.ResponseWriter, payload string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, payload)
}

// writeAPIError writes a stable structured API error response.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, webapi.APIError{Code: code, Message: message})
}

// parseIntDefault parses an int value and falls back on defaultValue.
func parseIntDefault(raw string, defaultValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultValue
	}
	if value <= 0 {
		return defaultValue
	}
	return value
}

// parseInt64Default parses an int64 value and falls back on defaultValue.
func parseInt64Default(raw string, defaultValue int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return defaultValue
	}
	if value < 0 {
		return defaultValue
	}
	return value
}

// writeSSE encodes one stream event in SSE format.
func writeSSE(w io.Writer, event webapi.StreamEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\n", event.EventID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Kind); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
		return err
	}
	return nil
}
