package webclient

import (
	"sync"
	"time"

	"github.com/bhandras/delight/shared/webapi"
)

// logBuffer stores recent log lines in memory for debug endpoints.
type logBuffer struct {
	mu      sync.Mutex
	maxSize int
	items   []webapi.LogEntry
}

// newLogBuffer creates a bounded in-memory log buffer.
func newLogBuffer(maxSize int) *logBuffer {
	if maxSize <= 0 {
		maxSize = maxLogEntries
	}
	return &logBuffer{maxSize: maxSize, items: make([]webapi.LogEntry, 0, maxSize)}
}

// add appends a new timestamped log line.
func (b *logBuffer) add(level, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = append(b.items, webapi.LogEntry{
		TS:      time.Now().UnixMilli(),
		Level:   level,
		Message: message,
	})
	if len(b.items) > b.maxSize {
		start := len(b.items) - b.maxSize
		b.items = append([]webapi.LogEntry(nil), b.items[start:]...)
	}
}

// list returns a copy of all buffered log lines.
func (b *logBuffer) list() []webapi.LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]webapi.LogEntry, len(b.items))
	copy(out, b.items)
	return out
}

// clear drops all buffered log lines.
func (b *logBuffer) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = nil
}
