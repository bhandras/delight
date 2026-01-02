package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/acp"
	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/wire"
)

const (
	// acpToolAwait is the toolName used for ACP awaiting permission prompts.
	acpToolAwait = "acp.await"
	// acpPermissionTimeout bounds how long we wait for a permission decision.
	acpPermissionTimeout = 5 * time.Minute
)

// startACPRemote starts ACP "remote mode".
//
// ACP does not have a long-running local process. The runner is treated as
// "ready" once configuration is present.
func (r *Runtime) startACPRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	_ = ctx
	if err := r.ensureACPClient(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	r.mu.Lock()
	r.acpRemoteGen = eff.Gen
	r.acpRemoteActive = true
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeRemote})
}

// stopACPRemote stops ACP remote mode for the matching generation.
func (r *Runtime) stopACPRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	gen := r.acpRemoteGen
	active := r.acpRemoteActive
	r.mu.Unlock()
	if !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	r.mu.Lock()
	r.acpRemoteActive = false
	r.mu.Unlock()
}

// startACPLocal starts ACP "local mode".
//
// Local mode is treated as "ready" but does not accept remote sends; the FSM
// will switch to remote when the phone sends a message.
func (r *Runtime) startACPLocal(ctx context.Context, eff effStartLocalRunner, emit func(framework.Input)) {
	_ = ctx
	if err := r.ensureACPClient(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}

	r.mu.Lock()
	r.acpLocalGen = eff.Gen
	r.acpLocalActive = true
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeLocal})
}

// stopACPLocal stops ACP local mode for the matching generation.
func (r *Runtime) stopACPLocal(eff effStopLocalRunner) {
	r.mu.Lock()
	gen := r.acpLocalGen
	active := r.acpLocalActive
	r.mu.Unlock()
	if !active {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	r.mu.Lock()
	r.acpLocalActive = false
	r.mu.Unlock()
}

// acpRemoteSend executes an ACP run for the given user message and emits output records.
func (r *Runtime) acpRemoteSend(ctx context.Context, eff effRemoteSend, emit func(framework.Input)) {
	r.mu.Lock()
	gen := r.acpRemoteGen
	active := r.acpRemoteActive
	client := r.acpClient
	sessionID := r.acpSessionID
	encryptFn := r.encryptFn
	r.mu.Unlock()

	if !active || client == nil || sessionID == "" || encryptFn == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	if strings.TrimSpace(eff.Text) == "" {
		return
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		result, err := client.Run(runCtx, sessionID, eff.Text)
		if err != nil {
			return
		}

		for result != nil && result.Status == "awaiting" && result.AwaitRequest != nil {
			payload, err := json.Marshal(wire.ACPAwaitInput{Await: result.AwaitRequest})
			if err != nil {
				return
			}

			permCtx, permCancel := context.WithTimeout(context.Background(), acpPermissionTimeout)
			decision, err := r.AwaitPermission(permCtx, fmt.Sprintf("acp-%s", result.RunID), acpToolAwait, payload, time.Now().UnixMilli())
			permCancel()
			if err != nil {
				return
			}

			resume := wire.ACPAwaitResume{
				Allow:   decision.Allow,
				Message: decision.Message,
			}
			result, err = client.Resume(runCtx, result.RunID, resume)
			if err != nil {
				return
			}
		}

		if result == nil || strings.TrimSpace(result.OutputText) == "" {
			return
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
						Model: "acp",
						Content: []wire.ContentBlock{
							{Type: "text", Text: result.OutputText},
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
			LocalID:    recordID,
			Ciphertext: ciphertext,
			NowMs:      time.Now().UnixMilli(),
		})
	}()
}

// ensureACPClient ensures the ACP client is configured and constructed.
func (r *Runtime) ensureACPClient() error {
	r.mu.Lock()
	url := r.acpURL
	agent := r.acpAgent
	sessionID := r.acpSessionID
	client := r.acpClient
	debug := r.debug
	r.mu.Unlock()

	if strings.TrimSpace(url) == "" || strings.TrimSpace(agent) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("acp is not configured (missing url, agent, or session id)")
	}

	if client != nil {
		return nil
	}

	r.mu.Lock()
	// Re-check under lock.
	if r.acpClient == nil {
		r.acpClient = acp.NewClient(url, agent, debug)
	}
	r.mu.Unlock()

	return nil
}
