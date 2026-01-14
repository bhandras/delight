package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/shared/logger"
)

// spawnEntry is a durable record describing a spawned child session.
type spawnEntry struct {
	SessionID string `json:"sessionId"`
	Directory string `json:"directory"`
}

// spawnRegistry tracks spawned sessions per parent session (owner).
type spawnRegistry struct {
	Owners map[string]map[string]spawnEntry `json:"owners"`
}

type spawnActorState struct {
	OwnerSessionID string

	// Pending maps request id -> reply channel.
	Pending map[int64]chan spawnResult

	// NextReq is incremented for each async request that needs a reply later.
	NextReq int64

	Stopping bool
}

type spawnResult struct {
	SessionID string
	Err       error
}

type cmdSpawnChild struct {
	framework.InputBase
	Directory string
	Agent     string
	Reply     chan spawnResult
}

type cmdStopChild struct {
	framework.InputBase
	SessionID string
	Reply     chan spawnResult
}

type cmdRestoreChildren struct {
	framework.InputBase
	Reply chan spawnResult
}

type cmdShutdownChildren struct {
	framework.InputBase
	Reply chan spawnResult
}

type evChildStarted struct {
	framework.InputBase
	ReqID     int64
	SessionID string
	Directory string
}

type evChildStartFailed struct {
	framework.InputBase
	ReqID int64
	Err   error
}

type evChildStopped struct {
	framework.InputBase
	ReqID int64
	Err   error
}

type effStartChild struct {
	framework.EffectBase
	ReqID     int64
	Directory string
	Agent     string
}

type effStopChild struct {
	framework.EffectBase
	ReqID     int64
	SessionID string
}

type effRestoreChildren struct {
	framework.EffectBase
	ReqID int64
}

type effShutdownChildren struct {
	framework.EffectBase
	ReqID int64
}

// effCompleteSpawnReply completes a spawn actor reply channel after state has
// been applied by the actor loop.
type effCompleteSpawnReply struct {
	framework.EffectBase
	Reply  chan spawnResult
	Result spawnResult
}

func reduceSpawnActor(state spawnActorState, input framework.Input) (spawnActorState, []framework.Effect) {
	switch in := input.(type) {
	case cmdSpawnChild:
		if state.Stopping {
			if in.Reply == nil {
				return state, nil
			}
			return state, []framework.Effect{effCompleteSpawnReply{
				Reply:  in.Reply,
				Result: spawnResult{Err: fmt.Errorf("spawner shutting down")},
			}}
		}
		if state.Pending == nil {
			state.Pending = make(map[int64]chan spawnResult)
		}
		state.NextReq++
		reqID := state.NextReq
		state.Pending[reqID] = in.Reply
		return state, []framework.Effect{effStartChild{ReqID: reqID, Directory: in.Directory, Agent: in.Agent}}

	case cmdStopChild:
		if state.Pending == nil {
			state.Pending = make(map[int64]chan spawnResult)
		}
		state.NextReq++
		reqID := state.NextReq
		state.Pending[reqID] = in.Reply
		return state, []framework.Effect{effStopChild{ReqID: reqID, SessionID: in.SessionID}}

	case cmdRestoreChildren:
		if state.Pending == nil {
			state.Pending = make(map[int64]chan spawnResult)
		}
		state.NextReq++
		reqID := state.NextReq
		state.Pending[reqID] = in.Reply
		return state, []framework.Effect{effRestoreChildren{ReqID: reqID}}

	case cmdShutdownChildren:
		if state.Pending == nil {
			state.Pending = make(map[int64]chan spawnResult)
		}
		state.Stopping = true
		state.NextReq++
		reqID := state.NextReq
		state.Pending[reqID] = in.Reply
		return state, []framework.Effect{effShutdownChildren{ReqID: reqID}}

	case evChildStarted:
		reply, ok := state.Pending[in.ReqID]
		if ok {
			delete(state.Pending, in.ReqID)
			if reply != nil {
				return state, []framework.Effect{effCompleteSpawnReply{
					Reply:  reply,
					Result: spawnResult{SessionID: in.SessionID},
				}}
			}
		}
		return state, nil

	case evChildStartFailed:
		reply, ok := state.Pending[in.ReqID]
		if ok {
			delete(state.Pending, in.ReqID)
			if reply != nil {
				return state, []framework.Effect{effCompleteSpawnReply{
					Reply:  reply,
					Result: spawnResult{Err: in.Err},
				}}
			}
		}
		return state, nil

	case evChildStopped:
		reply, ok := state.Pending[in.ReqID]
		if ok {
			delete(state.Pending, in.ReqID)
			if reply != nil {
				return state, []framework.Effect{effCompleteSpawnReply{
					Reply:  reply,
					Result: spawnResult{Err: in.Err},
				}}
			}
		}
		return state, nil

	default:
		return state, nil
	}
}

type spawnActorRuntime struct {
	cfg     *config.Config
	debug   bool
	ownerID string

	tokenMu sync.RWMutex
	token   string

	childrenMu sync.Mutex
	children   map[string]spawnedChild

	emit func(framework.Input)

	// startChild starts a child session manager. It is injected to keep
	// spawn actor tests deterministic and free of network dependencies.
	startChild spawnChildStarter
}

// setToken updates the auth token used when starting new child sessions.
func (r *spawnActorRuntime) setToken(token string) {
	r.tokenMu.Lock()
	r.token = token
	r.tokenMu.Unlock()
}

// tokenForRequest returns the most recent auth token for spawn operations.
func (r *spawnActorRuntime) tokenForRequest() string {
	r.tokenMu.RLock()
	token := r.token
	r.tokenMu.RUnlock()
	return token
}

// spawnedChild is the minimal interface the spawn actor needs to manage child
// sessions.
type spawnedChild interface {
	Close() error
	Wait() error
}

// spawnChildStarter creates and starts a child session manager and returns the
// assigned session id.
type spawnChildStarter func(ctx context.Context, cfg *config.Config, token string, debug bool, directory string, agent string) (string, spawnedChild, error)

// defaultSpawnChildStarter is the production implementation of spawnChildStarter.
func defaultSpawnChildStarter(ctx context.Context, cfg *config.Config, token string, debug bool, directory string, agent string) (string, spawnedChild, error) {
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	child, err := NewManager(cfg, token, debug)
	if err != nil {
		return "", nil, err
	}
	if agent == "acp" || agent == "claude" || agent == "codex" {
		child.agent = agent
	}
	child.disableTerminalSocket = true

	if err := child.Start(directory); err != nil {
		return "", nil, err
	}
	if child.sessionID == "" {
		_ = child.Close()
		return "", nil, fmt.Errorf("session id not assigned")
	}
	return child.sessionID, child, nil
}

func (r *spawnActorRuntime) HandleEffects(ctx context.Context, effects []framework.Effect, emit func(framework.Input)) {
	if r.startChild == nil {
		r.startChild = defaultSpawnChildStarter
	}
	r.emit = emit
	for _, eff := range effects {
		select {
		case <-ctx.Done():
			return
		default:
		}
		switch e := eff.(type) {
		case effCompleteSpawnReply:
			if e.Reply != nil {
				select {
				case e.Reply <- e.Result:
				default:
				}
			}
		case effStartChild:
			r.handleStartChild(ctx, e)
		case effStopChild:
			r.handleStopChild(ctx, e)
		case effRestoreChildren:
			r.handleRestore(ctx, e)
		case effShutdownChildren:
			r.handleShutdown(ctx, e)
		}
	}
}

func (r *spawnActorRuntime) Stop() {}

func (r *spawnActorRuntime) registryPath() string {
	return filepath.Join(r.cfg.DelightHome, "spawned.sessions.json")
}

func (r *spawnActorRuntime) loadRegistry() (*spawnRegistry, error) {
	registry := &spawnRegistry{Owners: make(map[string]map[string]spawnEntry)}
	data, err := os.ReadFile(r.registryPath())
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

func (r *spawnActorRuntime) saveRegistry(registry *spawnRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.registryPath(), data, 0600)
}

func (r *spawnActorRuntime) registerSpawnedSession(sessionID, directory string) {
	if r.ownerID == "" || sessionID == "" || directory == "" {
		return
	}
	registry, err := r.loadRegistry()
	if err != nil {
		if r.debug {
			logger.Debugf("Failed to load spawn registry: %v", err)
		}
		return
	}
	owner := registry.Owners[r.ownerID]
	if owner == nil {
		owner = make(map[string]spawnEntry)
		registry.Owners[r.ownerID] = owner
	}
	owner[sessionID] = spawnEntry{SessionID: sessionID, Directory: directory}
	if err := r.saveRegistry(registry); err != nil && r.debug {
		logger.Debugf("Failed to save spawn registry: %v", err)
	}
}

func (r *spawnActorRuntime) removeSpawnedSession(sessionID string) {
	if r.ownerID == "" || sessionID == "" {
		return
	}
	registry, err := r.loadRegistry()
	if err != nil {
		if r.debug {
			logger.Debugf("Failed to load spawn registry: %v", err)
		}
		return
	}
	owner := registry.Owners[r.ownerID]
	if owner == nil {
		return
	}
	delete(owner, sessionID)
	if len(owner) == 0 {
		delete(registry.Owners, r.ownerID)
	}
	if err := r.saveRegistry(registry); err != nil && r.debug {
		logger.Debugf("Failed to save spawn registry: %v", err)
	}
}

func (r *spawnActorRuntime) handleStartChild(ctx context.Context, eff effStartChild) {
	if ctx.Err() != nil {
		r.emit(evChildStartFailed{ReqID: eff.ReqID, Err: ctx.Err()})
		return
	}
	if eff.Directory == "" {
		r.emit(evChildStartFailed{ReqID: eff.ReqID, Err: fmt.Errorf("directory is required")})
		return
	}
	sessionID, child, err := r.startChild(ctx, r.cfg, r.tokenForRequest(), r.debug, eff.Directory, eff.Agent)
	if err != nil {
		r.emit(evChildStartFailed{ReqID: eff.ReqID, Err: err})
		return
	}

	r.childrenMu.Lock()
	if r.children == nil {
		r.children = make(map[string]spawnedChild)
	}
	r.children[sessionID] = child
	r.childrenMu.Unlock()

	r.registerSpawnedSession(sessionID, eff.Directory)
	r.emit(evChildStarted{ReqID: eff.ReqID, SessionID: sessionID, Directory: eff.Directory})

	go func(sessionID string, child spawnedChild) {
		_ = child.Wait()
		r.childrenMu.Lock()
		delete(r.children, sessionID)
		r.childrenMu.Unlock()
	}(sessionID, child)
}

func (r *spawnActorRuntime) handleStopChild(ctx context.Context, eff effStopChild) {
	if ctx.Err() != nil {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: ctx.Err()})
		return
	}
	r.childrenMu.Lock()
	child, ok := r.children[eff.SessionID]
	if ok {
		delete(r.children, eff.SessionID)
	}
	r.childrenMu.Unlock()

	if !ok {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: fmt.Errorf("session not found")})
		return
	}

	_ = child.Close()
	r.removeSpawnedSession(eff.SessionID)
	r.emit(evChildStopped{ReqID: eff.ReqID, Err: nil})
}

func (r *spawnActorRuntime) handleRestore(ctx context.Context, eff effRestoreChildren) {
	if ctx.Err() != nil {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: ctx.Err()})
		return
	}
	if r.ownerID == "" {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: nil})
		return
	}
	registry, err := r.loadRegistry()
	if err != nil {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: err})
		return
	}
	owner := registry.Owners[r.ownerID]
	if len(owner) == 0 {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: nil})
		return
	}

	for _, entry := range owner {
		if entry.Directory == "" || entry.SessionID == "" {
			continue
		}
		if entry.SessionID == r.ownerID {
			continue
		}
		// Always start a new child; the session id can change, so we update
		// the registry if it differs from the stored id.
		sessionID, child, err := r.startChild(ctx, r.cfg, r.tokenForRequest(), r.debug, entry.Directory, "")
		if err != nil {
			continue
		}

		r.childrenMu.Lock()
		if r.children == nil {
			r.children = make(map[string]spawnedChild)
		}
		r.children[sessionID] = child
		r.childrenMu.Unlock()

		// If the session ID changed, rewrite the registry entry.
		if sessionID != entry.SessionID {
			delete(owner, entry.SessionID)
			owner[sessionID] = spawnEntry{SessionID: sessionID, Directory: entry.Directory}
		}

		go func(sessionID string, child spawnedChild) {
			_ = child.Wait()
			r.childrenMu.Lock()
			delete(r.children, sessionID)
			r.childrenMu.Unlock()
		}(sessionID, child)
	}
	// Best-effort persist (even if unchanged).
	_ = r.saveRegistry(registry)
	r.emit(evChildStopped{ReqID: eff.ReqID, Err: nil})
}

func (r *spawnActorRuntime) handleShutdown(ctx context.Context, eff effShutdownChildren) {
	if ctx.Err() != nil {
		r.emit(evChildStopped{ReqID: eff.ReqID, Err: ctx.Err()})
		return
	}
	r.childrenMu.Lock()
	children := make([]spawnedChild, 0, len(r.children))
	for _, child := range r.children {
		children = append(children, child)
	}
	r.children = make(map[string]spawnedChild)
	r.childrenMu.Unlock()

	for _, child := range children {
		_ = child.Close()
	}
	r.emit(evChildStopped{ReqID: eff.ReqID, Err: nil})
}

func (m *Manager) initSpawnActor() {
	if m.cfg == nil || m.sessionID == "" {
		return
	}
	if m.spawnActor != nil {
		return
	}
	rt := &spawnActorRuntime{
		cfg:     m.cfg,
		token:   m.token,
		debug:   m.debug,
		ownerID: m.sessionID,
	}
	m.spawnActorRuntime = rt
	m.spawnActor = framework.New(
		spawnActorState{OwnerSessionID: m.sessionID},
		func(state spawnActorState, input framework.Input) (spawnActorState, []framework.Effect) {
			return reduceSpawnActor(state, input)
		},
		rt,
	)
	m.spawnActor.Start()
}

func (m *Manager) restoreSpawnedSessions() error {
	if m.spawnActor == nil {
		return nil
	}
	reply := make(chan spawnResult, 1)
	if !m.spawnActor.Enqueue(cmdRestoreChildren{Reply: reply}) {
		return fmt.Errorf("failed to schedule restore")
	}
	select {
	case <-m.stopCh:
		return fmt.Errorf("session closed")
	case res := <-reply:
		return res.Err
	}
}

func (m *Manager) shutdownSpawnedSessions() {
	if m.spawnActor == nil {
		return
	}
	reply := make(chan spawnResult, 1)
	_ = m.spawnActor.Enqueue(cmdShutdownChildren{Reply: reply})
	select {
	case <-reply:
	case <-m.stopCh:
	}
	m.spawnActor.Stop()
	m.spawnActor = nil
	m.spawnActorRuntime = nil
}
