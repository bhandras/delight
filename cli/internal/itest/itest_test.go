package itest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/storage"
	socket "github.com/zishang520/socket.io/clients/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

type testEnv struct {
	serverURL  string
	token      string
	sessionID  string
	terminalID string
	serverBuf  *bytes.Buffer
	cliBuf     *bytes.Buffer
	cleanup    func()
}

func TestFakeAgentRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	root := repoRoot(t)
	port := pickFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "delight.db")
	secret := fmt.Sprintf("itest-secret-%d", time.Now().UnixNano())

	serverCmd := exec.Command("go", "run", "./cmd/server")
	serverCmd.Dir = filepath.Join(root, "server")
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DELIGHT_MASTER_SECRET=%s", secret),
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	var serverBuf bytes.Buffer
	serverCmd.Stdout = &serverBuf
	serverCmd.Stderr = &serverBuf
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer stopProcess(t, serverCmd)

	if err := waitForPort(port, 20*time.Second); err != nil {
		t.Fatalf("server not ready: %v\nlogs:\n%s", err, serverBuf.String())
	}

	token := authToken(t, serverURL)

	delightHome := filepath.Join(tempDir, "delight-home")
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		t.Fatalf("mkdir delight home: %v", err)
	}
	if _, err := storage.GetOrCreateSecretKey(filepath.Join(delightHome, "master.key")); err != nil {
		t.Fatalf("create master.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(delightHome, "access.key"), []byte(token), 0600); err != nil {
		t.Fatalf("write access.key: %v", err)
	}

	cliCmd := exec.Command(
		"go",
		"run",
		"./cmd/delight",
		"run",
		"--server-url", serverURL,
		"--home-dir", delightHome,
		"--agent", "fake",
		"--log-level", "debug",
	)
	cliCmd.Dir = filepath.Join(root, "cli")
	cliCmd.Env = filterEnv(os.Environ(), "DEBUG")
	var cliBuf bytes.Buffer
	cliCmd.Stdout = &cliBuf
	cliCmd.Stderr = &cliBuf
	if err := cliCmd.Start(); err != nil {
		t.Fatalf("start cli: %v", err)
	}
	defer stopProcess(t, cliCmd)

	sessionID := waitForSession(t, serverURL, token, 10*time.Second)
	dataKey := waitForSessionDataEncryptionKey(t, serverURL, token, sessionID, 10*time.Second)

	sock := connectUserSocket(t, serverURL, token)
	defer sock.Close()

	userPayload := map[string]interface{}{
		"role": "user",
		"content": map[string]interface{}{
			"type": "text",
			"text": "ping",
		},
	}

	encrypted := encryptPayload(t, dataKey, userPayload)
	updateCh := make(chan map[string]interface{}, 1)
	sock.On(types.EventName("update"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			select {
			case updateCh <- data:
			default:
			}
		}
	})

	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	sendMessage := func() {
		if err := sock.Emit("message", map[string]interface{}{
			"sid":     sessionID,
			"message": encrypted,
		}); err != nil {
			t.Fatalf("emit message: %v", err)
		}
	}
	sendMessage()

	for {
		select {
		case update := <-updateCh:
			responseText, ok := decryptUpdateText(t, dataKey, update)
			if !ok {
				continue
			}
			if responseText != "fake-agent: ping" {
				t.Fatalf("unexpected response: %q\ncli logs:\n%s", responseText, cliBuf.String())
			}
			return
		case <-ticker.C:
			sendMessage()
		case <-deadline:
			t.Fatalf("timed out waiting for update\ncli logs:\n%s\nserver logs:\n%s", cliBuf.String(), serverBuf.String())
		}
	}
}

func TestArtifactSocketFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	root := repoRoot(t)
	port := pickFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "delight.db")
	secret := fmt.Sprintf("itest-secret-%d", time.Now().UnixNano())

	serverCmd := exec.Command("go", "run", "./cmd/server")
	serverCmd.Dir = filepath.Join(root, "server")
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DELIGHT_MASTER_SECRET=%s", secret),
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	var serverBuf bytes.Buffer
	serverCmd.Stdout = &serverBuf
	serverCmd.Stderr = &serverBuf
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer stopProcess(t, serverCmd)

	if err := waitForPort(port, 20*time.Second); err != nil {
		t.Fatalf("server not ready: %v\nlogs:\n%s", err, serverBuf.String())
	}

	token := authToken(t, serverURL)
	sock := connectUserSocket(t, serverURL, token)
	defer sock.Close()

	updateCh := make(chan map[string]interface{}, 4)
	sock.On(types.EventName("update"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			updateCh <- data
		}
	})

	header := base64.StdEncoding.EncodeToString([]byte("header"))
	body := base64.StdEncoding.EncodeToString([]byte("body"))
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatalf("rand data key: %v", err)
	}
	dataKeyB64 := base64.StdEncoding.EncodeToString(dataKey)
	artifactID := fmt.Sprintf("artifact-%d", time.Now().UnixNano())

	createResp := emitAck(t, sock, "artifact-create", map[string]interface{}{
		"id":                artifactID,
		"header":            header,
		"body":              body,
		"dataEncryptionKey": dataKeyB64,
	})
	if createResp["result"] != "success" {
		t.Fatalf("create artifact failed: %+v", createResp)
	}

	waitForUpdateType(t, updateCh, "new-artifact")

	readResp := emitAck(t, sock, "artifact-read", map[string]interface{}{
		"artifactId": artifactID,
	})
	if readResp["result"] != "success" {
		t.Fatalf("read artifact failed: %+v", readResp)
	}

	updateResp := emitAck(t, sock, "artifact-update", map[string]interface{}{
		"artifactId": artifactID,
		"header": map[string]interface{}{
			"data":            base64.StdEncoding.EncodeToString([]byte("header-2")),
			"expectedVersion": 1,
		},
	})
	if updateResp["result"] != "success" {
		t.Fatalf("update artifact failed: %+v", updateResp)
	}
	waitForUpdateType(t, updateCh, "update-artifact")

	deleteResp := emitAck(t, sock, "artifact-delete", map[string]interface{}{
		"artifactId": artifactID,
	})
	if deleteResp["result"] != "success" {
		t.Fatalf("delete artifact failed: %+v", deleteResp)
	}
	waitForUpdateType(t, updateCh, "delete-artifact")
}

func TestRPCRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	root := repoRoot(t)
	port := pickFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "delight.db")
	secret := fmt.Sprintf("itest-secret-%d", time.Now().UnixNano())

	serverCmd := exec.Command("go", "run", "./cmd/server")
	serverCmd.Dir = filepath.Join(root, "server")
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DELIGHT_MASTER_SECRET=%s", secret),
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	var serverBuf bytes.Buffer
	serverCmd.Stdout = &serverBuf
	serverCmd.Stderr = &serverBuf
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer stopProcess(t, serverCmd)

	if err := waitForPort(port, 20*time.Second); err != nil {
		t.Fatalf("server not ready: %v\nlogs:\n%s", err, serverBuf.String())
	}

	token := authToken(t, serverURL)

	delightHome := filepath.Join(tempDir, "delight-home")
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		t.Fatalf("mkdir delight home: %v", err)
	}
	if _, err := storage.GetOrCreateSecretKey(filepath.Join(delightHome, "master.key")); err != nil {
		t.Fatalf("create master.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(delightHome, "access.key"), []byte(token), 0600); err != nil {
		t.Fatalf("write access.key: %v", err)
	}

	cliCmd := exec.Command(
		"go",
		"run",
		"./cmd/delight",
		"run",
		"--server-url", serverURL,
		"--home-dir", delightHome,
		"--agent", "fake",
		"--log-level", "debug",
	)
	cliCmd.Dir = filepath.Join(root, "cli")
	cliCmd.Env = filterEnv(os.Environ(), "DEBUG")
	var cliBuf bytes.Buffer
	cliCmd.Stdout = &cliBuf
	cliCmd.Stderr = &cliBuf
	if err := cliCmd.Start(); err != nil {
		t.Fatalf("start cli: %v", err)
	}
	defer stopProcess(t, cliCmd)

	sessionID := waitForSessionWithLogs(t, serverURL, token, 20*time.Second, &cliBuf, &serverBuf)

	sock := connectUserSocket(t, serverURL, token)
	defer sock.Close()

	params := map[string]interface{}{
		"requestId": "itest",
		"allow":     true,
		"message":   "ok",
	}
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal rpc params: %v", err)
	}

	resp := waitForRPCMethodWithLogs(t, sock, sessionID+":permission", string(paramsBytes), 10*time.Second, &cliBuf, &serverBuf)
	ok, _ := resp["ok"].(bool)
	if !ok {
		t.Fatalf("rpc call failed: %+v\ncli logs:\n%s", resp, cliBuf.String())
	}

	result := decodeRPCResult(t, resp)
	if result == nil {
		t.Fatalf("missing rpc result: %+v", resp)
	}
	if success, _ := result["success"].(bool); !success {
		t.Fatalf("unexpected rpc result: %+v", result)
	}
}

func TestTerminalRPCRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	root := repoRoot(t)
	port := pickFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "delight.db")
	secret := fmt.Sprintf("itest-secret-%d", time.Now().UnixNano())

	serverCmd := exec.Command("go", "run", "./cmd/server")
	serverCmd.Dir = filepath.Join(root, "server")
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DELIGHT_MASTER_SECRET=%s", secret),
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	var serverBuf bytes.Buffer
	serverCmd.Stdout = &serverBuf
	serverCmd.Stderr = &serverBuf
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer stopProcess(t, serverCmd)

	if err := waitForPort(port, 20*time.Second); err != nil {
		t.Fatalf("server not ready: %v\nlogs:\n%s", err, serverBuf.String())
	}

	token := authToken(t, serverURL)

	delightHome := filepath.Join(tempDir, "delight-home")
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		t.Fatalf("mkdir delight home: %v", err)
	}
	if _, err := storage.GetOrCreateSecretKey(filepath.Join(delightHome, "master.key")); err != nil {
		t.Fatalf("create master.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(delightHome, "access.key"), []byte(token), 0600); err != nil {
		t.Fatalf("write access.key: %v", err)
	}

	cliCmd := exec.Command(
		"go",
		"run",
		"./cmd/delight",
		"run",
		"--server-url", serverURL,
		"--home-dir", delightHome,
		"--agent", "fake",
		"--log-level", "debug",
	)
	cliCmd.Dir = filepath.Join(root, "cli")
	cliCmd.Env = filterEnv(os.Environ(), "DEBUG")

	terminalID, err := storage.GetOrCreateTerminalID(delightHome, cliCmd.Dir)
	if err != nil {
		t.Fatalf("get terminal id: %v", err)
	}

	var cliBuf bytes.Buffer
	cliCmd.Stdout = &cliBuf
	cliCmd.Stderr = &cliBuf
	if err := cliCmd.Start(); err != nil {
		t.Fatalf("start cli: %v", err)
	}
	defer stopProcess(t, cliCmd)

	_ = waitForSessionWithLogs(t, serverURL, token, 20*time.Second, &cliBuf, &serverBuf)

	sock := connectUserSocket(t, serverURL, token)
	defer sock.Close()

	paramsBytes, err := json.Marshal(map[string]interface{}{})
	if err != nil {
		t.Fatalf("marshal rpc params: %v", err)
	}

	resp := waitForRPCMethodWithLogs(t, sock, terminalID+":ping", string(paramsBytes), 20*time.Second, &cliBuf, &serverBuf)
	ok, _ := resp["ok"].(bool)
	if !ok {
		t.Fatalf("rpc call failed: %+v\ncli logs:\n%s", resp, cliBuf.String())
	}
	result := decodeRPCResult(t, resp)
	if result == nil || result["success"] != true {
		t.Fatalf("unexpected rpc result: %+v", result)
	}
}

func TestUpdateStateAckMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := startServerAndCLI(t)
	defer env.cleanup()

	sock := connectSessionSocket(t, env.serverURL, env.token, env.sessionID)
	defer sock.Close()

	resp := emitAck(t, sock, "update-state", map[string]interface{}{
		"sid":        env.sessionID,
		"agentState": "itest-state",
		// CLI now persists an initial agentState on connect, so version starts at >=1.
		// Use an obviously stale version to force a mismatch.
		"expectedVersion": int64(0),
	})
	if result, _ := resp["result"].(string); result != "version-mismatch" {
		t.Fatalf("expected version-mismatch, got: %+v", resp)
	}
}

func TestSessionAliveEphemeral(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := startServerAndCLI(t)
	defer env.cleanup()

	userSock := connectUserSocket(t, env.serverURL, env.token)
	defer userSock.Close()

	ephemeralCh := make(chan map[string]interface{}, 2)
	userSock.On(types.EventName("ephemeral"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			ephemeralCh <- data
		}
	})

	sessionSock := connectSessionSocket(t, env.serverURL, env.token, env.sessionID)
	defer sessionSock.Close()

	if err := sessionSock.Emit("session-alive", map[string]interface{}{
		"sid":      env.sessionID,
		"time":     time.Now().UnixMilli(),
		"thinking": true,
	}); err != nil {
		t.Fatalf("emit session-alive: %v", err)
	}

	waitForEphemeralType(t, ephemeralCh, "activity", env.sessionID)
}

func TestUsageReportEphemeral(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := startServerAndCLI(t)
	defer env.cleanup()

	userSock := connectUserSocket(t, env.serverURL, env.token)
	defer userSock.Close()

	ephemeralCh := make(chan map[string]interface{}, 2)
	userSock.On(types.EventName("ephemeral"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			ephemeralCh <- data
		}
	})

	sessionSock := connectSessionSocket(t, env.serverURL, env.token, env.sessionID)
	defer sessionSock.Close()

	if err := sessionSock.Emit("usage-report", map[string]interface{}{
		"sessionId": env.sessionID,
		"key":       "itest",
		"tokens":    map[string]interface{}{"input": 5, "output": 7},
		"cost":      map[string]interface{}{"input": 1, "output": 2},
	}); err != nil {
		t.Fatalf("emit usage-report: %v", err)
	}

	waitForEphemeralType(t, ephemeralCh, "usage", env.sessionID)
}

func TestTerminalAliveEphemeral(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	env := startServerAndCLI(t)
	defer env.cleanup()

	userSock := connectUserSocket(t, env.serverURL, env.token)
	defer userSock.Close()

	ephemeralCh := make(chan map[string]interface{}, 2)
	userSock.On(types.EventName("ephemeral"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			ephemeralCh <- data
		}
	})

	terminalSock := connectTerminalSocket(t, env.serverURL, env.token, env.terminalID)
	defer terminalSock.Close()

	if err := terminalSock.Emit("terminal-alive", map[string]interface{}{
		"terminalId": env.terminalID,
		"time":       time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("emit terminal-alive: %v", err)
	}

	waitForEphemeralType(t, ephemeralCh, "terminal-activity", env.terminalID)
}

func TestACPFlowWithAwait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	acp := newACPMockServer(t)
	defer acp.server.Close()

	root := repoRoot(t)
	port := pickFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "delight.db")
	secret := fmt.Sprintf("itest-secret-%d", time.Now().UnixNano())

	serverCmd := exec.Command("go", "run", "./cmd/server")
	serverCmd.Dir = filepath.Join(root, "server")
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DELIGHT_MASTER_SECRET=%s", secret),
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	var serverBuf bytes.Buffer
	serverCmd.Stdout = &serverBuf
	serverCmd.Stderr = &serverBuf
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer stopProcess(t, serverCmd)

	if err := waitForPort(port, 20*time.Second); err != nil {
		t.Fatalf("server not ready: %v\nlogs:\n%s", err, serverBuf.String())
	}

	token := authToken(t, serverURL)

	delightHome := filepath.Join(tempDir, "delight-home")
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		t.Fatalf("mkdir delight home: %v", err)
	}
	if _, err := storage.GetOrCreateSecretKey(filepath.Join(delightHome, "master.key")); err != nil {
		t.Fatalf("create master.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(delightHome, "access.key"), []byte(token), 0600); err != nil {
		t.Fatalf("write access.key: %v", err)
	}

	cliCmd := exec.Command(
		"go",
		"run",
		"./cmd/delight",
		"run",
		"--server-url", serverURL,
		"--home-dir", delightHome,
		"--agent", "acp",
		"--acp-url", acp.server.URL,
		"--acp-agent", "test-agent",
		"--log-level", "debug",
	)
	cliCmd.Dir = filepath.Join(root, "cli")
	cliCmd.Env = filterEnv(os.Environ(), "DEBUG")
	var cliBuf bytes.Buffer
	cliCmd.Stdout = &cliBuf
	cliCmd.Stderr = &cliBuf
	if err := cliCmd.Start(); err != nil {
		t.Fatalf("start cli: %v", err)
	}
	defer stopProcess(t, cliCmd)

	sessionID := waitForSessionWithLogs(t, serverURL, token, 20*time.Second, &cliBuf, &serverBuf)
	dataKey := waitForSessionDataEncryptionKey(t, serverURL, token, sessionID, 10*time.Second)

	userSock := connectUserSocket(t, serverURL, token)
	defer userSock.Close()

	ephemeralCh := make(chan map[string]interface{}, 4)
	userSock.On(types.EventName("ephemeral"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			ephemeralCh <- data
		}
	})

	updateCh := make(chan map[string]interface{}, 8)
	userSock.On(types.EventName("update"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if data, ok := args[0].(map[string]interface{}); ok {
			updateCh <- data
		}
	})

	userPayload := map[string]interface{}{
		"role": "user",
		"content": map[string]interface{}{
			"type": "text",
			"text": "ping",
		},
	}
	encrypted := encryptPayload(t, dataKey, userPayload)
	if err := userSock.Emit("message", map[string]interface{}{
		"sid":     sessionID,
		"message": encrypted,
	}); err != nil {
		t.Fatalf("emit message: %v", err)
	}

	requestID := waitForPermissionRequest(t, ephemeralCh, sessionID, &cliBuf, &serverBuf)
	paramsBytes, err := json.Marshal(map[string]interface{}{
		"requestId": requestID,
		"allow":     true,
		"message":   "ok",
	})
	if err != nil {
		t.Fatalf("marshal permission params: %v", err)
	}
	resp := waitForRPCMethodWithLogs(t, userSock, sessionID+":permission", string(paramsBytes), 10*time.Second, &cliBuf, &serverBuf)
	ok, _ := resp["ok"].(bool)
	if !ok {
		t.Fatalf("permission rpc failed: %+v\ncli logs:\n%s", resp, cliBuf.String())
	}

	waitForAwaitResume(t, acp, 5*time.Second)
	waitForResumeDone(t, acp, 5*time.Second)

	responseText := waitForAssistantText(t, updateCh, dataKey)
	if responseText != "acp: ping (ok)" {
		t.Fatalf("unexpected ACP response: %q\ncli logs:\n%s", responseText, cliBuf.String())
	}

	awaitResume := acp.getAwaitResume()
	if allow, ok := awaitResume["allow"].(bool); !ok || !allow {
		t.Fatalf("await resume not recorded: %+v", awaitResume)
	}
	if message, ok := awaitResume["message"].(string); !ok || message != "ok" {
		t.Fatalf("await resume message mismatch: %+v", awaitResume)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("port %d not ready", port)
}

func startServerAndCLI(t *testing.T) *testEnv {
	t.Helper()

	root := repoRoot(t)
	port := pickFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%d", port)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "delight.db")
	secret := fmt.Sprintf("itest-secret-%d", time.Now().UnixNano())

	serverCmd := exec.Command("go", "run", "./cmd/server")
	serverCmd.Dir = filepath.Join(root, "server")
	serverCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DELIGHT_MASTER_SECRET=%s", secret),
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	serverBuf := &bytes.Buffer{}
	serverCmd.Stdout = serverBuf
	serverCmd.Stderr = serverBuf
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	if err := waitForPort(port, 20*time.Second); err != nil {
		stopProcess(t, serverCmd)
		t.Fatalf("server not ready: %v\nlogs:\n%s", err, serverBuf.String())
	}

	token := authToken(t, serverURL)

	delightHome := filepath.Join(tempDir, "delight-home")
	if err := os.MkdirAll(delightHome, 0700); err != nil {
		stopProcess(t, serverCmd)
		t.Fatalf("mkdir delight home: %v", err)
	}
	if _, err := storage.GetOrCreateSecretKey(filepath.Join(delightHome, "master.key")); err != nil {
		stopProcess(t, serverCmd)
		t.Fatalf("create master.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(delightHome, "access.key"), []byte(token), 0600); err != nil {
		stopProcess(t, serverCmd)
		t.Fatalf("write access.key: %v", err)
	}

	cliCmd := exec.Command(
		"go",
		"run",
		"./cmd/delight",
		"run",
		"--server-url", serverURL,
		"--home-dir", delightHome,
		"--agent", "fake",
		"--log-level", "debug",
	)
	cliCmd.Dir = filepath.Join(root, "cli")
	cliCmd.Env = filterEnv(os.Environ(), "DEBUG")

	terminalID, err := storage.GetOrCreateTerminalID(delightHome, cliCmd.Dir)
	if err != nil {
		stopProcess(t, serverCmd)
		t.Fatalf("get terminal id: %v", err)
	}

	cliBuf := &bytes.Buffer{}
	cliCmd.Stdout = cliBuf
	cliCmd.Stderr = cliBuf
	if err := cliCmd.Start(); err != nil {
		stopProcess(t, serverCmd)
		t.Fatalf("start cli: %v", err)
	}

	sessionID := waitForSessionWithLogs(t, serverURL, token, 20*time.Second, cliBuf, serverBuf)

	env := &testEnv{
		serverURL:  serverURL,
		token:      token,
		sessionID:  sessionID,
		terminalID: terminalID,
		serverBuf:  serverBuf,
		cliBuf:     cliBuf,
	}
	env.cleanup = func() {
		stopProcess(t, cliCmd)
		stopProcess(t, serverCmd)
	}
	return env
}

func authToken(t *testing.T, serverURL string) string {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second}

	publicKey, privateKey, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	challenge := []byte("delight-auth-challenge")
	signature, err := crypto.Sign(privateKey, challenge)
	if err != nil {
		t.Fatalf("sign challenge: %v", err)
	}

	reqBody := map[string]string{
		"publicKey": base64.StdEncoding.EncodeToString(publicKey),
		"challenge": base64.StdEncoding.EncodeToString(challenge),
		"signature": base64.StdEncoding.EncodeToString(signature),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal auth request: %v", err)
	}

	req, err := http.NewRequest("POST", serverURL+"/v1/auth", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("auth request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("auth call: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth status: %s", resp.Status)
	}

	var response struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if !response.Success || response.Token == "" {
		t.Fatal("missing auth token")
	}

	return response.Token
}

func waitForSession(t *testing.T, serverURL, token string, timeout time.Duration) string {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}

	type session struct {
		ID string `json:"id"`
	}
	type response struct {
		Sessions []session `json:"sessions"`
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", serverURL+"/v1/sessions", nil)
		if err != nil {
			t.Fatalf("session request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var payload response
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
			resp.Body.Close()
			if len(payload.Sessions) > 0 && payload.Sessions[0].ID != "" {
				return payload.Sessions[0].ID
			}
		} else {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	t.Fatal("timed out waiting for session")
	return ""
}

func waitForSessionWithLogs(t *testing.T, serverURL, token string, timeout time.Duration, cliBuf, serverBuf *bytes.Buffer) string {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}

	type session struct {
		ID string `json:"id"`
	}
	type response struct {
		Sessions []session `json:"sessions"`
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", serverURL+"/v1/sessions", nil)
		if err != nil {
			t.Fatalf("session request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var payload response
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
			resp.Body.Close()
			if len(payload.Sessions) > 0 && payload.Sessions[0].ID != "" {
				return payload.Sessions[0].ID
			}
		} else {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	cliLogs := ""
	if cliBuf != nil {
		cliLogs = cliBuf.String()
	}
	serverLogs := ""
	if serverBuf != nil {
		serverLogs = serverBuf.String()
	}
	t.Fatalf("timed out waiting for session\ncli logs:\n%s\nserver logs:\n%s", cliLogs, serverLogs)
	return ""
}

func waitForSessionDataEncryptionKey(t *testing.T, serverURL, token, sessionID string, timeout time.Duration) []byte {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}

	type session struct {
		ID                string  `json:"id"`
		DataEncryptionKey *string `json:"dataEncryptionKey"`
	}
	type response struct {
		Sessions []session `json:"sessions"`
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", serverURL+"/v1/sessions", nil)
		if err != nil {
			t.Fatalf("session request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var payload response
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		resp.Body.Close()
		for _, s := range payload.Sessions {
			if s.ID != sessionID {
				continue
			}
			if s.DataEncryptionKey == nil || *s.DataEncryptionKey == "" {
				break
			}
			raw, err := base64.StdEncoding.DecodeString(*s.DataEncryptionKey)
			if err != nil {
				t.Fatalf("decode dataEncryptionKey: %v", err)
			}
			if len(raw) != 32 {
				t.Fatalf("invalid dataEncryptionKey length: %d", len(raw))
			}
			return raw
		}
		time.Sleep(200 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for session dataEncryptionKey (sessionID=%s)", sessionID)
	return nil
}

func connectUserSocket(t *testing.T, serverURL, token string) *socket.Socket {
	t.Helper()

	opts := socket.DefaultOptions()
	opts.SetPath("/v1/updates")
	opts.SetTransports(types.NewSet(socket.Polling, socket.WebSocket))
	opts.SetAuth(map[string]interface{}{
		"token":      token,
		"clientType": "user-scoped",
	})

	sock := mustConnectSocket(t, serverURL, opts)
	if sock.Connected() {
		return sock
	}

	errCh := make(chan string, 1)
	sock.On(types.EventName("connect_error"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if msg, ok := args[0].(string); ok {
			select {
			case errCh <- msg:
			default:
			}
			return
		}
		select {
		case errCh <- fmt.Sprintf("%v", args[0]):
		default:
		}
	})

	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case msg := <-errCh:
			t.Fatalf("socket connect error: %s", msg)
		case <-deadline:
			t.Fatal("socket connect timeout")
		case <-ticker.C:
			if sock.Connected() {
				return sock
			}
		}
	}

	return sock
}

func connectSessionSocket(t *testing.T, serverURL, token, sessionID string) *socket.Socket {
	t.Helper()

	opts := socket.DefaultOptions()
	opts.SetPath("/v1/updates")
	opts.SetTransports(types.NewSet(socket.Polling, socket.WebSocket))
	opts.SetAuth(map[string]interface{}{
		"token":      token,
		"clientType": "session-scoped",
		"sessionId":  sessionID,
	})

	sock := mustConnectSocket(t, serverURL, opts)
	if sock.Connected() {
		return sock
	}

	errCh := make(chan string, 1)
	sock.On(types.EventName("connect_error"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if msg, ok := args[0].(string); ok {
			select {
			case errCh <- msg:
			default:
			}
			return
		}
		select {
		case errCh <- fmt.Sprintf("%v", args[0]):
		default:
		}
	})

	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case msg := <-errCh:
			t.Fatalf("socket connect error: %s", msg)
		case <-deadline:
			t.Fatal("socket connect timeout")
		case <-ticker.C:
			if sock.Connected() {
				return sock
			}
		}
	}

	return sock
}

func connectTerminalSocket(t *testing.T, serverURL, token, terminalID string) *socket.Socket {
	t.Helper()

	opts := socket.DefaultOptions()
	opts.SetPath("/v1/updates")
	opts.SetTransports(types.NewSet(socket.Polling, socket.WebSocket))
	opts.SetAuth(map[string]interface{}{
		"token":      token,
		"clientType": "terminal-scoped",
		"terminalId": terminalID,
	})

	sock := mustConnectSocket(t, serverURL, opts)
	if sock.Connected() {
		return sock
	}

	errCh := make(chan string, 1)
	sock.On(types.EventName("connect_error"), func(args ...any) {
		if len(args) == 0 {
			return
		}
		if msg, ok := args[0].(string); ok {
			select {
			case errCh <- msg:
			default:
			}
			return
		}
		select {
		case errCh <- fmt.Sprintf("%v", args[0]):
		default:
		}
	})

	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case msg := <-errCh:
			t.Fatalf("socket connect error: %s", msg)
		case <-deadline:
			t.Fatal("socket connect timeout")
		case <-ticker.C:
			if sock.Connected() {
				return sock
			}
		}
	}

	return sock
}

func mustConnectSocket(t *testing.T, serverURL string, opts *socket.Options) *socket.Socket {
	t.Helper()

	type result struct {
		sock *socket.Socket
		err  error
	}
	resCh := make(chan result, 1)

	go func() {
		sock, err := socket.Connect(serverURL, opts)
		resCh <- result{sock: sock, err: err}
	}()

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("socket connect: %v", res.err)
		}
		if res.sock == nil {
			t.Fatal("socket connect returned nil socket")
		}
		return res.sock
	case <-time.After(10 * time.Second):
		t.Fatalf("socket connect timeout (%s)", serverURL)
		return nil
	}
}

func encryptPayload(t *testing.T, dataKey []byte, payload interface{}) string {
	t.Helper()
	encrypted, err := crypto.EncryptWithDataKey(payload, dataKey)
	if err != nil {
		t.Fatalf("encrypt payload: %v", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted)
}

func decryptUpdateText(t *testing.T, dataKey []byte, update map[string]interface{}) (string, bool) {
	t.Helper()

	decrypted, ok := decryptUpdatePayload(t, dataKey, update)
	if !ok {
		return "", false
	}

	text := extractAssistantText(decrypted)
	if text == "" {
		return "", false
	}
	return text, true
}

func decryptUpdatePayload(t *testing.T, dataKey []byte, update map[string]interface{}) (interface{}, bool) {
	t.Helper()

	body, _ := update["body"].(map[string]interface{})
	if body == nil {
		return nil, false
	}
	message, _ := body["message"].(map[string]interface{})
	if message == nil {
		return nil, false
	}
	content, _ := message["content"].(map[string]interface{})
	if content == nil {
		return nil, false
	}
	cipher, _ := content["c"].(string)
	if cipher == "" {
		return nil, false
	}

	encrypted, err := base64.StdEncoding.DecodeString(cipher)
	if err != nil {
		t.Fatalf("decode cipher: %v", err)
	}

	var decrypted interface{}
	if err := crypto.DecryptWithDataKey(encrypted, dataKey, &decrypted); err != nil {
		t.Fatalf("decrypt payload: %v", err)
	}

	return decrypted, true
}

func extractAssistantText(payload interface{}) string {
	switch v := payload.(type) {
	case map[string]interface{}:
		role, _ := v["role"].(string)
		if !strings.HasPrefix(role, "agent") {
			return ""
		}
		content, _ := v["content"].(map[string]interface{})
		data, _ := content["data"].(map[string]interface{})
		message, _ := data["message"].(map[string]interface{})
		switch blocks := message["content"].(type) {
		case []interface{}:
			if len(blocks) == 0 {
				return ""
			}
			first, _ := blocks[0].(map[string]interface{})
			text, _ := first["text"].(string)
			return text
		case map[string]interface{}:
			text, _ := blocks["text"].(string)
			return text
		default:
			return ""
		}
	case string:
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
			var nested map[string]interface{}
			if err := json.Unmarshal(decoded, &nested); err == nil {
				return extractAssistantText(nested)
			}
		}
		var nested map[string]interface{}
		if err := json.Unmarshal([]byte(v), &nested); err == nil {
			return extractAssistantText(nested)
		}
		return v
	default:
		return ""
	}
}

func stopProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
	}
}

func emitAck(t *testing.T, sock *socket.Socket, event string, payload map[string]interface{}) map[string]interface{} {
	t.Helper()
	return emitAckWithTimeout(t, sock, event, payload, 5*time.Second)
}

func emitAckWithTimeout(t *testing.T, sock *socket.Socket, event string, payload map[string]interface{}, timeout time.Duration) map[string]interface{} {
	t.Helper()
	resp, err := emitAckMaybe(sock, event, payload, timeout)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return resp
}

func emitAckMaybe(sock *socket.Socket, event string, payload map[string]interface{}, timeout time.Duration) (map[string]interface{}, error) {
	done := make(chan map[string]interface{}, 1)
	errCh := make(chan error, 1)
	if err := sock.Emit(event, payload, func(args []any, err error) {
		if err != nil {
			errCh <- err
			return
		}
		if len(args) == 0 {
			done <- map[string]interface{}{}
			return
		}
		if resp, ok := args[0].(map[string]interface{}); ok {
			done <- resp
			return
		}
		done <- map[string]interface{}{}
	}); err != nil {
		return nil, fmt.Errorf("emit %s: %v", event, err)
	}

	select {
	case err := <-errCh:
		return nil, fmt.Errorf("ack %s: %v", event, err)
	case resp := <-done:
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("ack timeout: %s", event)
	}
}

func waitForEphemeralType(t *testing.T, updateCh <-chan map[string]interface{}, updateType, id string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updateCh:
			tval, _ := update["type"].(string)
			if tval != updateType {
				continue
			}
			if id == "" {
				return
			}
			if updateID, _ := update["id"].(string); updateID == id {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for ephemeral type %s", updateType)
		}
	}
}

func getInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func waitForRPCMethod(t *testing.T, sock *socket.Socket, method, params string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := emitAckMaybe(sock, "rpc-call", map[string]interface{}{
			"method": method,
			"params": params,
		}, 20*time.Second)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if ok, _ := resp["ok"].(bool); ok {
			return resp
		}
		if errMsg, _ := resp["error"].(string); errMsg != "" && errMsg != "RPC method not available" {
			return resp
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("rpc method not available: %s", method)
	return nil
}

func waitForRPCMethodWithLogs(t *testing.T, sock *socket.Socket, method, params string, timeout time.Duration, cliBuf, serverBuf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastResp map[string]interface{}
	for time.Now().Before(deadline) {
		resp, err := emitAckMaybe(sock, "rpc-call", map[string]interface{}{
			"method": method,
			"params": params,
		}, 20*time.Second)
		if err != nil {
			cliLogs := ""
			if cliBuf != nil {
				cliLogs = cliBuf.String()
			}
			serverLogs := ""
			if serverBuf != nil {
				serverLogs = serverBuf.String()
			}
			t.Fatalf("%v\ncli logs:\n%s\nserver logs:\n%s", err, cliLogs, serverLogs)
		}
		lastResp = resp
		if ok, _ := resp["ok"].(bool); ok {
			return resp
		}
		if errMsg, _ := resp["error"].(string); errMsg != "" && errMsg != "RPC method not available" {
			return resp
		}
		time.Sleep(200 * time.Millisecond)
	}

	cliLogs := ""
	if cliBuf != nil {
		cliLogs = cliBuf.String()
	}
	serverLogs := ""
	if serverBuf != nil {
		serverLogs = serverBuf.String()
	}
	t.Fatalf("rpc method not available: %s lastResponse=%v\ncli logs:\n%s\nserver logs:\n%s", method, lastResp, cliLogs, serverLogs)
	return nil
}

func decodeRPCResult(t *testing.T, resp map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw := resp["result"]
	switch v := raw.(type) {
	case map[string]interface{}:
		return v
	case []byte:
		var out map[string]interface{}
		if err := json.Unmarshal(v, &out); err != nil {
			t.Fatalf("unmarshal rpc result bytes: %v", err)
		}
		return out
	case string:
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			t.Fatalf("unmarshal rpc result string: %v", err)
		}
		return out
	default:
		return nil
	}
}

func waitForUpdateType(t *testing.T, updateCh <-chan map[string]interface{}, updateType string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updateCh:
			body, _ := update["body"].(map[string]interface{})
			if body == nil {
				continue
			}
			if tval, _ := body["t"].(string); tval == updateType {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for update type %s", updateType)
		}
	}
}

func waitForUpdate(t *testing.T, updateCh <-chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case update := <-updateCh:
			return update
		case <-deadline:
			t.Fatal("timeout waiting for update")
		}
	}
}

func waitForAssistantText(t *testing.T, updateCh <-chan map[string]interface{}, dataKey []byte) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	var lastPayload interface{}
	for {
		select {
		case update := <-updateCh:
			payload, ok := decryptUpdatePayload(t, dataKey, update)
			if !ok {
				continue
			}
			lastPayload = payload
			if text := extractAssistantText(payload); text != "" {
				return text
			}
		case <-deadline:
			if lastPayload != nil {
				pretty, _ := json.Marshal(lastPayload)
				t.Fatalf("timeout waiting for assistant response (last payload: %s)", pretty)
			}
			t.Fatal("timeout waiting for assistant response")
		}
	}
}

func waitForPermissionRequest(
	t *testing.T,
	ephemeralCh <-chan map[string]interface{},
	sessionID string,
	cliBuf *bytes.Buffer,
	serverBuf *bytes.Buffer,
) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	var lastEp map[string]interface{}
	for {
		select {
		case ep := <-ephemeralCh:
			lastEp = ep
			tval, _ := ep["type"].(string)
			if tval != "permission-request" {
				continue
			}
			if id, _ := ep["id"].(string); id != sessionID {
				continue
			}
			requestID, _ := ep["requestId"].(string)
			if requestID != "" {
				return requestID
			}
		case <-deadline:
			if lastEp != nil {
				pretty, _ := json.Marshal(lastEp)
				t.Fatalf(
					"timeout waiting for permission-request (last ephemeral: %s)\ncli logs:\n%s\nserver logs:\n%s",
					pretty,
					cliBuf.String(),
					serverBuf.String(),
				)
			}
			t.Fatalf(
				"timeout waiting for permission-request\ncli logs:\n%s\nserver logs:\n%s",
				cliBuf.String(),
				serverBuf.String(),
			)
		}
	}
}

func waitForAwaitResume(t *testing.T, acp *acpMockServer, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(acp.getAwaitResume()) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for ACP resume request")
}

func waitForResumeDone(t *testing.T, acp *acpMockServer, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if acp.isResumeDone() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for ACP resume response")
}

type acpMockServer struct {
	t           *testing.T
	server      *httptest.Server
	mu          sync.Mutex
	awaitResume map[string]interface{}
	resumeDone  bool
}

func newACPMockServer(t *testing.T) *acpMockServer {
	t.Helper()
	mock := &acpMockServer{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/runs", mock.handleRuns)
	mux.HandleFunc("/runs/", mock.handleRun)
	mock.server = httptest.NewServer(mux)
	return mock
}

func (m *acpMockServer) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AgentName string                   `json:"agent_name"`
		SessionID string                   `json:"session_id"`
		Input     []map[string]interface{} `json:"input"`
		Mode      string                   `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.AgentName == "" || len(req.Input) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	runID := fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()%0xffffffffffff)
	response := map[string]interface{}{
		"agent_name":    req.AgentName,
		"run_id":        runID,
		"status":        "awaiting",
		"await_request": map[string]interface{}{"reason": "permission"},
		"output":        []interface{}{},
		"created_at":    time.Now().Format(time.RFC3339Nano),
	}
	writeJSONResponse(w, response)
	m.mu.Lock()
	m.resumeDone = true
	m.mu.Unlock()
}

func (m *acpMockServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RunID       string                 `json:"run_id"`
		AwaitResume map[string]interface{} `json:"await_resume"`
		Mode        string                 `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.awaitResume = req.AwaitResume
	m.mu.Unlock()

	output := []map[string]interface{}{
		{
			"role": "agent/test-agent",
			"parts": []map[string]interface{}{
				{
					"content_type": "text/plain",
					"content":      "acp: ping (ok)",
				},
			},
		},
	}

	response := map[string]interface{}{
		"agent_name":    "test-agent",
		"run_id":        req.RunID,
		"status":        "completed",
		"await_request": nil,
		"output":        output,
		"created_at":    time.Now().Format(time.RFC3339Nano),
		"finished_at":   time.Now().Format(time.RFC3339Nano),
	}

	writeJSONResponse(w, response)
}

func (m *acpMockServer) getAwaitResume() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.awaitResume == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(m.awaitResume))
	for key, val := range m.awaitResume {
		out[key] = val
	}
	return out
}

func (m *acpMockServer) isResumeDone() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resumeDone
}

func writeJSONResponse(w http.ResponseWriter, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

func filterEnv(env []string, keys ...string) []string {
	if len(keys) == 0 {
		return env
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key+"="] = struct{}{}
	}

	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		skip := false
		for key := range keySet {
			if strings.HasPrefix(entry, key) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
