package handlers

import (
	"context"
	"database/sql"
	"log"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
)

// UpdateState applies a session agent-state update and returns an ACK payload
// plus any resulting update events that should be broadcast.
func UpdateState(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.UpdateStatePayload) EventResult {
	if req.SID == "" {
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	session, err := deps.Sessions().GetSessionByID(ctx, req.SID)
	if err != nil || session.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	stateVal := sql.NullString{}
	if req.AgentState != nil {
		stateVal.Valid = true
		stateVal.String = *req.AgentState
	}

	rows, err := deps.Sessions().UpdateSessionAgentState(ctx, models.UpdateSessionAgentStateParams{
		AgentState:          stateVal,
		AgentStateVersion:   req.ExpectedVersion + 1,
		ID:                  req.SID,
		AgentStateVersion_2: req.ExpectedVersion,
	})
	if err != nil || rows == 0 {
		current, err := deps.Sessions().GetSessionByID(ctx, req.SID)
		if err == nil {
			resp := protocolwire.VersionedAck{
				Result:  "version-mismatch",
				Version: current.AgentStateVersion,
			}
			if current.AgentState.Valid {
				resp.AgentState = current.AgentState.String
			}
			return NewEventResult(resp, nil)
		}
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	ack := protocolwire.VersionedAck{
		Result:  "success",
		Version: req.ExpectedVersion + 1,
	}
	if req.AgentState != nil {
		ack.AgentState = *req.AgentState
	}

	userSeq, err := deps.Accounts().UpdateAccountSeq(ctx, auth.UserID())
	if err != nil {
		log.Printf("Failed to allocate user seq: %v", err)
		return NewEventResult(ack, nil)
	}

	var agentStateBody *protocolwire.VersionedString
	if req.AgentState != nil {
		agentStateBody = &protocolwire.VersionedString{
			Value:   *req.AgentState,
			Version: req.ExpectedVersion + 1,
		}
	}

	event := protocolwire.UpdateEvent{
		ID:        deps.NewID(),
		Seq:       userSeq,
		CreatedAt: deps.Now().UnixMilli(),
		Body: protocolwire.UpdateBodyUpdateSession{
			T:          "update-session",
			ID:         req.SID,
			AgentState: agentStateBody,
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newSessionUpdate(auth.UserID(), req.SID, event)})
}
