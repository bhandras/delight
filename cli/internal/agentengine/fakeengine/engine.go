// Package fakeengine provides an in-memory implementation of agentengine.AgentEngine.
//
// This exists to support deterministic tests and UI development without
// spawning any real TTY/TUI processes (which can leave terminals in broken
// states when interrupted).
package fakeengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// fakeAgentModel is the model name emitted for fake agent records.
	fakeAgentModel = "fake-agent"
	// fakeAgentReplyPrefix is the prefix used in the fake agent reply body.
	fakeAgentReplyPrefix = "fake-agent: "
)

// Engine implements agentengine.AgentEngine using pure in-memory behavior.
type Engine struct {
	mu sync.Mutex

	events chan agentengine.Event

	activeRemote bool
	activeLocal  bool

	waitOnce sync.Once
	waitErr  error
	waitCh   chan struct{}
}

// New returns a new fake engine instance.
func New() *Engine {
	return &Engine{
		events: make(chan agentengine.Event, 128),
		waitCh: make(chan struct{}),
	}
}

// Events implements agentengine.AgentEngine.
func (e *Engine) Events() <-chan agentengine.Event {
	return e.events
}

// Start implements agentengine.AgentEngine.
func (e *Engine) Start(ctx context.Context, spec agentengine.EngineStartSpec) error {
	_ = ctx
	if e == nil {
		return fmt.Errorf("fake engine is nil")
	}

	e.mu.Lock()
	switch spec.Mode {
	case agentengine.ModeRemote:
		e.activeRemote = true
	case agentengine.ModeLocal:
		e.activeLocal = true
	default:
		e.mu.Unlock()
		return fmt.Errorf("unsupported mode: %q", spec.Mode)
	}
	e.mu.Unlock()

	e.tryEmit(agentengine.EvReady{Mode: spec.Mode})
	return nil
}

// Stop implements agentengine.AgentEngine.
func (e *Engine) Stop(ctx context.Context, mode agentengine.Mode) error {
	_ = ctx
	if e == nil {
		return nil
	}

	e.mu.Lock()
	switch mode {
	case agentengine.ModeRemote:
		e.activeRemote = false
	case agentengine.ModeLocal:
		e.activeLocal = false
	}
	e.mu.Unlock()
	return nil
}

// Close implements agentengine.AgentEngine.
func (e *Engine) Close(ctx context.Context) error {
	_ = ctx
	if e == nil {
		return nil
	}
	_ = e.Stop(context.Background(), agentengine.ModeRemote)
	_ = e.Stop(context.Background(), agentengine.ModeLocal)
	return nil
}

// SendUserMessage implements agentengine.AgentEngine.
func (e *Engine) SendUserMessage(ctx context.Context, msg agentengine.UserMessage) error {
	_ = ctx
	if e == nil {
		return fmt.Errorf("fake engine is nil")
	}

	e.mu.Lock()
	active := e.activeRemote
	e.mu.Unlock()
	if !active {
		return fmt.Errorf("fake remote mode not active")
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}

	nowMs := time.Now().UnixMilli()
	e.tryEmit(agentengine.EvThinking{Mode: agentengine.ModeRemote, Thinking: true, AtMs: nowMs})
	defer e.tryEmit(agentengine.EvThinking{Mode: agentengine.ModeRemote, Thinking: false, AtMs: time.Now().UnixMilli()})

	reply := fmt.Sprintf("%s%s", fakeAgentReplyPrefix, text)
	uuid := types.NewCUID()

	plaintext, err := json.Marshal(wire.AgentOutputRecord{
		Role: "agent",
		Content: wire.AgentOutputContent{
			Type: "output",
			Data: wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             uuid,
				ParentUUID:       nil,
				Message: wire.AgentMessage{
					Role:  "assistant",
					Model: fakeAgentModel,
					Content: []wire.ContentBlock{
						{Type: "text", Text: reply},
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}

	e.tryEmit(agentengine.EvOutboundRecord{
		Mode:    agentengine.ModeRemote,
		LocalID: uuid,
		Payload: plaintext,
		AtMs:    time.Now().UnixMilli(),
	})
	return nil
}

// Abort implements agentengine.AgentEngine.
func (e *Engine) Abort(ctx context.Context) error {
	_ = ctx
	return nil
}

// Capabilities implements agentengine.AgentEngine.
func (e *Engine) Capabilities() agentengine.AgentCapabilities {
	return agentengine.AgentCapabilities{}
}

// CurrentConfig implements agentengine.AgentEngine.
func (e *Engine) CurrentConfig() agentengine.AgentConfig {
	return agentengine.AgentConfig{}
}

// ApplyConfig implements agentengine.AgentEngine.
func (e *Engine) ApplyConfig(ctx context.Context, cfg agentengine.AgentConfig) error {
	_ = ctx
	_ = cfg
	return nil
}

// Wait implements agentengine.AgentEngine.
func (e *Engine) Wait() error {
	if e == nil {
		return nil
	}

	e.waitOnce.Do(func() {
		defer close(e.waitCh)
		e.waitErr = nil
	})
	<-e.waitCh
	return e.waitErr
}

func (e *Engine) tryEmit(ev agentengine.Event) {
	if e == nil {
		return
	}
	select {
	case e.events <- ev:
	default:
	}
}
