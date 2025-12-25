package types

import (
	"fmt"
	"time"
)

// CUID generates a unique identifier (simplified version)
func NewCUID() string {
	return fmt.Sprintf("c%d", time.Now().UnixNano())
}

// Metadata represents session metadata (decrypted)
type Metadata struct {
	Path            string `json:"path"`
	Host            string `json:"host"`
	Version         string `json:"version"`
	OS              string `json:"os"`
	MachineID       string `json:"machineId"`
	HomeDir         string `json:"homeDir"`
	DelightHomeDir  string `json:"happyHomeDir"`
	Flavor          string `json:"flavor,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
}

// AgentState represents agent state (decrypted)
type AgentState struct {
	ControlledByUser bool `json:"controlledByUser"`
}

// UserMessage represents a message from the user
type UserMessage struct {
	Role    string                 `json:"role"`
	Content map[string]interface{} `json:"content"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
}

// AgentMessage represents a message from Claude
type AgentMessage struct {
	Role    string                 `json:"role"`
	Content map[string]interface{} `json:"content"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
}

// MachineMetadata represents machine/terminal metadata
type MachineMetadata struct {
	Host              string `json:"host"`
	Platform          string `json:"platform"`
	DelightCliVersion string `json:"happyCliVersion"`
	HomeDir           string `json:"homeDir"`
	DelightHomeDir    string `json:"happyHomeDir"`
}

// DaemonState represents daemon runtime state
type DaemonState struct {
	Status    string `json:"status"` // "running" | "offline" | "shutting-down"
	PID       int    `json:"pid,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
}
