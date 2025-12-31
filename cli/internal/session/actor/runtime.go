package actor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/websocket"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/protocol/wire"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Runtime interprets SessionActor effects for the Delight CLI.
//
// IMPORTANT: Runtime must never mutate SessionActor state directly. It only
// emits events back into the actor mailbox via the provided emit function.
type Runtime struct {
	mu sync.Mutex

	sessionID string
	workDir   string
	debug     bool

	stateUpdater  StateUpdater
	socketEmitter SocketEmitter

	encryptFn func([]byte) (string, error)

	timers map[string]*time.Timer

	localProc    *claude.Process
	localGen     int64
	localScanner *claude.Scanner
	remoteBridge *claude.RemoteBridge
	remoteGen    int64

	takebackCancel chan struct{}
	takebackDone   chan struct{}
	takebackTTY    *os.File
	takebackState  *term.State
}

// StateUpdater persists session agent state to a server.
type StateUpdater interface {
	UpdateState(sessionID string, agentState string, version int64) (int64, error)
}

func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// SocketEmitter emits session-scoped messages + ephemerals to the server.
type SocketEmitter interface {
	EmitEphemeral(data any) error
	EmitMessage(data any) error
	EmitRaw(event string, data any) error
}

// NewRuntime returns a Runtime that executes runner effects in the given workDir.
func NewRuntime(workDir string, debug bool) *Runtime {
	return &Runtime{workDir: workDir, debug: debug}
}

// WithSessionID configures the session id used for state persistence effects.
func (r *Runtime) WithSessionID(sessionID string) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = sessionID
	return r
}

// WithStateUpdater configures the persistence adapter used for agent state updates.
func (r *Runtime) WithStateUpdater(updater StateUpdater) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Beware: an interface can be non-nil while holding a nil concrete pointer.
	// This can happen when Manager wires the runtime before websocket client
	// construction; persist effects must treat that as "no updater".
	if isNilInterface(updater) {
		r.stateUpdater = nil
	} else {
		r.stateUpdater = updater
	}
	return r
}

// WithSocketEmitter configures the socket emitter used for emit-message and
// emit-ephemeral effects.
func (r *Runtime) WithSocketEmitter(emitter SocketEmitter) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	if isNilInterface(emitter) {
		r.socketEmitter = nil
	} else {
		r.socketEmitter = emitter
	}
	return r
}

// WithEncryptFn configures the encryption function used for outbound session
// messages (plaintext JSON -> ciphertext string).
func (r *Runtime) WithEncryptFn(fn func([]byte) (string, error)) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.encryptFn = fn
	return r
}

// HandleEffects implements actor.Runtime.
func (r *Runtime) HandleEffects(ctx context.Context, effects []framework.Effect, emit func(framework.Input)) {
	for _, eff := range effects {
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch e := eff.(type) {
		case effStartLocalRunner:
			r.startLocal(ctx, e, emit)
		case effStopLocalRunner:
			r.stopLocal(e)
		case effStartRemoteRunner:
			r.startRemote(ctx, e, emit)
		case effStopRemoteRunner:
			r.stopRemote(e)
		case effLocalSendLine:
			r.localSendLine(e)
		case effRemoteSend:
			r.remoteSend(e)
		case effRemoteAbort:
			r.remoteAbort(e)
		case effRemotePermissionDecision:
			r.remotePermissionDecision(e)
		case effPersistAgentState:
			r.persistAgentState(ctx, e, emit)
		case effStartDesktopTakebackWatcher:
			r.startDesktopTakebackWatcher(ctx, emit)
		case effStopDesktopTakebackWatcher:
			r.stopDesktopTakebackWatcher()
		case effStartTimer:
			r.startTimer(ctx, e, emit)
		case effCancelTimer:
			r.cancelTimer(e)
		case effEmitEphemeral:
			r.emitEphemeral(e)
		case effEmitMessage:
			r.emitMessage(e)
		default:
			// Unknown effect: ignore.
		}
	}
}

// Stop implements actor.Runtime.
func (r *Runtime) Stop() {
	r.mu.Lock()
	cancel, done, tty, state := r.stopDesktopTakebackWatcherLocked()
	for _, timer := range r.timers {
		timer.Stop()
	}
	r.timers = nil
	if r.remoteBridge != nil {
		r.remoteBridge.Kill()
		r.remoteBridge = nil
	}
	if r.localProc != nil {
		r.localProc.Kill()
		r.localProc = nil
	}
	r.mu.Unlock()

	if cancel != nil {
		func() {
			defer func() { recover() }()
			close(cancel)
		}()
	}
	if state != nil && tty != nil {
		_ = term.Restore(int(tty.Fd()), state)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *Runtime) startDesktopTakebackWatcher(ctx context.Context, emit func(framework.Input)) {
	r.mu.Lock()
	if r.takebackCancel != nil {
		r.mu.Unlock()
		return
	}
	cancel := make(chan struct{})
	done := make(chan struct{})
	r.takebackCancel = cancel
	r.takebackDone = done
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		r.takebackCancel = nil
		r.takebackDone = nil
		r.mu.Unlock()
		return
	}
	r.takebackTTY = tty
	r.mu.Unlock()

	fd := int(tty.Fd())
	if !term.IsTerminal(fd) {
		_ = tty.Close()
		r.stopDesktopTakebackWatcher()
		return
	}

	go func() {
		defer close(done)

		restored := false
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			_ = tty.Close()
			r.mu.Lock()
			if r.takebackDone == done {
				r.takebackCancel = nil
				r.takebackDone = nil
				r.takebackTTY = nil
				r.takebackState = nil
			}
			r.mu.Unlock()
			return
		}
		r.mu.Lock()
		if r.takebackDone == done {
			r.takebackState = oldState
		}
		r.mu.Unlock()

		defer func() {
			if !restored {
				_ = term.Restore(fd, oldState)
			}
			_ = tty.Close()
			r.mu.Lock()
			if r.takebackDone == done {
				r.takebackCancel = nil
				r.takebackDone = nil
				r.takebackTTY = nil
				r.takebackState = nil
			}
			r.mu.Unlock()
		}()

		buf := make([]byte, 16)
		var pendingSpace bool
		var pendingSpaceAt time.Time
		const confirmWindow = 15 * time.Second

		for {
			select {
			case <-ctx.Done():
				return
			case <-cancel:
				return
			default:
			}

			pollRes, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 50)
			if err != nil {
				if err == unix.EINTR {
					continue
				}
				return
			}
			if pollRes == 0 {
				continue
			}

			n, err := tty.Read(buf)
			if err != nil || n <= 0 {
				return
			}

			now := time.Now()
			if pendingSpace && now.Sub(pendingSpaceAt) > confirmWindow {
				pendingSpace = false
			}

			shouldSwitch := false
			for _, b := range buf[:n] {
				if b == ' ' {
					if pendingSpace {
						shouldSwitch = true
						break
					}
					pendingSpace = true
					pendingSpaceAt = now
					fmt.Fprintln(os.Stdout, "Press space again to take back control on desktop.")
					continue
				}

				if pendingSpace {
					pendingSpace = false
				}
			}
			if !shouldSwitch {
				continue
			}

			_ = term.Restore(fd, oldState)
			restored = true
			r.mu.Lock()
			if r.takebackState == oldState {
				r.takebackState = nil
			}
			r.mu.Unlock()

			emit(evDesktopTakeback{})
			return
		}
	}()
}

func (r *Runtime) stopDesktopTakebackWatcher() {
	r.mu.Lock()
	cancel, done, tty, state := r.stopDesktopTakebackWatcherLocked()
	r.mu.Unlock()

	if cancel != nil {
		func() {
			defer func() { recover() }()
			close(cancel)
		}()
	}

	if state != nil && tty != nil {
		_ = term.Restore(int(tty.Fd()), state)
	}

	if done != nil {
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *Runtime) stopDesktopTakebackWatcherLocked() (chan struct{}, chan struct{}, *os.File, *term.State) {
	cancel := r.takebackCancel
	done := r.takebackDone
	tty := r.takebackTTY
	state := r.takebackState
	r.takebackCancel = nil
	r.takebackDone = nil
	r.takebackTTY = nil
	r.takebackState = nil
	return cancel, done, tty, state
}

func (r *Runtime) startLocal(ctx context.Context, eff effStartLocalRunner, emit func(framework.Input)) {
	workDir := eff.WorkDir
	if workDir == "" {
		workDir = r.workDir
	}
	if workDir == "" {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: fmt.Errorf("missing workDir")})
		return
	}

	proc, err := claude.NewProcess(workDir, r.debug)
	if err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}
	if err := proc.Start(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeLocal, Err: err})
		return
	}

	r.mu.Lock()
	// Replace any existing local proc.
	if r.localProc != nil {
		r.localProc.Kill()
	}
	r.localProc = proc
	r.localGen = eff.Gen
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeLocal})

	go func(gen int64, p *claude.Process) {
		err := p.Wait()
		emit(evRunnerExited{Gen: gen, Mode: ModeLocal, Err: err})
	}(eff.Gen, proc)

	go r.watchLocalSession(ctx, eff.Gen, workDir, proc, emit)
}

func (r *Runtime) stopLocal(eff effStopLocalRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.localProc == nil {
		return
	}
	// Best-effort: only stop if it matches the currently tracked generation.
	if r.localGen != 0 && eff.Gen != 0 && eff.Gen != r.localGen {
		return
	}
	r.localProc.Kill()
	r.localProc = nil
	r.localGen = 0
	if r.localScanner != nil {
		r.localScanner.Stop()
		r.localScanner = nil
	}
}

func (r *Runtime) startRemote(ctx context.Context, eff effStartRemoteRunner, emit func(framework.Input)) {
	workDir := eff.WorkDir
	if workDir == "" {
		workDir = r.workDir
	}
	bridge, err := claude.NewRemoteBridge(workDir, eff.Resume, r.debug)
	if err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	bridge.SetMessageHandler(func(msg *claude.RemoteMessage) error {
		if msg == nil {
			return nil
		}
		// Detach from bridge-owned buffers.
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		var copyMsg claude.RemoteMessage
		if err := json.Unmarshal(raw, &copyMsg); err != nil {
			return err
		}

		nowMs := time.Now().UnixMilli()

		if copyMsg.Type == "control_request" {
			var req claude.PermissionRequest
			if len(copyMsg.Request) > 0 && json.Unmarshal(copyMsg.Request, &req) == nil && req.ToolName != "" {
				emit(evPermissionRequested{
					RequestID: copyMsg.RequestID,
					ToolName:  req.ToolName,
					Input:     req.Input,
					NowMs:     nowMs,
				})
			} else {
				emit(evPermissionRequested{
					RequestID: copyMsg.RequestID,
					ToolName:  "unknown",
					Input:     json.RawMessage("null"),
					NowMs:     nowMs,
				})
			}
			return nil
		}

		plaintext, ok := buildRawRecordBytesFromRemote(&copyMsg)
		if !ok {
			return nil
		}

		r.mu.Lock()
		encryptFn := r.encryptFn
		r.mu.Unlock()
		if encryptFn == nil {
			return nil
		}
		ciphertext, err := encryptFn(plaintext)
		if err != nil {
			return nil
		}
		emit(evOutboundMessageReady{
			Gen:        eff.Gen,
			LocalID:    "",
			Ciphertext: ciphertext,
			NowMs:      nowMs,
		})
		return nil
	})

	if err := bridge.Start(); err != nil {
		emit(evRunnerExited{Gen: eff.Gen, Mode: ModeRemote, Err: err})
		return
	}

	r.mu.Lock()
	if r.remoteBridge != nil {
		r.remoteBridge.Kill()
	}
	r.remoteBridge = bridge
	r.remoteGen = eff.Gen
	r.mu.Unlock()

	emit(evRunnerReady{Gen: eff.Gen, Mode: ModeRemote})

	go func(gen int64, b *claude.RemoteBridge) {
		err := b.Wait()
		emit(evRunnerExited{Gen: gen, Mode: ModeRemote, Err: err})
	}(eff.Gen, bridge)
}

func (r *Runtime) stopRemote(eff effStopRemoteRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.remoteBridge == nil {
		return
	}
	if r.remoteGen != 0 && eff.Gen != 0 && eff.Gen != r.remoteGen {
		return
	}
	r.remoteBridge.Kill()
	r.remoteBridge = nil
	r.remoteGen = 0
}

func (r *Runtime) remoteSend(eff effRemoteSend) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	_ = bridge.SendUserMessage(eff.Text, eff.Meta)
}

func (r *Runtime) remoteAbort(eff effRemoteAbort) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	_ = bridge.Abort()
}

func (r *Runtime) localSendLine(eff effLocalSendLine) {
	r.mu.Lock()
	proc := r.localProc
	gen := r.localGen
	r.mu.Unlock()
	if proc == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	_ = proc.SendLine(eff.Text)
}

func (r *Runtime) remotePermissionDecision(eff effRemotePermissionDecision) {
	r.mu.Lock()
	bridge := r.remoteBridge
	gen := r.remoteGen
	r.mu.Unlock()
	if bridge == nil {
		return
	}
	if gen != 0 && eff.Gen != 0 && eff.Gen != gen {
		return
	}
	behavior := "deny"
	if eff.Allow {
		behavior = "allow"
	}
	resp := &claude.PermissionResponse{
		Behavior: behavior,
		Message:  eff.Message,
	}
	if eff.Allow && len(eff.UpdatedInput) > 0 {
		resp.UpdatedInput = eff.UpdatedInput
	}
	_ = bridge.SendPermissionResponse(eff.RequestID, resp)
}

func (r *Runtime) watchLocalSession(ctx context.Context, gen int64, workDir string, proc *claude.Process, emit func(framework.Input)) {
	if proc == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case sessionID, ok := <-proc.SessionID():
			if !ok || sessionID == "" {
				return
			}
			if !claude.WaitForSessionFile(workDir, sessionID, 5*time.Second) {
				continue
			}

			emit(evClaudeSessionDetected{Gen: gen, SessionID: sessionID})

			// Stop any previous scanner before starting a new one.
			scanner := claude.NewScanner(workDir, sessionID, r.debug)
			scanner.Start()

			r.mu.Lock()
			if r.localGen != gen || r.localProc != proc {
				r.mu.Unlock()
				scanner.Stop()
				return
			}
			if r.localScanner != nil {
				r.localScanner.Stop()
			}
			r.localScanner = scanner
			encryptFn := r.encryptFn
			r.mu.Unlock()

			if encryptFn == nil {
				return
			}

			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case msg, ok := <-scanner.Messages():
						if !ok || msg == nil {
							return
						}

						// Deep-copy the message to detach from scanner buffers.
						raw, err := json.Marshal(msg)
						if err != nil {
							continue
						}
						var copyMsg claude.SessionMessage
						if err := json.Unmarshal(raw, &copyMsg); err != nil {
							continue
						}

						nowMs := time.Now().UnixMilli()
						plaintext, err := json.Marshal(copyMsg)
						if err != nil {
							continue
						}
						ciphertext, err := encryptFn(plaintext)
						if err != nil {
							continue
						}

						userText := ""
						if copyMsg.Type == "user" {
							userText = extractClaudeUserText(copyMsg.Message)
						}

						emit(evOutboundMessageReady{
							Gen:                gen,
							LocalID:            copyMsg.UUID,
							Ciphertext:         ciphertext,
							NowMs:              nowMs,
							UserTextNormalized: userText,
						})
					}
				}
			}()

			return
		case thinking, ok := <-proc.Thinking():
			if !ok {
				return
			}
			_ = thinking
			// Phase 3/4: thinking is still Manager-owned. Once the actor owns the
			// keep-alive loop, we can emit thinking change events here.
		}
	}
}

func extractClaudeUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	var walk func(any) string
	walk = func(v any) string {
		switch t := v.(type) {
		case string:
			return t
		case map[string]any:
			if content, ok := t["content"]; ok {
				if s := walk(content); s != "" {
					return s
				}
			}
			if text, ok := t["text"]; ok {
				if s := walk(text); s != "" {
					return s
				}
			}
			if message, ok := t["message"]; ok {
				if s := walk(message); s != "" {
					return s
				}
			}
			if data, ok := t["data"]; ok {
				if s := walk(data); s != "" {
					return s
				}
			}
			return ""
		case []any:
			for _, part := range t {
				if s := walk(part); s != "" {
					return s
				}
			}
			return ""
		default:
			return ""
		}
	}

	return normalizeRemoteInputText(walk(value))
}

func buildRawRecordBytesFromRemote(msg *claude.RemoteMessage) ([]byte, bool) {
	if msg == nil {
		return nil, false
	}

	// Prefer already-structured payloads.
	if len(msg.Message) > 0 && msg.Type != "raw" {
		raw := json.RawMessage(msg.Message)
		// If it already looks like a wire record, forward as-is.
		var probe struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && probe.Role != "" && probe.Content.Type != "" {
			return []byte(raw), true
		}
	}

	switch msg.Type {
	case "raw":
		if len(msg.Message) == 0 {
			return nil, false
		}
		return []byte(msg.Message), true
	case "message":
		role := msg.Role
		contentBlocks, err := wire.DecodeContentBlocks(msg.Content)
		if err != nil {
			return nil, false
		}
		if role == "" || len(contentBlocks) == 0 {
			return nil, false
		}

		model := msg.Model
		if model == "" {
			model = "unknown"
		}
		outType := role
		uuid := types.NewCUID()

		rec := wire.AgentOutputRecord{
			Role: "agent",
			Content: wire.AgentOutputContent{
				Type: "output",
				Data: wire.AgentOutputData{
					Type:             outType,
					IsSidechain:      false,
					IsCompactSummary: false,
					IsMeta:           false,
					UUID:             uuid,
					ParentUUID:       nil,
					Message: wire.AgentMessage{
						Role:    role,
						Model:   model,
						Content: contentBlocks,
					},
				},
			},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, false
		}
		return data, true
	case "assistant":
		text := ""
		switch v := msg.Content.(type) {
		case string:
			text = v
		default:
			blocks, err := wire.DecodeContentBlocks(v)
			if err == nil {
				for _, block := range blocks {
					if block.Type == "text" && block.Text != "" {
						text = block.Text
						break
					}
				}
			}
		}
		if text == "" && msg.Result != "" {
			text = msg.Result
		}
		if text == "" {
			return nil, false
		}
		model := msg.Model
		if model == "" {
			model = "unknown"
		}
		rec := wire.AgentOutputRecord{
			Role: "agent",
			Content: wire.AgentOutputContent{
				Type: "output",
				Data: wire.AgentOutputData{
					Type:             "assistant",
					IsSidechain:      false,
					IsCompactSummary: false,
					IsMeta:           false,
					UUID:             types.NewCUID(),
					ParentUUID:       nil,
					Message: wire.AgentMessage{
						Role:  "assistant",
						Model: model,
						Content: []wire.ContentBlock{
							{Type: "text", Text: text},
						},
					},
				},
			},
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return nil, false
		}
		return data, true
	default:
		return nil, false
	}
}

func (r *Runtime) persistAgentState(ctx context.Context, eff effPersistAgentState, emit func(framework.Input)) {
	r.mu.Lock()
	updater := r.stateUpdater
	sessionID := r.sessionID
	r.mu.Unlock()

	if updater == nil || sessionID == "" {
		// Not configured yet; treat as no-op.
		return
	}

	// Persist asynchronously to avoid blocking the actor loop on socket IO.
	go func() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		newVersion, err := updater.UpdateState(sessionID, eff.AgentStateJSON, eff.ExpectedVersion)
		if err == nil {
			emit(EvAgentStatePersisted{NewVersion: newVersion})
			return
		}

		// Special case: version mismatch should be retried by the reducer with the server version.
		if errors.Is(err, websocket.ErrVersionMismatch) {
			if newVersion <= 0 {
				// If the server didn't provide a version, treat as a generic failure.
				emit(EvAgentStatePersistFailed{Err: err})
				return
			}
			emit(EvAgentStateVersionMismatch{ServerVersion: newVersion})
			return
		}
		emit(EvAgentStatePersistFailed{Err: err})
	}()
}

func (r *Runtime) startTimer(ctx context.Context, eff effStartTimer, emit func(framework.Input)) {
	if eff.Name == "" || eff.AfterMs <= 0 {
		return
	}

	r.mu.Lock()
	if r.timers == nil {
		r.timers = make(map[string]*time.Timer)
	}
	if prev := r.timers[eff.Name]; prev != nil {
		prev.Stop()
	}
	after := time.Duration(eff.AfterMs) * time.Millisecond
	r.timers[eff.Name] = time.AfterFunc(after, func() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		emit(evTimerFired{Name: eff.Name, NowMs: time.Now().UnixMilli()})
	})
	r.mu.Unlock()
}

func (r *Runtime) cancelTimer(eff effCancelTimer) {
	if eff.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timers == nil {
		return
	}
	if t := r.timers[eff.Name]; t != nil {
		t.Stop()
	}
	delete(r.timers, eff.Name)
}

func (r *Runtime) emitEphemeral(eff effEmitEphemeral) {
	r.mu.Lock()
	emitter := r.socketEmitter
	r.mu.Unlock()
	if emitter == nil {
		return
	}
	_ = emitter.EmitEphemeral(eff.Payload)
}

func (r *Runtime) emitMessage(eff effEmitMessage) {
	r.mu.Lock()
	emitter := r.socketEmitter
	sessionID := r.sessionID
	r.mu.Unlock()
	if emitter == nil {
		return
	}
	if sessionID == "" || eff.Ciphertext == "" {
		return
	}
	_ = emitter.EmitMessage(wire.OutboundMessagePayload{
		SID:     sessionID,
		LocalID: eff.LocalID,
		Message: eff.Ciphertext,
	})
}
