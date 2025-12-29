package wire

import (
	"encoding/json"
	"fmt"
)

// MessageEvent represents the server -> CLI "message" event payload.
//
// Over time we've seen multiple shapes for the encrypted payload:
// - {"message":"<cipher>"}
// - {"message":{"content":"<cipher>"}}
// - {"message":{"content":{"t":"encrypted","c":"<cipher>"}}}
// - {"content":"<cipher>"} or {"content":{"c":"<cipher>"}}
//
// This type is intentionally permissive and is used only to extract the
// ciphertext and idempotency metadata.
type MessageEvent struct {
	// SID is the session id for this event.
	SID string `json:"sid"`
	// LocalID is an optional idempotency key for optimistic reconciliation.
	LocalID string `json:"localId,omitempty"`
	// Message contains the encrypted payload (shape varies by client).
	Message json.RawMessage `json:"message,omitempty"`
	// Content contains the encrypted payload (legacy web shape).
	Content json.RawMessage `json:"content,omitempty"`
}

// ExtractMessageCipher extracts the ciphertext from a "message" event payload.
// It returns (cipher, localID, ok, err).
func ExtractMessageCipher(v any) (cipher string, localID string, ok bool, err error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", "", false, err
	}
	var evt MessageEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return "", "", false, err
	}
	localID = evt.LocalID

	if c, ok, err := extractCipherFromRaw(evt.Message); err != nil {
		return "", localID, false, err
	} else if ok && c != "" {
		return c, localID, true, nil
	}

	if c, ok, err := extractCipherFromRaw(evt.Content); err != nil {
		return "", localID, false, err
	} else if ok && c != "" {
		return c, localID, true, nil
	}

	return "", localID, false, nil
}

func extractCipherFromRaw(raw json.RawMessage) (cipher string, ok bool, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}

	// Case 1: ciphertext directly as a JSON string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return "", true, fmt.Errorf("ciphertext is empty")
		}
		return s, true, nil
	}

	// Case 2: object wrapper.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", true, err
	}

	// A) {"c":"..."} or {"t":"encrypted","c":"..."}
	if v, ok := obj["c"]; ok {
		var c string
		if err := json.Unmarshal(v, &c); err != nil {
			return "", true, err
		}
		if c == "" {
			return "", true, fmt.Errorf("ciphertext is empty")
		}
		return c, true, nil
	}

	// B) {"content": <string|object>}
	if v, ok := obj["content"]; ok {
		return extractCipherFromRaw(v)
	}

	return "", true, fmt.Errorf("unsupported ciphertext envelope")
}

// UserTextRecord is the plaintext record shape sent from the mobile app.
//
// This is the decrypted payload (not the encrypted wire envelope). Only
// role=user + content.type=text records are treated as user input.
type UserTextRecord struct {
	// Role identifies the sender ("user", "assistant", etc.).
	Role string `json:"role"`
	// Content holds the typed payload for the message.
	Content struct {
		// Type identifies the content kind (e.g. "text").
		Type string `json:"type"`
		// Text is the message body when Type=="text".
		Text string `json:"text"`
	} `json:"content"`
	// Meta is optional metadata (e.g. model/permissionMode for Codex).
	Meta map[string]any `json:"meta,omitempty"`
}

// ParseUserTextRecord parses a decrypted record and returns user text + meta.
//
// It returns (text, meta, ok, err). ok is true only for role=user and
// content.type=text with a non-empty text.
func ParseUserTextRecord(decrypted []byte) (string, map[string]any, bool, error) {
	var rec UserTextRecord
	if err := json.Unmarshal(decrypted, &rec); err != nil {
		return "", nil, false, err
	}
	if rec.Role != "user" || rec.Content.Type != "text" || rec.Content.Text == "" {
		return "", rec.Meta, false, nil
	}
	return rec.Content.Text, rec.Meta, true, nil
}
