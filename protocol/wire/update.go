package wire

import (
	"encoding/json"
	"fmt"
)

// UpdateEnvelope is the typed wrapper for server "update" events.
type UpdateEnvelope struct {
	// Body is the typed payload for an update event.
	Body UpdateBody `json:"body"`
}

// UpdateBody is the payload inside an update event.
type UpdateBody struct {
	// T is the update type (e.g. "new-message").
	T string `json:"t"`
	// Message contains the payload for message-oriented updates.
	Message *UpdateMessage `json:"message,omitempty"`
}

// UpdateMessage is the message payload inside an update event.
type UpdateMessage struct {
	// LocalID is the sender-supplied idempotency key (when available).
	LocalID *string `json:"localId,omitempty"`
	// Content is the encrypted content payload.
	Content *EncryptedContent `json:"content,omitempty"`
}

// EncryptedContent contains ciphertext for an encrypted message.
type EncryptedContent struct {
	// C is the ciphertext.
	C string `json:"c"`
}

// ParseUpdateEnvelope parses an update event payload into a typed envelope.
func ParseUpdateEnvelope(v any) (*UpdateEnvelope, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var env UpdateEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// ExtractNewMessageCipher extracts the ciphertext from an update event when
// `body.t == "new-message"`.
func ExtractNewMessageCipher(v any) (string, bool, error) {
	cipher, _, ok, err := ExtractNewMessageCipherAndLocalID(v)
	return cipher, ok, err
}

// ExtractNewMessageCipherAndLocalID extracts the ciphertext and local id from an
// update event when `body.t == "new-message"`.
func ExtractNewMessageCipherAndLocalID(v any) (cipher string, localID string, ok bool, err error) {
	env, err := ParseUpdateEnvelope(v)
	if err != nil {
		return "", "", false, err
	}
	if env.Body.T != "new-message" {
		return "", "", false, nil
	}
	if env.Body.Message == nil || env.Body.Message.Content == nil || env.Body.Message.Content.C == "" {
		return "", "", false, fmt.Errorf("new-message missing message.content.c")
	}
	if env.Body.Message.LocalID != nil {
		localID = *env.Body.Message.LocalID
	}
	return env.Body.Message.Content.C, localID, true, nil
}
