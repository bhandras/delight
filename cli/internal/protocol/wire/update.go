package wire

import (
	"encoding/json"
	"fmt"
)

type UpdateEnvelope struct {
	Body UpdateBody `json:"body"`
}

type UpdateBody struct {
	T       string         `json:"t"`
	Message *UpdateMessage `json:"message,omitempty"`
}

type UpdateMessage struct {
	Content *EncryptedContent `json:"content,omitempty"`
}

type EncryptedContent struct {
	C string `json:"c"`
}

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
