package runtime

import (
	"sync"
)

// Config controls a Runtime instance.
type Config struct {
	// SessionID identifies which session the runtime belongs to.
	SessionID string

	// QueueSize bounds the event queue. If zero, a default is used.
	QueueSize int
}

// Runtime serializes session events and produces effect commands.
type Runtime struct {
	mu sync.Mutex

	state State

	events   chan Event
	commands chan Command
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// New creates a new Runtime instance.
func New(cfg Config) *Runtime {
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 256
	}

	r := &Runtime{
		state: State{
			SessionID: cfg.SessionID,
		},
		events:   make(chan Event, queueSize),
		commands: make(chan Command, queueSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	return r
}

// Start begins the runtime loop in a new goroutine.
func (r *Runtime) Start() {
	go r.loop()
}

// Stop requests stopping the runtime loop and waits for it to exit.
func (r *Runtime) Stop() {
	select {
	case <-r.stopCh:
		<-r.doneCh
		return
	default:
		close(r.stopCh)
	}
	<-r.doneCh
}

// Commands returns a channel of commands to be executed by the caller.
func (r *Runtime) Commands() <-chan Command {
	return r.commands
}

// Post enqueues an event for the runtime loop.
// It returns false if the runtime is stopped or the queue is full.
func (r *Runtime) Post(evt Event) bool {
	if evt == nil {
		return false
	}
	select {
	case <-r.stopCh:
		return false
	default:
	}

	select {
	case r.events <- evt:
		return true
	default:
		return false
	}
}

// Snapshot returns a copy of the current runtime state.
func (r *Runtime) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *Runtime) loop() {
	defer close(r.doneCh)
	defer close(r.commands)

	for {
		select {
		case <-r.stopCh:
			return
		case evt := <-r.events:
			if evt == nil {
				continue
			}
			cmds := r.apply(evt)
			for _, cmd := range cmds {
				if cmd == nil {
					continue
				}
				select {
				case r.commands <- cmd:
				case <-r.stopCh:
					return
				}
			}
		}
	}
}

func (r *Runtime) apply(evt Event) []Command {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch e := evt.(type) {
	case SetWorkingEvent:
		if r.state.Working == e.Working {
			return nil
		}
		r.state.Working = e.Working
		if r.state.SessionID == "" {
			return nil
		}
		return []Command{
			EmitActivityCommand{
				SessionID:  r.state.SessionID,
				Working:    e.Working,
				Active:     true,
				ActiveAtMs: e.AtMs,
			},
		}
	default:
		return nil
	}
}
