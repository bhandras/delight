package websocket

import (
	"sync"
)

// ConnectionManager manages all active WebSocket connections
type ConnectionManager struct {
	mu          sync.RWMutex
	connections map[string][]*ClientConnection // userID -> connections
}

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections: make(map[string][]*ClientConnection),
	}
}

// AddConnection registers a new connection
func (m *ConnectionManager) AddConnection(conn *ClientConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connections[conn.UserID] == nil {
		m.connections[conn.UserID] = make([]*ClientConnection, 0)
	}
	m.connections[conn.UserID] = append(m.connections[conn.UserID], conn)
}

// RemoveConnection removes a connection
func (m *ConnectionManager) RemoveConnection(socketID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for userID, conns := range m.connections {
		for i, conn := range conns {
			if conn.Socket.ID() == socketID {
				// Remove from slice
				m.connections[userID] = append(conns[:i], conns[i+1:]...)

				// Clean up empty user entries
				if len(m.connections[userID]) == 0 {
					delete(m.connections, userID)
				}
				return
			}
		}
	}
}

// GetUserConnections returns all connections for a user
func (m *ConnectionManager) GetUserConnections(userID string) []*ClientConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conns := m.connections[userID]
	if conns == nil {
		return []*ClientConnection{}
	}

	// Return a copy to avoid race conditions
	result := make([]*ClientConnection, len(conns))
	copy(result, conns)
	return result
}

// GetConnection finds a connection by socket ID
func (m *ConnectionManager) GetConnection(socketID string) *ClientConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conns := range m.connections {
		for _, conn := range conns {
			if conn.Socket.ID() == socketID {
				return conn
			}
		}
	}
	return nil
}

// GetConnectionCount returns the total number of active connections
func (m *ConnectionManager) GetConnectionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, conns := range m.connections {
		count += len(conns)
	}
	return count
}

// GetUserCount returns the number of users with active connections
func (m *ConnectionManager) GetUserCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.connections)
}
