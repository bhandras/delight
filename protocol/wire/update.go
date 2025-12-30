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
	env, err := ParseUpdateEnvelope(v)
	if err != nil {
		return "", false, err
	}
	if env.Body.T != "new-message" {
		return "", false, nil
	}
	if env.Body.Message == nil || env.Body.Message.Content == nil || env.Body.Message.Content.C == "" {
		return "", false, fmt.Errorf("new-message missing message.content.c")
	}
	return env.Body.Message.Content.C, true, nil
}
