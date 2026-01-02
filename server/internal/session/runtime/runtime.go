package runtime

import (
	"context"
	"sync"

	"github.com/bhandras/delight/shared/logger"
)

// Manager owns per-session runtimes and provides serialized entrypoints.
type Manager struct {
	store   Store
	emitter UpdateEmitter

	mu       sync.Mutex
	runtimes map[string]*sessionRuntime
}

// NewManager creates a new per-session runtime manager.
func NewManager(store Store, emitter UpdateEmitter) *Manager {
	return &Manager{
		store:    store,
		emitter:  emitter,
		runtimes: make(map[string]*sessionRuntime),
	}
}

// EnqueueMessage schedules a message ingest for a session.
//
// The runtime serializes all ingests for a given session id to ensure stable
// ordering under concurrent Socket.IO callbacks.
func (m *Manager) EnqueueMessage(ctx context.Context, userID, sessionID, cipher string, localID *string, skipSocketID string) {
	if sessionID == "" {
		return
	}
	rt := m.getOrCreate(sessionID)
	rt.enqueue(messageEvent{
		ctx:          ctx,
		userID:       userID,
		sessionID:    sessionID,
		cipher:       cipher,
		localID:      localID,
		skipSocketID: skipSocketID,
	})
}

func (m *Manager) getOrCreate(sessionID string) *sessionRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.runtimes[sessionID]; ok {
		return rt
	}
	rt := newSessionRuntime(m.store, m.emitter, sessionID)
	m.runtimes[sessionID] = rt
	return rt
}

type sessionRuntime struct {
	store   Store
	emitter UpdateEmitter

	sessionID string
	events    chan any

	startOnce sync.Once
}

func newSessionRuntime(store Store, emitter UpdateEmitter, sessionID string) *sessionRuntime {
	return &sessionRuntime{
		store:     store,
		emitter:   emitter,
		sessionID: sessionID,
		events:    make(chan any, 256),
	}
}

func (r *sessionRuntime) enqueue(evt any) {
	r.startOnce.Do(func() { go r.loop() })
	select {
	case r.events <- evt:
	default:
		// Avoid blocking Socket.IO callbacks indefinitely; drop under overload.
		logger.Warnf("[runtime] session %s queue full; dropping event %T", r.sessionID, evt)
	}
}

func (r *sessionRuntime) loop() {
	for evt := range r.events {
		switch e := evt.(type) {
		case messageEvent:
			r.handleMessage(e)
		default:
			logger.Warnf("[runtime] session %s: unknown event %T", r.sessionID, evt)
		}
	}
}
