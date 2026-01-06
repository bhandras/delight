package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/crypto"
	sessionactor "github.com/bhandras/delight/cli/internal/session/actor"
	"github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestManager_HydrateSessionDataEncryptionKeyFromUpdate(t *testing.T) {
	t.Parallel()

	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i + 1)
	}
	wantKey := make([]byte, 32)
	for i := range wantKey {
		wantKey[i] = byte(100 + i)
	}
	encoded, err := crypto.EncryptDataEncryptionKey(wantKey, master)
	require.NoError(t, err)

	m := &Manager{
		sessionID:    "s1",
		masterSecret: master,
	}

	body := wire.UpdateBodyNewSession{
		T:                 "new-session",
		ID:                "s1",
		DataEncryptionKey: &encoded,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	var bodyMap map[string]any
	require.NoError(t, json.Unmarshal(raw, &bodyMap))

	m.hydrateSessionDataEncryptionKeyFromUpdate(map[string]any{"body": bodyMap})
	require.Equal(t, wantKey, m.dataKey)
}

func TestManager_HandleUpdate_DecryptsAndEnqueuesInboundUserMessage(t *testing.T) {
	t.Parallel()

	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(200 + i)
	}

	inCh := make(chan framework.Input, 8)
	hooks := framework.Hooks[sessionactor.State]{
		OnInput: func(input framework.Input) {
			select {
			case inCh <- input:
			default:
			}
		},
	}
	a := framework.New(sessionactor.State{}, func(state sessionactor.State, input framework.Input) (sessionactor.State, []framework.Effect) {
		_ = input
		return state, nil
	}, noopRuntime{}, framework.WithHooks(hooks))
	a.Start()
	defer a.Stop()

	m := &Manager{
		sessionID:    "s1",
		masterSecret: make([]byte, 32),
		dataKey:      dataKey,
		sessionActor: a,
	}

	plaintext := wire.UserTextRecord{
		Role: "user",
	}
	plaintext.Content.Type = "text"
	plaintext.Content.Text = "hi"
	plaintext.Meta = map[string]any{"k": "v"}
	raw, err := json.Marshal(plaintext)
	require.NoError(t, err)

	encrypted, err := crypto.EncryptWithDataKey(json.RawMessage(raw), dataKey)
	require.NoError(t, err)
	cipherB64 := base64.StdEncoding.EncodeToString(encrypted)

	localID := "local-1"
	update := map[string]any{
		"body": map[string]any{
			"t": "new-message",
			"message": map[string]any{
				"localId": localID,
				"content": map[string]any{
					"c": cipherB64,
				},
			},
		},
	}

	m.handleUpdate(update)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case in := <-inCh:
			if !strings.Contains(fmt.Sprintf("%T", in), "cmdInboundUserMessage") {
				continue
			}
			val := reflect.ValueOf(in)
			require.Equal(t, reflect.Struct, val.Kind())

			text := val.FieldByName("Text").String()
			require.Equal(t, "hi", text)

			gotLocalID := val.FieldByName("LocalID").String()
			require.Equal(t, localID, gotLocalID)

			meta, ok := val.FieldByName("Meta").Interface().(map[string]any)
			require.True(t, ok)
			require.Equal(t, any("v"), meta["k"])
			return
		case <-deadline:
			t.Fatalf("timeout waiting for inbound user message command")
		}
	}
}
