package websocket

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/bhandras/delight/cli/internal/protocol/wire"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

// RPCHandler is a function that handles an RPC call and returns a response
type RPCHandler func(params json.RawMessage) (json.RawMessage, error)

// RPCManager manages RPC handlers for Socket.IO
type RPCManager struct {
	client   *Client
	handlers map[string]RPCHandler
	mu       sync.RWMutex
	debug    bool

	// Encryption helpers (set by session manager)
	encryptFunc func([]byte) (string, error)
	decryptFunc func(string) ([]byte, error)
}

// NewRPCManager creates a new RPC manager
func NewRPCManager(client *Client, debug bool) *RPCManager {
	return &RPCManager{
		client:   client,
		handlers: make(map[string]RPCHandler),
		debug:    debug,
	}
}

// SetEncryption sets the encryption/decryption functions for RPC messages
func (m *RPCManager) SetEncryption(
	encryptFunc func([]byte) (string, error),
	decryptFunc func(string) ([]byte, error),
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.encryptFunc = encryptFunc
	m.decryptFunc = decryptFunc
}

// RegisterHandler registers an RPC handler for a method
// Method names can include a scope prefix like "sessionId:methodName"
func (m *RPCManager) RegisterHandler(method string, handler RPCHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[method] = handler
	if m.debug || shouldDebugRPC() {
		log.Printf("Registered RPC handler: %s", method)
	}
	if m.client != nil && m.client.IsConnected() {
		_ = m.client.EmitRaw("rpc-register", wire.RPCRegisterPayload{Method: method})
	}
}

// RegisterAll re-registers all handlers for the current connection.
func (m *RPCManager) RegisterAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.client == nil || !m.client.IsConnected() {
		return
	}
	for method := range m.handlers {
		if m.debug || shouldDebugRPC() {
			log.Printf("Re-registering RPC handler: %s", method)
		}
		_ = m.client.EmitRaw("rpc-register", wire.RPCRegisterPayload{Method: method})
	}
}

// UnregisterHandler removes an RPC handler
func (m *RPCManager) UnregisterHandler(method string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.handlers, method)
}

// SetupSocketHandlers sets up the Socket.IO event handlers for RPC
func (m *RPCManager) SetupSocketHandlers(sock interface{}) {
	// Type assert to get the socket
	s, ok := sock.(interface {
		On(types.EventName, ...types.EventListener) error
	})
	if !ok {
		log.Println("Failed to set up RPC handlers: invalid socket type")
		return
	}

	_ = s.On(types.EventName("connect"), func(args ...any) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.client == nil {
			return
		}
		for method := range m.handlers {
			_ = m.client.EmitRaw("rpc-register", wire.RPCRegisterPayload{Method: method})
		}
	})

	// Handle incoming RPC calls
	_ = s.On(types.EventName("rpc-request"), func(args ...any) {
		if len(args) < 2 {
			if m.debug {
				log.Printf("Invalid RPC call: not enough arguments")
			}
			return
		}

		// Parse RPC request
		data, ok := args[0].(map[string]interface{})
		if !ok {
			if m.debug {
				log.Printf("Invalid RPC call: data is not a map (%T)", args[0])
			}
			return
		}
		if shouldDebugRPC() {
			log.Printf("RPC request received: method=%v", data["method"])
		}

		// Get callback function for response
		if m.debug || shouldDebugRPC() {
			log.Printf("RPC request ack type: %T", args[len(args)-1])
		}
		var callback func(...any)
		if cb, ok := args[len(args)-1].(func(...any)); ok {
			callback = cb
		} else if cb, ok := args[len(args)-1].(func([]any, error)); ok {
			callback = func(resp ...any) {
				cb(resp, nil)
			}
		} else {
			cbVal := reflect.ValueOf(args[len(args)-1])
			cbType := cbVal.Type()
			errorType := reflect.TypeOf((*error)(nil)).Elem()
			if cbVal.IsValid() && cbVal.Kind() == reflect.Func &&
				cbType.NumIn() == 2 &&
				cbType.In(0).Kind() == reflect.Slice &&
				cbType.In(0).Elem().Kind() == reflect.Interface &&
				cbType.In(1) == errorType {
				callback = func(resp ...any) {
					cbVal.Call([]reflect.Value{
						reflect.ValueOf(resp),
						reflect.Zero(errorType),
					})
				}
			} else {
				if m.debug {
					log.Printf("Invalid RPC call: no callback function")
				}
				return
			}
		}

		// Process the RPC call
		go m.handleRPCCall(data, callback)
	})
}

// handleRPCCall processes an incoming RPC call
func (m *RPCManager) handleRPCCall(data map[string]interface{}, callback func(...any)) {
	method, _ := data["method"].(string)
	params, _ := data["params"].(string) // Encrypted params as base64 string

	if m.debug {
		log.Printf("RPC call: %s", method)
	}

	// Find handler
	m.mu.RLock()
	handler, ok := m.handlers[method]
	decryptFunc := m.decryptFunc
	encryptFunc := m.encryptFunc
	m.mu.RUnlock()

	if !ok {
		if m.debug {
			log.Printf("No handler for RPC method: %s", method)
		}
		callback(wire.ErrorResponse{Error: fmt.Sprintf("unknown method: %s", method)})
		return
	}

	// Decrypt params if encryption is set up
	var paramsJSON json.RawMessage
	usedPlainParams := false
	if params != "" && decryptFunc != nil {
		decrypted, err := decryptFunc(params)
		if err != nil {
			if json.Valid([]byte(params)) {
				// Accept plain JSON for legacy/test clients.
				paramsJSON = json.RawMessage(params)
				usedPlainParams = true
			} else {
				if m.debug {
					log.Printf("Failed to decrypt RPC params: %v", err)
				}
				callback(wire.ErrorResponse{Error: "decryption failed"})
				return
			}
		}
		if paramsJSON == nil {
			paramsJSON = json.RawMessage(decrypted)
		}
	} else if params != "" {
		paramsJSON = json.RawMessage(params)
	}

	// Call handler
	result, err := handler(paramsJSON)
	if err != nil {
		if m.debug {
			log.Printf("RPC handler error: %v", err)
		}
		callback(wire.ErrorResponse{Error: err.Error()})
		return
	}

	// Encrypt result if encryption is set up
	var response interface{}
	if encryptFunc != nil && result != nil && !usedPlainParams {
		encrypted, err := encryptFunc(result)
		if err != nil {
			if m.debug {
				log.Printf("Failed to encrypt RPC result: %v", err)
			}
			callback(wire.ErrorResponse{Error: "encryption failed"})
			return
		}
		response = encrypted
	} else {
		response = result
	}

	callback(response)
}

func shouldDebugRPC() bool {
	if val := os.Getenv("DELIGHT_DEBUG_RPC"); strings.EqualFold(val, "true") || strings.EqualFold(val, "1") {
		return true
	}
	return strings.EqualFold(os.Getenv("HAPPY_DEBUG_RPC"), "true") ||
		strings.EqualFold(os.Getenv("HAPPY_DEBUG_RPC"), "1")
}

// Call makes an RPC call to the server
func (m *RPCManager) Call(method string, params interface{}) (json.RawMessage, error) {
	// This would need Socket.IO emit with acknowledgement
	// For now, return an error as we mainly need to handle incoming RPCs
	return nil, fmt.Errorf("outgoing RPC calls not implemented")
}
