package wire

import (
	"encoding/json"
	"fmt"
)

// DecodeContentBlocks decodes a `message.content` value into []ContentBlock.
//
// The CLI receives content blocks in a few shapes depending on the agent SDK:
// - already decoded as `[]ContentBlock`
// - decoded as `[]any` / `[]map[string]any`
// - raw JSON bytes (`json.RawMessage` / `[]byte`)
//
// This helper normalizes all shapes into `[]ContentBlock` without discarding
// unknown per-block fields.
func DecodeContentBlocks(v any) ([]ContentBlock, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []ContentBlock:
		return t, nil
	case json.RawMessage:
		var out []ContentBlock
		if err := json.Unmarshal(t, &out); err != nil {
			return nil, err
		}
		return out, nil
	case []byte:
		var out []ContentBlock
		if err := json.Unmarshal(t, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		var out []ContentBlock
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// TryParseAgentOutputRecord parses a raw record JSON blob as an AgentOutputRecord.
//
// ok is false when the JSON does not look like an output record; in that case
// err is nil and callers should ignore the payload.
func TryParseAgentOutputRecord(data []byte) (_ *AgentOutputRecord, ok bool, _ error) {
	if len(data) == 0 {
		return nil, false, nil
	}

	var rec AgentOutputRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, false, err
	}
	if rec.Role != "agent" || rec.Content.Type != "output" {
		return nil, false, nil
	}
	return &rec, true, nil
}

// ValidateContentBlocks ensures each block has a non-empty type.
func ValidateContentBlocks(blocks []ContentBlock) error {
	return validateContentBlocks(blocks)
}

// ContentBlock is a single structured content block within a message.
//
// The protocol permits many block types (text/tool-use/tool-result/etc).
// This type preserves unknown fields to avoid losing information.
type ContentBlock struct {
	// Type identifies the block kind (e.g. "text").
	Type string `json:"type"`
	// Text contains the block text when Type=="text".
	Text string `json:"text,omitempty"`
	// Fields stores additional block-specific attributes.
	Fields map[string]any `json:"-"`
}

// MarshalJSON preserves the block fields while ensuring Type/Text are included.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(b.Fields)+2)
	for k, v := range b.Fields {
		out[k] = v
	}
	if b.Type != "" {
		out["type"] = b.Type
	}
	if b.Text != "" {
		out["text"] = b.Text
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes a content block while preserving unknown fields.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	blockType, _ := raw["type"].(string)
	text, _ := raw["text"].(string)
	delete(raw, "type")
	delete(raw, "text")

	b.Type = blockType
	b.Text = text
	if len(raw) == 0 {
		b.Fields = nil
		return nil
	}
	b.Fields = raw
	return nil
}

// AgentMessage is the plaintext message embedded inside an encrypted raw record.
type AgentMessage struct {
	// Role is the message role ("assistant"/"user"/etc).
	Role string `json:"role"`
	// Model is the model identifier (assistant messages only).
	Model string `json:"model,omitempty"`
	// Content is a structured list of content blocks.
	Content []ContentBlock `json:"content"`
	// Usage contains model-specific usage information when available.
	Usage any `json:"usage,omitempty"`
}

// AgentOutputData is the "output" record data object.
type AgentOutputData struct {
	// Type identifies the output kind ("assistant"/"user"/etc).
	Type string `json:"type"`
	// IsSidechain indicates whether the message is a sidechain.
	IsSidechain bool `json:"isSidechain"`
	// IsCompactSummary indicates whether the output is a compact summary.
	IsCompactSummary bool `json:"isCompactSummary"`
	// IsMeta indicates whether the output is meta information.
	IsMeta bool `json:"isMeta"`
	// UUID identifies this output record.
	UUID string `json:"uuid"`
	// ParentUUID references the parent record when present.
	ParentUUID *string `json:"parentUuid"`
	// Message contains the assistant/user message payload.
	Message AgentMessage `json:"message"`
	// ParentToolUseID identifies the tool-use relationship when present.
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
}

// AgentOutputContent is a raw record content object of type "output".
type AgentOutputContent struct {
	// Type must be "output".
	Type string `json:"type"`
	// Data is the output record data object.
	Data AgentOutputData `json:"data"`
}

// AgentOutputRecord is the plaintext raw record emitted by agents.
type AgentOutputRecord struct {
	// Role must be "agent".
	Role string `json:"role"`
	// Content is the output record content object.
	Content AgentOutputContent `json:"content"`
}

func validateContentBlocks(blocks []ContentBlock) error {
	if len(blocks) == 0 {
		return fmt.Errorf("content blocks required")
	}
	for i, block := range blocks {
		if block.Type == "" {
			return fmt.Errorf("content block %d missing type", i)
		}
	}
	return nil
}
