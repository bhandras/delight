package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// fakeAgentModel is the model name emitted for fake agent records.
	fakeAgentModel = "fake-agent"
	// fakeAgentReplyPrefix is the prefix used in the fake agent reply body.
	fakeAgentReplyPrefix = "fake-agent: "
)

// startFakeRemote starts the fake agent in remote mode.
func (r *Runtime) startFakeRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	_ = ctx
	r.mu.Lock()
	r.fakeRemoteGen = eff.Gen
	r.fakeRemoteActive = true
	r.mu.Unlock()
	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeRemote})
}

// stopFakeRemote stops fake remote mode for the matching generation.
func (r *Runtime) stopFakeRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	gen := r.fakeRemoteGen
	active := r.fakeRemoteActive
	r.mu.Unlock()
	if !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	r.mu.Lock()
	r.fakeRemoteActive = false
	r.mu.Unlock()
}

// startFakeLocal starts the fake agent in local mode.
func (r *Runtime) startFakeLocal(ctx context.Context, eff effStartLocalRunner, emit func(framework.Input)) {
	_ = ctx
	r.mu.Lock()
	r.fakeLocalGen = eff.Gen
	r.fakeLocalActive = true
	r.mu.Unlock()
	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeLocal})
}

// stopFakeLocal stops fake local mode for the matching generation.
func (r *Runtime) stopFakeLocal(eff effStopLocalRunner) {
	r.mu.Lock()
	gen := r.fakeLocalGen
	active := r.fakeLocalActive
	r.mu.Unlock()
	if !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	r.mu.Lock()
	r.fakeLocalActive = false
	r.mu.Unlock()
}

// fakeRemoteSend emits a fake assistant response for the provided user message.
func (r *Runtime) fakeRemoteSend(ctx context.Context, eff effRemoteSend, emit func(framework.Input)) {
	r.mu.Lock()
	gen := r.fakeRemoteGen
	active := r.fakeRemoteActive
	encryptFn := r.encryptFn
	r.mu.Unlock()

	if !active || encryptFn == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}

	text := strings.TrimSpace(eff.Text)
	if text == "" {
		return
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		default:
		}

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
			return
		}

		ciphertext, err := encryptFn(plaintext)
		if err != nil {
			return
		}
		emit(evOutboundMessageReady{
			Gen:        gen,
			LocalID:    uuid,
			Ciphertext: ciphertext,
			NowMs:      time.Now().UnixMilli(),
		})
	}()
}
