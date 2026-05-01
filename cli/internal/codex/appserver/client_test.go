package appserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestClientReadStdoutHandlesLargeJSONL ensures the app-server client can read
// notifications that exceed bufio.Scanner's default token limit.
func TestClientReadStdoutHandlesLargeJSONL(t *testing.T) {
	// 96KiB of payload is enough to exceed the default 64KiB scanner limit.
	largeDelta := bytes.Repeat([]byte("x"), 96*1024)

	params, err := json.Marshal(map[string]any{
		"itemId": "it_1",
		"delta":  string(largeDelta),
	})
	if err != nil {
		t.Fatalf("Marshal params: %v", err)
	}

	line, err := json.Marshal(map[string]any{
		"method": "item/agentMessage/delta",
		"params": json.RawMessage(params),
	})
	if err != nil {
		t.Fatalf("Marshal message: %v", err)
	}
	line = append(line, '\n')

	client := NewClient(false)
	client.stdout = ioNopCloser{bytes.NewReader(line)}

	var notifyCount atomic.Int64
	client.SetNotificationHandler(func(method string, raw json.RawMessage) {
		if method != "item/agentMessage/delta" {
			return
		}
		notifyCount.Add(1)
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		client.readStdout()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for readStdout to complete")
	}

	if got := notifyCount.Load(); got != 1 {
		t.Fatalf("expected 1 notification, got %d", got)
	}
}

// ioNopCloser adapts a Reader into an io.ReadCloser.
type ioNopCloser struct {
	*bytes.Reader
}

// Close implements io.Closer.
func (c ioNopCloser) Close() error { return nil }

// TestReadJSONLLineDropsOversizedLine ensures an oversized JSONL line is
// discarded without preventing subsequent reads.
func TestReadJSONLLineDropsOversizedLine(t *testing.T) {
	var stream bytes.Buffer
	stream.WriteString(`{"method":"too_big","params":{"x":"`)
	stream.Write(bytes.Repeat([]byte("x"), 1024))
	stream.WriteString(`"}}` + "\n")
	stream.WriteString(`{"method":"ok","params":{"y":1}}` + "\n")

	r := bufio.NewReader(&stream)

	line, tooLong, err := readJSONLLine(r, 128)
	if err != nil {
		t.Fatalf("readJSONLLine(oversized) err: %v", err)
	}
	if !tooLong {
		t.Fatalf("expected tooLong=true for oversized line, got line len=%d", len(line))
	}
	if len(line) != 128 {
		t.Fatalf("expected retained prefix len 128, got %d", len(line))
	}

	line, tooLong, err = readJSONLLine(r, 128)
	if err != nil {
		t.Fatalf("readJSONLLine(next) err: %v", err)
	}
	if tooLong {
		t.Fatalf("expected tooLong=false for second line")
	}
	if got := string(line); got != `{"method":"ok","params":{"y":1}}` {
		t.Fatalf("unexpected second line: %q", got)
	}
}

// TestParseJSONRPCResponseIDFromPrefix extracts an id that appears before a
// truncated result payload.
func TestParseJSONRPCResponseIDFromPrefix(t *testing.T) {
	id, ok := parseJSONRPCResponseIDFromPrefix([]byte(`{"jsonrpc":"2.0","id":42,"result":{"huge":"`))
	if !ok {
		t.Fatalf("expected id to be parsed from prefix")
	}
	if got := string(id); got != "42" {
		t.Fatalf("expected id 42, got %q", got)
	}
}

// TestParseJSONRPCResponseIDFromPrefixIgnoresRequests avoids completing a
// caller when the oversized record is a server-initiated request.
func TestParseJSONRPCResponseIDFromPrefixIgnoresRequests(t *testing.T) {
	_, ok := parseJSONRPCResponseIDFromPrefix([]byte(`{"id":42,"method":"tool/call","params":{"huge":"`))
	if ok {
		t.Fatalf("expected oversized request prefix to be ignored")
	}
}

// TestDispatchOversizedMessageUnblocksPendingCall ensures a dropped oversized
// response does not leave the matching caller waiting for a timeout.
func TestDispatchOversizedMessageUnblocksPendingCall(t *testing.T) {
	client := NewClient(false)
	ch := make(chan rpcResponse, 1)
	client.pending[7] = ch

	client.dispatchOversizedMessage([]byte(`{"id":7,"result":{"huge":"`))

	select {
	case resp := <-ch:
		if !errors.Is(resp.err, ErrOversizedMessage) {
			t.Fatalf("expected ErrOversizedMessage, got %v", resp.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for oversized response dispatch")
	}
}
