package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
)

// AwaitPermission implements agentengine.PermissionRequester for synchronous engines.
//
// This method schedules a cmdPermissionAwait command on the actor mailbox and
// blocks the caller goroutine until the user responds (or ctx is cancelled).
func (r *Runtime) AwaitPermission(ctx context.Context, requestID string, toolName string, input json.RawMessage, nowMs int64) (agentengine.PermissionDecision, error) {
	if r == nil {
		return agentengine.PermissionDecision{}, fmt.Errorf("runtime is nil")
	}
	if requestID == "" || toolName == "" {
		return agentengine.PermissionDecision{}, fmt.Errorf("missing requestID or toolName")
	}

	r.mu.Lock()
	emit := r.emitFn
	debug := r.debug
	r.mu.Unlock()
	if emit == nil {
		if debug {
			log.Printf("session: AwaitPermission cannot emit (emitFn nil) requestID=%s tool=%s", requestID, toolName)
		}
		return agentengine.PermissionDecision{}, fmt.Errorf("runtime emit function not configured")
	}

	reply := make(chan PermissionDecision, 1)
	ack := make(chan struct{}, 1)

	// Ensure the permission request is registered by the reducer before we wait
	// for a user decision. If the actor mailbox is overloaded (or a command is
	// dropped), we retry registration with backoff rather than failing fast. A
	// fast failure would cause upstream engines (Codex/ACP) to treat the request
	// as denied even if the phone UI later approves it.
	ackWait := 2 * time.Second
	const maxAckWait = 10 * time.Second
	for {
		if debug {
			log.Printf("session: AwaitPermission registering requestID=%s tool=%s", requestID, toolName)
		}
		emit(cmdPermissionAwait{
			RequestID: requestID,
			ToolName:  toolName,
			Input:     append([]byte(nil), input...),
			NowMs:     nowMs,
			Reply:     reply,
			Ack:       ack,
		})

		select {
		case <-ack:
			if debug {
				log.Printf("session: AwaitPermission registered requestID=%s tool=%s", requestID, toolName)
			}
			goto awaitDecision
		case <-ctx.Done():
			return agentengine.PermissionDecision{}, ctx.Err()
		case <-time.After(ackWait):
			if debug {
				log.Printf("session: AwaitPermission retry requestID=%s tool=%s after=%s", requestID, toolName, ackWait)
			}
			// Retry. cmdPermissionAwait is idempotent; the reducer will avoid
			// duplicate ephemerals for the same request id.
			ackWait *= 2
			if ackWait > maxAckWait {
				ackWait = maxAckWait
			}
		}
	}

awaitDecision:
	select {
	case <-ctx.Done():
		return agentengine.PermissionDecision{}, ctx.Err()
	case decision := <-reply:
		if debug {
			log.Printf("session: AwaitPermission resolved requestID=%s tool=%s allow=%t", requestID, toolName, decision.Allow)
		}
		return agentengine.PermissionDecision{
			Allow:   decision.Allow,
			Message: decision.Message,
		}, nil
	}
}
