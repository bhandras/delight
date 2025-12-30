package wire

import (
	"encoding/json"
	"fmt"
)

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

// AgentCodexContent is a raw record content object of type "codex".
type AgentCodexContent struct {
	// Type must be "codex".
	Type string `json:"type"`
	// Data is the Codex event payload.
	Data any `json:"data"`
}

// AgentCodexRecord is the plaintext raw record emitted for Codex events.
type AgentCodexRecord struct {
	// Role must be "agent".
	Role string `json:"role"`
	// Content is the Codex record content object.
	Content AgentCodexContent `json:"content"`
}

// CodexRecord is the structured payload embedded inside an AgentCodexRecord.
//
// Fields like Input/Output may contain arbitrary JSON objects depending on the
// Codex tool being invoked.
type CodexRecord struct {
	// Type identifies the Codex record kind.
	Type string `json:"type"`
	// Message contains user-visible text for message/reasoning records.
	Message string `json:"message,omitempty"`
	// CallID identifies a tool call for tool-call records.
	CallID string `json:"callId,omitempty"`
	// Name is the tool name for tool-call records.
	Name string `json:"name,omitempty"`
	// Input is the tool input object for tool-call records.
	Input any `json:"input,omitempty"`
	// Output is the tool output object for tool-call-result records.
	Output any `json:"output,omitempty"`
	// ID is a unique identifier for record correlation.
	ID string `json:"id,omitempty"`
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
