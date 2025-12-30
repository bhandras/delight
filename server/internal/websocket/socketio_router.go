package websocket

import (
	"context"

	"github.com/bhandras/delight/server/internal/websocket/handlers"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
)

func (s *SocketIOServer) emitHandlerUpdates(callerSocketID string, result handlers.EventResult) {
	for _, upd := range result.Updates() {
		skipSocketID := ""
		if upd.SkipSelf() {
			skipSocketID = callerSocketID
		}
		switch {
		case upd.IsUser():
			s.emitUpdateToUser(upd.UserID(), upd.Event(), skipSocketID)
		case upd.IsSession():
			s.emitUpdateToSession(upd.UserID(), upd.SessionID(), upd.Event(), skipSocketID)
		}
	}
}

func onTypedAck[Req any](
	s *SocketIOServer,
	client *socket.Socket,
	event string,
	deps handlers.Deps,
	handler func(context.Context, handlers.Deps, handlers.AuthContext, Req) handlers.EventResult,
) {
	client.On(event, func(data ...any) {
		sd := s.getSocketData(client.Id())
		raw, ack := getFirstAnyWithAck(data)

		var req Req
		_ = decodeAny(raw, &req)

		auth := handlers.NewAuthContext(sd.UserID, sd.ClientType, string(client.Id()))
		result := handler(context.Background(), deps, auth, req)

		if ack != nil {
			ack(result.Ack())
		}
		s.emitHandlerUpdates(string(client.Id()), result)
	})
}
