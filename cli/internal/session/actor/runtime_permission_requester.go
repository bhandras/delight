package actor

import (
	"context"
	"encoding/json"
	"fmt"

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
	r.mu.Unlock()
	if emit == nil {
		return agentengine.PermissionDecision{}, fmt.Errorf("runtime emit function not configured")
	}

	reply := make(chan PermissionDecision, 1)
	emit(cmdPermissionAwait{
		RequestID: requestID,
		ToolName:  toolName,
		Input:     append([]byte(nil), input...),
		NowMs:     nowMs,
		Reply:     reply,
	})

	select {
	case <-ctx.Done():
		return agentengine.PermissionDecision{}, ctx.Err()
	case decision := <-reply:
		return agentengine.PermissionDecision{
			Allow:   decision.Allow,
			Message: decision.Message,
		}, nil
	}
}
