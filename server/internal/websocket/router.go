package websocket

import (
	"log"
)

// EventRouter handles routing of events to appropriate connections
type EventRouter struct {
	manager *ConnectionManager
}

// NewEventRouter creates a new event router
func NewEventRouter(manager *ConnectionManager) *EventRouter {
	return &EventRouter{
		manager: manager,
	}
}

// EmitUpdate sends an update to appropriate recipients
func (r *EventRouter) EmitUpdate(userID string, payload UpdatePayload, filter *RecipientFilter, skipSocketID string) {
	connections := r.manager.GetUserConnections(userID)

	for _, conn := range connections {
		// Skip sender to avoid echo
		if skipSocketID != "" && conn.Socket.ID() == skipSocketID {
			continue
		}

		// Apply filter
		if filter != nil && !r.shouldSendToConnection(conn, filter) {
			continue
		}

		// Send update
		conn.Socket.Emit("update", payload)
	}
}

// EmitEphemeral sends an ephemeral event to appropriate recipients
func (r *EventRouter) EmitEphemeral(userID string, payload EphemeralPayload, filter *RecipientFilter, skipSocketID string) {
	connections := r.manager.GetUserConnections(userID)

	for _, conn := range connections {
		// Skip sender
		if skipSocketID != "" && conn.Socket.ID() == skipSocketID {
			continue
		}

		// Apply filter
		if filter != nil && !r.shouldSendToConnection(conn, filter) {
			continue
		}

		// Send ephemeral event
		conn.Socket.Emit("ephemeral", payload)
	}
}

// shouldSendToConnection determines if a connection should receive the event
func (r *EventRouter) shouldSendToConnection(conn *ClientConnection, filter *RecipientFilter) bool {
	switch filter.Type {
	case FilterAllInterestedInSession:
		// Send to:
		// - session-scoped connections with matching sessionId
		// - all user-scoped connections
		// - NOT machine-scoped connections
		if conn.Type == ConnectionTypeSessionScoped {
			return conn.SessionID == filter.SessionID
		}
		if conn.Type == ConnectionTypeMachineScoped {
			return false
		}
		return true // user-scoped always gets it

	case FilterUserScopedOnly:
		return conn.Type == ConnectionTypeUserScoped

	case FilterAllUserAuthenticated:
		return true // Send to all connections for this user

	default:
		log.Printf("Unknown filter type: %s", filter.Type)
		return false
	}
}

// BroadcastToSession sends an event to all connections interested in a session
func (r *EventRouter) BroadcastToSession(userID string, sessionID string, payload UpdatePayload, skipSocketID string) {
	filter := &RecipientFilter{
		Type:      FilterAllInterestedInSession,
		SessionID: sessionID,
	}
	r.EmitUpdate(userID, payload, filter, skipSocketID)
}

// BroadcastToUser sends an event to all user connections
func (r *EventRouter) BroadcastToUser(userID string, payload UpdatePayload, skipSocketID string) {
	filter := &RecipientFilter{
		Type: FilterAllUserAuthenticated,
	}
	r.EmitUpdate(userID, payload, filter, skipSocketID)
}
