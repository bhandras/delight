package wire

// RPCCallPayload is the client -> server payload for the "rpc-call" event.
type RPCCallPayload struct {
	// Method is the RPC method name.
	Method string `json:"method"`
	// Params is a JSON-encoded string.
	Params string `json:"params"`
}

// RPCRequestPayload is the server -> client payload for the "rpc-request" event.
type RPCRequestPayload struct {
	// Method is the RPC method name.
	Method string `json:"method"`
	// Params is a JSON-encoded string.
	Params string `json:"params"`
}
