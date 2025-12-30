package websocket

import "sync"

// RPCRegistry tracks RPC method registrations per user in a concurrency-safe
// way. It stores socket ids (not socket pointers) so lookups can be validated
// against the current connection map.
type RPCRegistry struct {
	mu      sync.RWMutex
	methods map[string]map[string]string // userID -> method -> socketID
}

func NewRPCRegistry() *RPCRegistry {
	return &RPCRegistry{
		methods: make(map[string]map[string]string),
	}
}

func (r *RPCRegistry) Register(userID, method, socketID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	methods, ok := r.methods[userID]
	if !ok {
		methods = make(map[string]string)
		r.methods[userID] = methods
	}
	methods[method] = socketID
}

func (r *RPCRegistry) Unregister(userID, method, socketID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	methods, ok := r.methods[userID]
	if !ok {
		return
	}
	if current, ok := methods[method]; ok && current == socketID {
		delete(methods, method)
	}
	if len(methods) == 0 {
		delete(r.methods, userID)
	}
}

func (r *RPCRegistry) UnregisterAll(userID, socketID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	methods, ok := r.methods[userID]
	if !ok {
		return
	}
	for method, current := range methods {
		if current == socketID {
			delete(methods, method)
		}
	}
	if len(methods) == 0 {
		delete(r.methods, userID)
	}
}

func (r *RPCRegistry) GetSocketID(userID, method string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	methods, ok := r.methods[userID]
	if !ok {
		return "", false
	}
	socketID, ok := methods[method]
	return socketID, ok && socketID != ""
}
