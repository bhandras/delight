package session

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

type fakeTerminalAckClient struct {
	connected bool

	lastEvent   string
	lastPayload any
	lastTimeout time.Duration

	resp map[string]any
	err  error
}

// IsConnected implements terminalAckClient.
func (f *fakeTerminalAckClient) IsConnected() bool { return f.connected }

// EmitWithAck implements terminalAckClient.
func (f *fakeTerminalAckClient) EmitWithAck(event string, payload any, timeout time.Duration) (map[string]any, error) {
	f.lastEvent = event
	f.lastPayload = payload
	f.lastTimeout = timeout
	return f.resp, f.err
}

func TestEmitTerminalUpdateState_SuccessDecryptsPayload(t *testing.T) {
	t.Parallel()

	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i + 1)
	}
	state := &types.DaemonState{Status: "running", PID: 123, StartedAt: 456}

	client := &fakeTerminalAckClient{
		resp: map[string]any{"result": "success", "version": float64(7)},
	}
	gotVer, err := emitTerminalUpdateState(client, "term-1", state, master, 3)
	require.NoError(t, err)
	require.Equal(t, int64(7), gotVer)
	require.Equal(t, "terminal-update-state", client.lastEvent)

	payload, ok := client.lastPayload.(wire.TerminalUpdateStatePayload)
	require.True(t, ok)
	require.Equal(t, "term-1", payload.TerminalID)
	require.Equal(t, int64(3), payload.ExpectedVersion)

	raw, err := base64.StdEncoding.DecodeString(payload.DaemonState)
	require.NoError(t, err)
	var decrypted types.DaemonState
	require.NoError(t, crypto.DecryptWithDataKey(raw, master, &decrypted))
	require.Equal(t, *state, decrypted)
}

func TestEmitTerminalUpdateState_MissingAckFails(t *testing.T) {
	t.Parallel()

	master := make([]byte, 32)
	client := &fakeTerminalAckClient{resp: nil}

	_, err := emitTerminalUpdateState(client, "term-1", &types.DaemonState{Status: "running"}, master, 0)
	require.Error(t, err)
}

func TestEmitTerminalUpdateState_ServerFailureFails(t *testing.T) {
	t.Parallel()

	master := make([]byte, 32)
	client := &fakeTerminalAckClient{resp: map[string]any{"result": "nope"}}

	_, err := emitTerminalUpdateState(client, "term-1", &types.DaemonState{Status: "running"}, master, 0)
	require.Error(t, err)
}

func TestEmitTerminalUpdateMetadata_VersionMismatchReturnsServerVersion(t *testing.T) {
	t.Parallel()

	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(200 + i)
	}
	meta := &types.TerminalMetadata{
		Host:              "h",
		Platform:          "darwin",
		DelightCliVersion: "v",
		HomeDir:           "/h",
		DelightHomeDir:    "/d",
	}

	client := &fakeTerminalAckClient{
		resp: map[string]any{"result": "version-mismatch", "version": json.Number("9")},
	}
	gotVer, err := emitTerminalUpdateMetadata(client, "term-1", meta, master, 1)
	require.NoError(t, err)
	require.Equal(t, int64(9), gotVer)
	require.Equal(t, "terminal-update-metadata", client.lastEvent)
}

func TestGetInt64_ConvertsNumericTypes(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(1), getInt64(int64(1)))
	require.Equal(t, int64(2), getInt64(int(2)))
	require.Equal(t, int64(3), getInt64(float64(3)))
	require.Equal(t, int64(4), getInt64(float32(4)))
	require.Equal(t, int64(5), getInt64(json.Number("5")))
	require.Equal(t, int64(0), getInt64("nope"))
}

func TestManager_RegisterTerminalRPCHandlers_RegistersMethods(t *testing.T) {
	t.Parallel()

	m := &Manager{
		terminalID:  "t1",
		terminalRPC: websocket.NewRPCManager(nil, false),
	}
	m.registerTerminalRPCHandlers()

	keys := rpcManagerHandlerKeys(t, m.terminalRPC)
	require.Contains(t, keys, "t1:ping")
	require.Contains(t, keys, "t1:stop-daemon")
	require.Contains(t, keys, "t1:restart-daemon")
}

// rpcManagerHandlerKeys extracts registered handler method names via reflection.
func rpcManagerHandlerKeys(t *testing.T, mgr *websocket.RPCManager) []string {
	t.Helper()

	val := reflect.ValueOf(mgr).Elem().FieldByName("handlers")
	require.True(t, val.IsValid())

	iter := val.MapRange()
	out := make([]string, 0, val.Len())
	for iter.Next() {
		out = append(out, iter.Key().String())
	}
	sort.Strings(out)
	return out
}
