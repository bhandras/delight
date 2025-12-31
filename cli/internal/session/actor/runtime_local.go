package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/claude"
)

const (
	// localSessionFileWaitTimeout is the maximum time we wait for the Claude
	// session file to be created after the process emits a session ID.
	localSessionFileWaitTimeout = 5 * time.Second
)

// startLocal starts the interactive Claude process and emits runner lifecycle events.
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

// stopLocal kills the interactive Claude process for the matching generation.
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

// localSendLine writes a line of user input into the interactive Claude process.
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

// watchLocalSession watches for local session file creation and streams scanner
// messages as evOutboundMessageReady events.
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
			if !claude.WaitForSessionFile(workDir, sessionID, localSessionFileWaitTimeout) {
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

// extractClaudeUserText best-effort extracts text content from a Claude "user"
// message payload.
func extractClaudeUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	// walk recursively traverses the mixed map/slice shapes emitted by Claude's
	// local session scanner and returns the first string-like payload it finds.
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

