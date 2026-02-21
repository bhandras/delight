package webclient

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/bhandras/delight/shared/webapi"
)

// eventHub manages stream subscribers and replay buffering.
type eventHub struct {
	mu         sync.Mutex
	nextID     int64
	nextSubID  int64
	subs       map[int64]chan webapi.StreamEvent
	buffer     []webapi.StreamEvent
	bufferSize int
}

// newEventHub creates a hub with bounded replay capacity.
func newEventHub(bufferSize int) *eventHub {
	if bufferSize <= 0 {
		bufferSize = maxEventBuffer
	}
	return &eventHub{
		nextID:     1,
		nextSubID:  1,
		subs:       make(map[int64]chan webapi.StreamEvent),
		buffer:     make([]webapi.StreamEvent, 0, bufferSize),
		bufferSize: bufferSize,
	}
}

// latestEventID returns the most recently published event id.
func (h *eventHub) latestEventID() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextID - 1
}

// publishJSON marshals payload and publishes it as a new stream event.
func (h *eventHub) publishJSON(kind string, payload interface{}) webapi.StreamEvent {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(`{"error":"failed to encode stream payload"}`)
	}
	return h.publishRaw(kind, raw)
}

// publishRaw emits an event with pre-encoded JSON payload.
func (h *eventHub) publishRaw(kind string, payload json.RawMessage) webapi.StreamEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	event := webapi.StreamEvent{
		EventID: h.nextID,
		Kind:    kind,
		TS:      time.Now().UnixMilli(),
		Payload: payload,
	}
	h.nextID++
	h.buffer = append(h.buffer, event)
	if len(h.buffer) > h.bufferSize {
		start := len(h.buffer) - h.bufferSize
		h.buffer = append([]webapi.StreamEvent(nil), h.buffer[start:]...)
	}
	for _, ch := range h.subs {
		select {
		case ch <- event:
		default:
		}
	}
	return event
}

// subscribe registers a live stream subscriber and returns its channel.
func (h *eventHub) subscribe() (int64, <-chan webapi.StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextSubID
	h.nextSubID++
	ch := make(chan webapi.StreamEvent, 64)
	h.subs[id] = ch
	return id, ch
}

// unsubscribe removes and closes a previously registered subscriber.
func (h *eventHub) unsubscribe(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.subs[id]
	if !ok {
		return
	}
	delete(h.subs, id)
	close(ch)
}

// replaySince returns buffered events newer than the cursor.
//
// The second return value is true when the cursor is too old and the client
// must perform a full resync.
func (h *eventHub) replaySince(since int64) ([]webapi.StreamEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if since <= 0 {
		out := make([]webapi.StreamEvent, len(h.buffer))
		copy(out, h.buffer)
		return out, false
	}
	if len(h.buffer) == 0 {
		return nil, false
	}
	oldest := h.buffer[0].EventID
	if since < oldest {
		return nil, true
	}
	out := make([]webapi.StreamEvent, 0, len(h.buffer))
	for _, event := range h.buffer {
		if event.EventID > since {
			out = append(out, event)
		}
	}
	return out, false
}
