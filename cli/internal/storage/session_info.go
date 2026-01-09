package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalSessionInfo is durable, machine-local session metadata persisted under
// DelightHome.
//
// This data should never be sent to the server because it can contain
// machine-specific paths (e.g. Codex rollout JSONL locations).
type LocalSessionInfo struct {
	// SessionID is the Delight session id (server-generated).
	SessionID string `json:"sessionId"`
	// AgentType is the active agent type for the session (e.g. "codex").
	AgentType string `json:"agentType,omitempty"`
	// ResumeToken is the agent-specific session identifier (e.g. Codex thread id).
	ResumeToken string `json:"resumeToken,omitempty"`
	// RolloutPath is an agent-specific local event-log path. For Codex this is
	// the rollout JSONL file path.
	RolloutPath string `json:"rolloutPath,omitempty"`
	// UpdatedAtMs is the wall-clock timestamp of the most recent write.
	UpdatedAtMs int64 `json:"updatedAtMs,omitempty"`
}

// LoadLocalSessionInfo reads the LocalSessionInfo for a Delight session id.
//
// ok is false when no entry exists.
func LoadLocalSessionInfo(delightHome string, sessionID string) (info LocalSessionInfo, ok bool, err error) {
	path, err := localSessionInfoPath(delightHome, sessionID)
	if err != nil {
		return LocalSessionInfo{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LocalSessionInfo{}, false, nil
		}
		return LocalSessionInfo{}, false, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return LocalSessionInfo{}, false, err
	}
	return info, true, nil
}

// SaveLocalSessionInfo writes the LocalSessionInfo entry to disk.
func SaveLocalSessionInfo(delightHome string, info LocalSessionInfo) error {
	if strings.TrimSpace(info.SessionID) == "" {
		return fmt.Errorf("missing session id")
	}
	path, err := localSessionInfoPath(delightHome, info.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	info.UpdatedAtMs = time.Now().UnixMilli()
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// UpdateLocalSessionInfo loads, mutates, and persists a LocalSessionInfo entry.
func UpdateLocalSessionInfo(delightHome string, sessionID string, update func(*LocalSessionInfo)) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("missing session id")
	}
	info := LocalSessionInfo{SessionID: sessionID}
	if existing, ok, err := LoadLocalSessionInfo(delightHome, sessionID); err != nil {
		return err
	} else if ok {
		info = existing
	}
	update(&info)
	info.SessionID = sessionID
	return SaveLocalSessionInfo(delightHome, info)
}

// localSessionInfoPath returns the absolute path for local session metadata.
func localSessionInfoPath(delightHome string, sessionID string) (string, error) {
	if strings.TrimSpace(delightHome) == "" {
		return "", fmt.Errorf("missing delight home")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("missing session id")
	}
	// Defensively prevent path traversal if session ids ever become untrusted.
	sessionID = strings.ReplaceAll(sessionID, string(os.PathSeparator), "_")
	return filepath.Join(delightHome, "sessions", sessionID, "local.json"), nil
}
