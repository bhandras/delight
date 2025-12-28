package sdk

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/stretchr/testify/require"
)

func TestClientSerializesConcurrentCalls(t *testing.T) {
	c := NewClient("http://example.invalid")

	master := make([]byte, 32)
	_, err := rand.Read(master)
	require.NoError(t, err)
	require.NoError(t, c.SetMasterKeyBase64(base64.StdEncoding.EncodeToString(master)))

	// Install a dummy socket so SendMessage gets past the nil socket check.
	_, err = c.dispatch.call(func() (interface{}, error) {
		c.userSocket = &websocket.Client{}
		return nil, nil
	})
	require.NoError(t, err)

	const goroutines = 50
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := make([]byte, 32)
				_, _ = rand.Read(key)
				_ = c.SetSessionDataKey("s1", base64.StdEncoding.EncodeToString(key))
				_ = c.SendMessage("s1", `{"role":"user","content":{"type":"text","text":"hi"}}`)
				c.SetDebug((g+i)%2 == 0)
			}
		}(g)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 10*time.Second, 10*time.Millisecond)
}

