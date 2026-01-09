package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestClaudeRemoteBridgeAppliesModelViaControlRequest ensures the remote bridge
// applies model selection by sending a stream-json control_request (required for
// resumed sessions where `--model` may be ignored).
func TestClaudeRemoteBridgeAppliesModelViaControlRequest(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "fake_claude_code_cli.js")
	if err := os.WriteFile(fakeClaude, []byte(fakeClaudeCodeCLI), 0o700); err != nil {
		t.Fatalf("write fake claude cli: %v", err)
	}

	bridgePath := filepath.Join("..", "..", "scripts", "claude_remote_bridge.cjs")
	cmd := exec.CommandContext(ctx, node, bridgePath, "--cwd", tmp)
	cmd.Env = append(os.Environ(),
		"DELIGHT_CLAUDE_CLI="+fakeClaude,
		"CLAUDE_CONFIG_DIR="+tmp,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Drain stderr to avoid blocking on verbose output.
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()

	events := make(chan map[string]any, 64)
	go readJSONLines(t, stdout, events)

	waitForType(t, ctx, events, "ready")

	wantModel := "claude-haiku-4-5"
	user := map[string]any{
		"type":    "user",
		"content": "hi",
		"meta": map[string]any{
			"model":          wantModel,
			"permissionMode": "default",
		},
	}
	if err := writeJSONLine(stdin, user); err != nil {
		t.Fatalf("write user: %v", err)
	}

	var sawControlResponse bool
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		case msg := <-events:
			if msgType, _ := msg["type"].(string); msgType == "control_response" {
				sawControlResponse = true
			}
			model := extractRawAssistantModel(msg)
			if model == "" {
				continue
			}
			if model != wantModel {
				t.Fatalf("raw assistant model=%q, want %q", model, wantModel)
			}
			if sawControlResponse {
				t.Fatalf("unexpected control_response forwarded to Go")
			}
			if err := writeJSONLine(stdin, map[string]any{"type": "shutdown"}); err != nil {
				t.Fatalf("write shutdown: %v", err)
			}
			return
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	t.Fatalf("expected a raw assistant message")
}

// readJSONLines parses line-delimited JSON objects and forwards them to ch.
func readJSONLines(t *testing.T, r io.Reader, ch chan<- map[string]any) {
	t.Helper()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			// Ignore malformed output; the bridge uses stderr for logs.
			continue
		}
		ch <- msg
	}
}

// waitForType blocks until it sees a JSON message with `type` equal to want.
func waitForType(t *testing.T, ctx context.Context, ch <-chan map[string]any, want string) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for %q: %v", want, ctx.Err())
		case msg := <-ch:
			if typ, _ := msg["type"].(string); typ == want {
				return
			}
		}
	}
}

// writeJSONLine writes a single JSON object followed by a newline.
func writeJSONLine(w io.Writer, msg any) error {
	enc, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\n", enc); err != nil {
		return err
	}
	return nil
}

// extractRawAssistantModel returns the model field from a raw assistant record,
// or "" if msg is not a raw assistant record.
func extractRawAssistantModel(msg map[string]any) string {
	if typ, _ := msg["type"].(string); typ != "raw" {
		return ""
	}
	root, _ := msg["message"].(map[string]any)
	content, _ := root["content"].(map[string]any)
	data, _ := content["data"].(map[string]any)
	if kind, _ := data["type"].(string); kind != "assistant" {
		return ""
	}
	message, _ := data["message"].(map[string]any)
	model, _ := message["model"].(string)
	return model
}

const fakeClaudeCodeCLI = `#!/usr/bin/env node
// Minimal stream-json compatible fake "claude-code" entrypoint for bridge tests.
const readline = require('readline');

let currentModel = '';

function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + '\n');
}

emit({ type: 'system', subtype: 'init', session_id: 'sess-test' });

const rl = readline.createInterface({ input: process.stdin, terminal: false });
rl.on('line', (line) => {
  if (!line.trim()) return;
  let msg;
  try { msg = JSON.parse(line); } catch { return; }

  if (msg.type === 'control_request') {
    if (msg.request && msg.request.subtype === 'set_model') {
      currentModel = msg.request.model;
    }
    emit({
      type: 'control_response',
      response: {
        subtype: 'success',
        request_id: msg.request_id,
        response: {}
      }
    });
    return;
  }

  if (msg.type === 'user') {
    emit({ type: 'result', result: 'ok', model: currentModel || 'unknown' });
  }
});
`
