package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	protocolwire "github.com/bhandras/delight/shared/wire"
)

type fakeStore struct {
	mu sync.Mutex

	sessionOwner map[string]string
	sessionSeq   map[string]int64
	userSeq      map[string]int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessionOwner: make(map[string]string),
		sessionSeq:   make(map[string]int64),
		userSeq:      make(map[string]int64),
	}
}

func (s *fakeStore) ValidateSessionOwner(_ context.Context, sessionID, userID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionOwner[sessionID] == userID, nil
}

func (s *fakeStore) NextSessionMessageSeq(_ context.Context, sessionID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionSeq[sessionID]++
	return s.sessionSeq[sessionID], nil
}

func (s *fakeStore) CreateEncryptedMessage(_ context.Context, _ string, seq int64, _ *string, _ string) (StoredMessage, error) {
	now := time.Now().UnixMilli()
	return StoredMessage{
		ID:        "m",
		Seq:       seq,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *fakeStore) MarkSessionActive(_ context.Context, _ string) error { return nil }

func (s *fakeStore) NextUserSeq(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userSeq[userID]++
	return s.userSeq[userID], nil
}

type fakeEmitter struct {
	mu      sync.Mutex
	updates []protocolwire.UpdateEvent
}

func (e *fakeEmitter) EmitUpdateToSession(_ string, _ string, payload any, _ string) {
	ev, ok := payload.(protocolwire.UpdateEvent)
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updates = append(e.updates, ev)
}

func TestManager_EnqueueMessage_SerializesPerSession(t *testing.T) {
	store := newFakeStore()
	store.sessionOwner["s1"] = "u1"
	emitter := &fakeEmitter{}

	mgr := NewManager(store, emitter)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(_ int) {
			defer wg.Done()
			mgr.EnqueueMessage(context.Background(), "u1", "s1", "cipher", nil, "")
		}(i)
	}
	wg.Wait()

	// We don't have a completion channel; wait briefly for the runtime to drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		emitter.mu.Lock()
		got := len(emitter.updates)
		emitter.mu.Unlock()
		if got == n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	emitter.mu.Lock()
	defer emitter.mu.Unlock()

	if len(emitter.updates) != n {
		t.Fatalf("expected %d updates, got %d", n, len(emitter.updates))
	}

	seen := make(map[int64]bool, n)
	for _, upd := range emitter.updates {
		body, ok := upd.Body.(protocolwire.UpdateBodyNewMessage)
		if !ok {
			t.Fatalf("unexpected body type %T", upd.Body)
		}
		if body.T != "new-message" || body.SID != "s1" {
			t.Fatalf("unexpected body: %+v", body)
		}
		seq := body.Message.Seq
		if seen[seq] {
			t.Fatalf("duplicate message seq %d", seq)
		}
		seen[seq] = true
	}

	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("missing message seq %d", i)
		}
	}
}
