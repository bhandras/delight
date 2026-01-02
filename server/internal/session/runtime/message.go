package runtime

import (
	"context"
	"time"

	"github.com/bhandras/delight/protocol/logger"
	protocolwire "github.com/bhandras/delight/protocol/wire"
	pkgtypes "github.com/bhandras/delight/server/pkg/types"
)

func (r *sessionRuntime) handleMessage(e messageEvent) {
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if e.userID == "" || e.sessionID == "" || e.cipher == "" {
		return
	}

	ok, err := r.store.ValidateSessionOwner(ctx, e.sessionID, e.userID)
	if err != nil {
		logger.Errorf("[runtime] message validate error sid=%s: %v", e.sessionID, err)
		return
	}
	if !ok {
		return
	}

	seq, err := r.store.NextSessionMessageSeq(ctx, e.sessionID)
	if err != nil {
		logger.Errorf("[runtime] message seq alloc error sid=%s: %v", e.sessionID, err)
		return
	}

	msg, err := r.store.CreateEncryptedMessage(ctx, e.sessionID, seq, e.localID, e.cipher)
	if err != nil {
		logger.Errorf("[runtime] message persist error sid=%s: %v", e.sessionID, err)
		return
	}

	_ = r.store.MarkSessionActive(ctx, e.sessionID)

	userSeq, err := r.store.NextUserSeq(ctx, e.userID)
	if err != nil {
		logger.Errorf("[runtime] user seq alloc error uid=%s: %v", e.userID, err)
		return
	}

	update := protocolwire.UpdateEvent{
		ID:        pkgtypes.NewCUID(),
		Seq:       userSeq,
		CreatedAt: time.Now().UnixMilli(),
		Body: protocolwire.UpdateBodyNewMessage{
			T:   "new-message",
			SID: e.sessionID,
			Message: protocolwire.UpdateNewMessage{
				ID:      msg.ID,
				Seq:     msg.Seq,
				LocalID: e.localID,
				Content: protocolwire.EncryptedEnvelope{
					T: "encrypted",
					C: e.cipher,
				},
				CreatedAt: msg.CreatedAt,
				UpdatedAt: msg.UpdatedAt,
			},
		},
	}

	r.emitter.EmitUpdateToSession(e.userID, e.sessionID, update, e.skipSocketID)
}
