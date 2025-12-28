package session

import (
	"sync"
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
