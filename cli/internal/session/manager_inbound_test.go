package session

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInboundQueueSerializesWork(t *testing.T) {
	m := &Manager{
		stopCh:       make(chan struct{}),
		inboundQueue: make(chan func(), 8),
	}
	t.Cleanup(func() { _ = m.Close() })

	m.startInboundLoop()

	startedFirst := make(chan struct{})
	releaseFirst := make(chan struct{})

	var orderMu sync.Mutex
	order := make([]int, 0, 2)

	_ = m.enqueueInbound(func() {
		close(startedFirst)
		<-releaseFirst
		orderMu.Lock()
		order = append(order, 1)
		orderMu.Unlock()
	})
	_ = m.enqueueInbound(func() {
		orderMu.Lock()
		order = append(order, 2)
		orderMu.Unlock()
	})

	<-startedFirst

	time.Sleep(50 * time.Millisecond)
	orderMu.Lock()
	require.Len(t, order, 0, "second inbound event should not run while first is blocked")
	orderMu.Unlock()

	close(releaseFirst)

	require.Eventually(t, func() bool {
		orderMu.Lock()
		defer orderMu.Unlock()
		return len(order) == 2
	}, 2*time.Second, 10*time.Millisecond)

	orderMu.Lock()
	require.Equal(t, []int{1, 2}, order)
	orderMu.Unlock()
}

func TestRunInboundRPCSerializesConcurrentCalls(t *testing.T) {
	m := &Manager{
		stopCh:       make(chan struct{}),
		inboundQueue: make(chan func(), 64),
	}
	t.Cleanup(func() { _ = m.Close() })

	m.startInboundLoop()

	var active int32
	var maxActive int32

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := m.runInboundRPC(func() (json.RawMessage, error) {
				cur := atomic.AddInt32(&active, 1)
				for {
					prev := atomic.LoadInt32(&maxActive)
					if cur <= prev || atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return json.RawMessage(`{"ok":true}`), nil
			})
			require.NoError(t, err)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for concurrent runInboundRPC calls to finish (possible deadlock)")
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&maxActive), "inbound queue must never run more than one inbound task at a time")
}
