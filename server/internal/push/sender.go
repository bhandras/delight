package push

import "context"

// Result reports aggregate push delivery outcomes.
type Result struct {
	Sent   int
	Failed int
}

// Sender delivers encrypted push payloads to device tokens.
type Sender interface {
	// SendEncrypted sends ciphertext to the provided device tokens.
	SendEncrypted(ctx context.Context, deviceTokens []string, ciphertext string) (Result, error)
}
