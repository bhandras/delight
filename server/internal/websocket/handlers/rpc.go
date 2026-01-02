package handlers

import (
	"time"

	protocolwire "github.com/bhandras/delight/shared/wire"
)

// RPCMethodLocator provides read-only lookup for an RPC method target socket.
type RPCMethodLocator interface {
	GetSocketID(userID, method string) (string, bool)
}

// RPCForward describes a single rpc-request that should be forwarded to another
// socket by the transport adapter.
type RPCForward struct {
	targetSocketID string
	request        protocolwire.RPCRequestPayload
	timeout        time.Duration
}

// TargetSocketID returns the target socket id to forward to.
func (r RPCForward) TargetSocketID() string { return r.targetSocketID }

// Request returns the rpc-request payload to forward.
func (r RPCForward) Request() protocolwire.RPCRequestPayload { return r.request }

// Timeout returns the per-request timeout to use when forwarding.
func (r RPCForward) Timeout() time.Duration { return r.timeout }

// RPCCall validates an rpc-call request and returns either a forward
// instruction (on success) or an immediate ACK (on failure).
func RPCCall(auth AuthContext, locator RPCMethodLocator, req protocolwire.RPCCallPayload) (*RPCForward, *protocolwire.RPCAck) {
	if req.Method == "" {
		ack := protocolwire.RPCAck{OK: false, Error: "Invalid parameters: method is required"}
		return nil, &ack
	}

	targetSocketID, ok := locator.GetSocketID(auth.UserID(), req.Method)
	if !ok {
		ack := protocolwire.RPCAck{OK: false, Error: "RPC method not available"}
		return nil, &ack
	}
	if targetSocketID == auth.SocketID() {
		ack := protocolwire.RPCAck{OK: false, Error: "Cannot call RPC on the same socket"}
		return nil, &ack
	}

	forward := RPCForward{
		targetSocketID: targetSocketID,
		request: protocolwire.RPCRequestPayload{
			Method: req.Method,
			Params: req.Params,
		},
		timeout: 30 * time.Second,
	}
	return &forward, nil
}
