package websocket

import (
	"context"

	"github.com/bhandras/delight/server/internal/websocket/handlers"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
)

func (s *SocketIOServer) registerClientHandlers(client *socket.Socket, deps handlers.Deps, socketID string) {
	// Message event - broadcast to session-scoped clients
	client.On("message", func(data ...any) {
		sd := s.getSocketData(socketID)
		logger.Tracef(
			"Message event from user %s (socket %s): %+v",
			sd.UserID,
			socketID,
			data,
		)

		if len(data) == 0 {
			return
		}

		var payload protocolwire.OutboundMessagePayload
		if err := decodeAny(data[0], &payload); err != nil {
			logger.Warnf("Message data decode error: %v (type=%T)", err, data[0])
			return
		}

		instr := handlers.MessageIngest(
			handlers.NewAuthContext(sd.UserID, sd.ClientType, socketID),
			sd.SessionID,
			payload,
		)
		if instr == nil {
			return
		}
		s.sessions.EnqueueMessage(
			context.Background(),
			instr.UserID(),
			instr.SessionID(),
			instr.Content(),
			instr.LocalID(),
			instr.SkipSocketID(),
		)
	})

	// Typed events (decode -> handler -> emit updates/ephemerals)
	onTypedEvent[protocolwire.SessionAlivePayload](s, client, "session-alive", deps, handlers.SessionAlive)
	onTypedEvent[protocolwire.TerminalAlivePayload](s, client, "terminal-alive", deps, handlers.TerminalAlive)
	onTypedEvent[protocolwire.UsageReportPayload](s, client, "usage-report", deps, handlers.UsageReport)

	// Typed ACK handlers (decode -> handler -> ack -> emit updates/ephemerals)
	onTypedAck[protocolwire.TerminalUpdateMetadataPayload](s, client, "terminal-update-metadata", deps, handlers.TerminalUpdateMetadata)
	onTypedAck[protocolwire.TerminalUpdateStatePayload](s, client, "terminal-update-state", deps, handlers.TerminalUpdateState)
	onTypedAck[protocolwire.ArtifactReadRequest](s, client, "artifact-read", deps, handlers.ArtifactRead)
	onTypedAck[protocolwire.ArtifactCreateRequest](s, client, "artifact-create", deps, handlers.ArtifactCreate)
	onTypedAck[protocolwire.ArtifactUpdateRequest](s, client, "artifact-update", deps, handlers.ArtifactUpdate)
	onTypedAck[protocolwire.ArtifactDeleteRequest](s, client, "artifact-delete", deps, handlers.ArtifactDelete)
	onTypedAck[protocolwire.UpdateMetadataPayload](s, client, "update-metadata", deps, handlers.UpdateMetadata)
	onTypedAck[protocolwire.UpdateStatePayload](s, client, "update-state", deps, handlers.UpdateState)

	// Ephemeral forward (client -> server -> user-scoped)
	client.On("ephemeral", func(data ...any) {
		sd := s.getSocketData(socketID)
		payload, _ := getFirstMap(data)
		result := handlers.EphemeralForward(
			context.Background(),
			deps,
			handlers.NewAuthContext(sd.UserID, sd.ClientType, socketID),
			payload,
		)
		s.emitHandlerUpdates(socketID, result)
	})

	// RPC register
	client.On("rpc-register", func(data ...any) {
		sd := s.getSocketData(socketID)
		raw, _ := getFirstAnyWithAck(data)
		var req protocolwire.RPCRegisterPayload
		if err := decodeAny(raw, &req); err != nil {
			req.Method = ""
		}
		method := req.Method
		if method == "" {
			client.Emit("rpc-error", protocolwire.RPCErrorPayload{Type: "register", Error: "Invalid method name"})
			return
		}
		if shouldDebugRPC() {
			logger.Debugf("RPC register: user=%s client=%s method=%s", sd.UserID, sd.ClientType, method)
		}
		s.rpc.Register(sd.UserID, method, socketID)
		client.Emit("rpc-registered", protocolwire.RPCRegisteredPayload{Method: method})
	})

	// RPC unregister
	client.On("rpc-unregister", func(data ...any) {
		sd := s.getSocketData(socketID)
		raw, _ := getFirstAnyWithAck(data)
		var req protocolwire.RPCRegisterPayload
		if err := decodeAny(raw, &req); err != nil {
			req.Method = ""
		}
		method := req.Method
		if method == "" {
			client.Emit("rpc-error", protocolwire.RPCErrorPayload{Type: "unregister", Error: "Invalid method name"})
			return
		}
		s.rpc.Unregister(sd.UserID, method, socketID)
		client.Emit("rpc-unregistered", protocolwire.RPCUnregisteredPayload{Method: method})
	})

	// RPC call
	client.On("rpc-call", func(data ...any) {
		sd := s.getSocketData(socketID)
		raw, ack := getFirstAnyWithAck(data)
		var req protocolwire.RPCCallPayload
		if err := decodeAny(raw, &req); err != nil {
			if ack != nil {
				ack(protocolwire.RPCAck{OK: false, Error: "Invalid parameters"})
			}
			return
		}
		forward, immediateAck := handlers.RPCCall(
			handlers.NewAuthContext(sd.UserID, sd.ClientType, socketID),
			s.rpc,
			req,
		)
		if immediateAck != nil {
			if ack != nil {
				ack(*immediateAck)
			}
			return
		}
		if forward == nil {
			if ack != nil {
				ack(protocolwire.RPCAck{OK: false, Error: "Internal error"})
			}
			return
		}

		targetSD := s.getSocketData(forward.TargetSocketID())
		if targetSD == nil || targetSD.Socket == nil {
			if ack != nil {
				ack(protocolwire.RPCAck{OK: false, Error: "RPC method not available"})
			}
			return
		}
		target := targetSD.Socket
		if shouldDebugRPC() {
			logger.Debugf(
				"RPC call: user=%s client=%s method=%s target=%s",
				sd.UserID,
				sd.ClientType,
				req.Method,
				target.Id(),
			)
		}

		target.Timeout(forward.Timeout()).EmitWithAck("rpc-request", forward.Request())(func(args []any, err error) {
			if ack == nil {
				return
			}
			if err != nil {
				ack(protocolwire.RPCAck{OK: false, Error: err.Error()})
				return
			}
			var result any
			if len(args) > 0 {
				result = args[0]
			}
			ack(protocolwire.RPCAck{OK: true, Result: result})
		})
	})

	// Disconnection handler
	client.On("disconnect", func(data ...any) {
		sd := s.getSocketData(socketID)
		reason := ""
		if len(data) > 0 {
			if r, ok := data[0].(string); ok {
				reason = r
			}
		}
		logger.Infof(
			"User disconnected: %s (socket %s, clientType: %s, reason: %s)",
			sd.UserID,
			socketID,
			sd.ClientType,
			reason,
		)

		result := handlers.DisconnectEffects(
			context.Background(),
			deps,
			handlers.NewAuthContext(sd.UserID, sd.ClientType, socketID),
			sd.SessionID,
			sd.TerminalID,
		)
		s.emitHandlerUpdates(socketID, result)

		s.socketData.Delete(socketID)
		s.rpc.UnregisterAll(sd.UserID, socketID)
	})
}
