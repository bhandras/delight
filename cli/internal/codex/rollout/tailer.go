package rollout

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	// tailPollInterval bounds how often we poll for appended rollout bytes when
	// reaching EOF.
	tailPollInterval = 150 * time.Millisecond
	// tailReadBufferSize is the bufio.Reader size used while tailing.
	tailReadBufferSize = 64 * 1024
	// tailChannelBuffer bounds the number of parsed events we queue for consumers.
	tailChannelBuffer = 128
)

// Tailer incrementally reads a Codex rollout JSONL file and emits parsed events.
type Tailer struct {
	path string
	opts TailerOptions

	events chan Event
	done   chan struct{}
}

// TailerOptions configures a Tailer instance.
type TailerOptions struct {
	// StartAtEnd seeks to EOF before reading. This is useful when the rollout file
	// already contains historical transcript and the consumer only wants new
	// appended events.
	StartAtEnd bool
}

// NewTailer returns a Tailer that reads the provided rollout JSONL path.
func NewTailer(path string, opts TailerOptions) *Tailer {
	return &Tailer{
		path:   path,
		opts:   opts,
		events: make(chan Event, tailChannelBuffer),
		done:   make(chan struct{}),
	}
}

// Events returns a channel that receives parsed rollout events.
func (t *Tailer) Events() <-chan Event {
	return t.events
}

// Start begins tailing in a background goroutine.
//
// Start returns an error only if the initial open fails. Subsequent read errors
// are surfaced as a terminal close of Events().
func (t *Tailer) Start(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("tailer is nil")
	}
	file, err := os.Open(t.path)
	if err != nil {
		return err
	}
	if t.opts.StartAtEnd {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			_ = file.Close()
			return err
		}
	}

	go func() {
		defer close(t.done)
		defer close(t.events)
		defer file.Close()

		reader := bufio.NewReaderSize(file, tailReadBufferSize)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadBytes('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Wait for more bytes.
					select {
					case <-ctx.Done():
						return
					case <-time.After(tailPollInterval):
						continue
					}
				}
				// Any other error terminates tailing.
				return
			}

			ev, ok, err := ParseLine(line)
			if err != nil || !ok {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case t.events <- ev:
			default:
				// If the consumer is slower than the writer, drop events to keep the
				// session responsive. Tailing is best-effort; the authoritative log
				// remains in the rollout JSONL file.
			}
		}
	}()

	return nil
}

// Wait blocks until the tailer stops.
func (t *Tailer) Wait() {
	if t == nil {
		return
	}
	<-t.done
}
