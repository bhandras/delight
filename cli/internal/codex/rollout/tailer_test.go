package rollout

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// tailerTestTimeout bounds how long we wait for a tailed event.
	tailerTestTimeout = 2 * time.Second
)

// TestTailerEmitsAppendedLines ensures Tailer observes new JSONL lines appended after Start.
func TestTailerEmitsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	require.NoError(t, os.WriteFile(path, nil, 0600))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewTailer(path, TailerOptions{})
	require.NoError(t, tailer.Start(ctx))

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	defer f.Close()

	_, err = f.WriteString(`{"timestamp":"2026-01-01T09:59:16.585Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Sync())

	select {
	case ev := <-tailer.Events():
		msg, ok := ev.(EvUserMessage)
		require.True(t, ok)
		require.Equal(t, "hi", msg.Text)
	case <-time.After(tailerTestTimeout):
		require.FailNow(t, "timed out waiting for rollout event")
	}
}
