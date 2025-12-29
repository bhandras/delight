package session

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type spawnEntry struct {
	SessionID string `json:"sessionId"`
	Directory string `json:"directory"`
}

type spawnRegistry struct {
	Owners map[string]map[string]spawnEntry `json:"owners"`
}

func (m *Manager) spawnRegistryPath() string {
	return filepath.Join(m.cfg.DelightHome, "spawned.sessions.json")
}

func (m *Manager) loadSpawnRegistryLocked() (*spawnRegistry, error) {
	registry := &spawnRegistry{Owners: make(map[string]map[string]spawnEntry)}
	data, err := os.ReadFile(m.spawnRegistryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return registry, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, registry); err != nil {
		return nil, err
	}
	if registry.Owners == nil {
		registry.Owners = make(map[string]map[string]spawnEntry)
	}
	return registry, nil
}

func (m *Manager) saveSpawnRegistryLocked(registry *spawnRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.spawnRegistryPath(), data, 0600)
}

func (m *Manager) registerSpawnedSession(sessionID, directory string) {
	if m.sessionID == "" {
		return
	}
	m.spawnStoreMu.Lock()
	defer m.spawnStoreMu.Unlock()

	registry, err := m.loadSpawnRegistryLocked()
	if err != nil {
		if m.debug {
			log.Printf("Failed to load spawn registry: %v", err)
		}
		return
	}

	owner := registry.Owners[m.sessionID]
	if owner == nil {
		owner = make(map[string]spawnEntry)
		registry.Owners[m.sessionID] = owner
	}
	owner[sessionID] = spawnEntry{SessionID: sessionID, Directory: directory}
	if err := m.saveSpawnRegistryLocked(registry); err != nil && m.debug {
		log.Printf("Failed to save spawn registry: %v", err)
	}
}

func (m *Manager) removeSpawnedSession(sessionID string) {
	if m.sessionID == "" {
		return
	}
	m.spawnStoreMu.Lock()
	defer m.spawnStoreMu.Unlock()

	registry, err := m.loadSpawnRegistryLocked()
	if err != nil {
		if m.debug {
			log.Printf("Failed to load spawn registry: %v", err)
		}
		return
	}

	owner := registry.Owners[m.sessionID]
	if owner == nil {
		return
	}
	delete(owner, sessionID)
	if len(owner) == 0 {
		delete(registry.Owners, m.sessionID)
	}
	if err := m.saveSpawnRegistryLocked(registry); err != nil && m.debug {
		log.Printf("Failed to save spawn registry: %v", err)
	}
}

func (m *Manager) restoreSpawnedSessions() error {
	if m.sessionID == "" {
		return nil
	}

	m.spawnStoreMu.Lock()
	registry, err := m.loadSpawnRegistryLocked()
	m.spawnStoreMu.Unlock()
	if err != nil {
		return err
	}

	owner := registry.Owners[m.sessionID]
	if len(owner) == 0 {
		return nil
	}

	for _, entry := range owner {
		if entry.Directory == "" || entry.SessionID == "" {
			continue
		}
		if entry.SessionID == m.sessionID {
			continue
		}

		m.spawnMu.Lock()
		_, exists := m.spawnedSessions[entry.SessionID]
		m.spawnMu.Unlock()
		if exists {
			continue
		}

		child, err := NewManager(m.cfg, m.token, m.debug)
		if err != nil {
			if m.debug {
				log.Printf("Failed to restore session %s: %v", entry.SessionID, err)
			}
			continue
		}
		child.disableMachineSocket = true

		if err := child.Start(entry.Directory); err != nil {
			if m.debug {
				log.Printf("Failed to start restored session %s: %v", entry.SessionID, err)
			}
			continue
		}
		if child.sessionID == "" {
			continue
		}

		m.spawnMu.Lock()
		m.spawnedSessions[child.sessionID] = child
		m.spawnMu.Unlock()

		if child.sessionID != entry.SessionID {
			m.registerSpawnedSession(child.sessionID, entry.Directory)
		}

		go func(sessionID string, mgr *Manager) {
			_ = mgr.Wait()
			m.spawnMu.Lock()
			delete(m.spawnedSessions, sessionID)
			m.spawnMu.Unlock()
		}(child.sessionID, child)
	}

	return nil
}
