package webclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bhandras/delight/shared/webapi"
)

// resolveStatePath returns the absolute state file path.
func resolveStatePath(cfg Config) (string, error) {
	if trimmed := strings.TrimSpace(cfg.StatePath); trimmed != "" {
		return trimmed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, defaultStateDirName, stateFileName), nil
}

// loadState reads persisted runtime state or returns defaults.
func loadState(path, defaultServerURL string) (persistentState, error) {
	state := persistentState{
		ServerURL:   strings.TrimRight(strings.TrimSpace(defaultServerURL), "/"),
		Preferences: defaultPreferences(),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, fmt.Errorf("read state file: %w", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, fmt.Errorf("decode state file: %w", err)
	}
	if state.ServerURL == "" {
		state.ServerURL = strings.TrimRight(strings.TrimSpace(defaultServerURL), "/")
	}
	state.Preferences = normalizePreferences(state.Preferences)
	return state, nil
}

// saveState writes runtime state atomically.
func saveState(path string, state persistentState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit state file: %w", err)
	}
	return nil
}

// defaultPreferences returns default appearance and transcript preferences.
func defaultPreferences() webapi.Preferences {
	return webapi.Preferences{
		AppearanceMode: "system",
		GlobalTranscript: webapi.TranscriptSettings{
			ShowToolUse:            true,
			ShowToolOutput:         true,
			ShowReasoningSummaries: true,
			FontSize:               14,
		},
		PerTerminalTranscript: make(map[string]webapi.TranscriptSettings),
	}
}

// normalizePreferences ensures required fields are initialized.
func normalizePreferences(pref webapi.Preferences) webapi.Preferences {
	if pref.AppearanceMode == "" {
		pref.AppearanceMode = "system"
	}
	if pref.GlobalTranscript.FontSize <= 0 {
		pref.GlobalTranscript.FontSize = 14
	}
	if pref.PerTerminalTranscript == nil {
		pref.PerTerminalTranscript = make(map[string]webapi.TranscriptSettings)
	}
	return pref
}
