// Package acpengine adapts ACP (HTTP API) to the agentengine.AgentEngine
// interface.
//
// ACP is treated as a remote-only engine in Delight: the phone owns the
// interactive loop and user messages are forwarded to ACP for synchronous runs.
package acpengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bhandras/delight/cli/internal/acp"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// acpToolAwait is the toolName used when ACP blocks on an await_request.
	acpToolAwait = "acp.await"
	// acpPermissionTimeout bounds how long we wait for a permission decision.
	acpPermissionTimeout = 5 * time.Minute
	// acpModelName is the model identifier used in emitted agent records.
	acpModelName = "acp"
)

// ConfigProvider supplies ACP connection parameters.
//
// ACP requires an HTTP base URL, an agent name, and a stable session identifier.
type ConfigProvider interface {
	ACPConfig() (baseURL string, agentName string, sessionID string)
}

// Engine adapts ACP to agentengine.AgentEngine.
type Engine struct {
	mu sync.Mutex

	requester agentengine.PermissionRequester
	provider  ConfigProvider
	debug     bool

	events chan agentengine.Event

	client    *acp.Client
	sessionID string
	active    bool

	turnCancel context.CancelFunc

	waitOnce sync.Once
	waitErr  error
	waitCh   chan struct{}
}

// New returns a new ACP engine.
func New(requester agentengine.PermissionRequester, provider ConfigProvider, debug bool) *Engine {
	return &Engine{
		requester: requester,
		provider:  provider,
		debug:     debug,
		events:    make(chan agentengine.Event, 128),
		waitCh:    make(chan struct{}),
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
		return fmt.Errorf("acp engine is nil")
	}

	// ACP is remote-only. We tolerate local starts as "ready" so mode switches
	// don't explode if upstream code still attempts to start local mode.
	if spec.Mode == agentengine.ModeLocal {
		e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeLocal})
		return nil
	}
	if spec.Mode != agentengine.ModeRemote {
		return fmt.Errorf("unsupported mode: %q", spec.Mode)
	}

	baseURL, agentName, sessionID := e.provider.ACPConfig()
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(agentName) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("acp is not configured (missing url, agent, or session id)")
	}

	e.mu.Lock()
	e.client = acp.NewClient(baseURL, agentName, e.debug)
	e.sessionID = sessionID
	e.active = true
	e.mu.Unlock()

	e.tryEmit(agentengine.EvReady{Mode: agentengine.ModeRemote})
	return nil
}

// Stop implements agentengine.AgentEngine.
func (e *Engine) Stop(ctx context.Context, mode agentengine.Mode) error {
	_ = ctx
	if e == nil {
		return nil
	}

	e.mu.Lock()
	if mode == agentengine.ModeRemote {
		e.active = false
	}
	cancel := e.turnCancel
	e.turnCancel = nil
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// Close implements agentengine.AgentEngine.
func (e *Engine) Close(ctx context.Context) error {
	_ = ctx
	if e == nil {
		return nil
	}
	_ = e.Stop(context.Background(), agentengine.ModeRemote)
	return nil
}

// SendUserMessage implements agentengine.AgentEngine.
func (e *Engine) SendUserMessage(ctx context.Context, msg agentengine.UserMessage) error {
	if e == nil {
		return fmt.Errorf("acp engine is nil")
	}

	e.mu.Lock()
	active := e.active
	client := e.client
	sessionID := e.sessionID
	prevCancel := e.turnCancel
	e.mu.Unlock()

	if !active || client == nil || sessionID == "" {
		return fmt.Errorf("acp remote mode not active")
	}

	if prevCancel != nil {
		prevCancel()
	}

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.turnCancel = cancel
	e.mu.Unlock()

	nowMs := time.Now().UnixMilli()
	e.tryEmit(agentengine.EvThinking{Mode: agentengine.ModeRemote, Thinking: true, AtMs: nowMs})
	defer e.tryEmit(agentengine.EvThinking{Mode: agentengine.ModeRemote, Thinking: false, AtMs: time.Now().UnixMilli()})

	result, err := client.Run(runCtx, sessionID, msg.Text)
	if err != nil {
		return err
	}

	for result != nil && result.Status == "awaiting" && result.AwaitRequest != nil {
		payload, err := json.Marshal(wire.ACPAwaitInput{Await: result.AwaitRequest})
		if err != nil {
			return err
		}

		permCtx, permCancel := context.WithTimeout(context.Background(), acpPermissionTimeout)
		decision, err := e.requester.AwaitPermission(
			permCtx,
			fmt.Sprintf("acp-%s", result.RunID),
			acpToolAwait,
			payload,
			time.Now().UnixMilli(),
		)
		permCancel()
		if err != nil {
			return err
		}

		resume := wire.ACPAwaitResume{
			Allow:   decision.Allow,
			Message: decision.Message,
		}
		result, err = client.Resume(runCtx, result.RunID, resume)
		if err != nil {
			return err
		}
	}

	if result == nil || strings.TrimSpace(result.OutputText) == "" {
		return nil
	}

	recordID := types.NewCUID()
	plaintext, err := json.Marshal(wire.AgentOutputRecord{
		Role: "agent",
		Content: wire.AgentOutputContent{
			Type: "output",
			Data: wire.AgentOutputData{
				Type:             "assistant",
				IsSidechain:      false,
				IsCompactSummary: false,
				IsMeta:           false,
				UUID:             recordID,
				ParentUUID:       nil,
				Message: wire.AgentMessage{
					Role:  "assistant",
					Model: acpModelName,
					Content: []wire.ContentBlock{
						{Type: "text", Text: result.OutputText},
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
		LocalID: recordID,
		Payload: plaintext,
		AtMs:    time.Now().UnixMilli(),
	})
	return nil
}

// Abort implements agentengine.AgentEngine.
func (e *Engine) Abort(ctx context.Context) error {
	_ = ctx
	if e == nil {
		return nil
	}
	e.mu.Lock()
	cancel := e.turnCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Capabilities implements agentengine.AgentEngine.
func (e *Engine) Capabilities() agentengine.AgentCapabilities {
	// ACP capabilities are currently unknown; treat as fixed defaults until we
	// integrate upstream capability APIs.
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
	// ACP config is not yet modeled in Delight.
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
