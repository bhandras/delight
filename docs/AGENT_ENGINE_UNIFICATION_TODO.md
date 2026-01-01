# Agent Engine Unification — TODO

This checklist tracks the work described in `docs/AGENT_ENGINE_UNIFICATION.md`.

Conventions:

- Reducers are pure; runtimes execute effects and emit events.
- All new Go tests use `testify/require`.
- All new files and non-trivial functions require doc comments.
- No magic constants; introduce named constants for timeouts, buffer sizes, and event discriminants.

---

## 0) Preflight

- [ ] Re-read `docs/AGENT_ENGINE_UNIFICATION.md` and confirm the scope (Claude + Codex first).
- [ ] Confirm Codex "remote bootstrap" UX: start MCP first, then optionally switch to local.

---

## 1) Engine interface + common events (Go)

- [ ] Add `cli/internal/agentengine` (name TBD) with:
  - [ ] `AgentEngine` interface
  - [ ] `EngineStartSpec` and `UserMessage` types
  - [ ] Common `AgentEvent` types (ready/exited/thinking/output/tool/permission/ids)
  - [ ] Small helpers for content blocks (`Text`, `Reasoning`, `ToolCall`, `ToolResult`)

---

## 2) SessionActor: engine-generic effects

- [ ] Replace Claude-specific runner effects with:
  - [ ] `effEngineStart`
  - [ ] `effEngineStop`
  - [ ] `effEngineSend`
  - [ ] `effEngineAbort`
- [ ] Add actor inputs for engine events:
  - [ ] `evEngineReady`, `evEngineExited`
  - [ ] `evEngineThinking`
  - [ ] `evEngineOutput` (and tool call/result variants)
  - [ ] `evEnginePermissionRequested`
  - [ ] `evEngineSessionIdentified`, `evEngineRolloutPath`
- [ ] Ensure reducer does not branch on agent implementation details.

---

## 3) Runtime: engine dispatcher

- [ ] Add a runtime adapter that:
  - [ ] creates the correct engine for the configured `agent` string
  - [ ] executes `effEngine*` effects by calling engine methods
  - [ ] translates engine `AgentEvent`s into actor mailbox events
  - [ ] enforces generation ID correctness

---

## 4) ClaudeEngine adapter

- [ ] Implement `ClaudeEngine`:
  - [ ] Remote: wrap current stream-json bridge and map events → `AgentEvent`
  - [ ] Local: wrap PTY runner start/stop, emit ready/exited; session-id detection event if possible
  - [ ] Permissions: emit `AgentPermissionRequest` only for remote mode

---

## 5) CodexEngine adapter (remote MCP)

- [ ] Implement `CodexEngine` remote mode:
  - [ ] Wrap existing MCP client (`cli/internal/codex`).
  - [ ] Map `codex/event` notifications → `AgentEvent`s.
  - [ ] Capture and emit:
    - [ ] `EvEngineSessionIdentified` (Codex session id / resume token)
    - [ ] `EvEngineRolloutPath` (rollout JSONL path)

---

## 6) CodexEngine adapter (local TUI + rollout tail)

- [ ] Implement `CodexEngine` local mode:
  - [ ] Spawn PTY: `codex resume <sessionId>`
  - [ ] Start rollout tailer from stored rolloutPath
  - [ ] Tail JSONL and map events to `AgentEvent`s
  - [ ] Ensure phone does not show permission modals in local mode
- [ ] Fallback: if rolloutPath missing, discover the active rollout file via `~/.codex/sessions/**/rollout-*.jsonl`.

---

## 7) Remove remaining wired logic

- [ ] Move "thinking" state out of `Manager` into SessionActor-owned state + effects.
- [ ] Replace direct `wsClient.EmitMessage/EmitEphemeral` calls in agent codepaths with actor effects.
- [ ] Delete or slim:
  - [ ] `cli/internal/session/manager_agent_codex.go` queue loop
  - [ ] any agent-specific goroutine that mutates shared state

---

## 8) Tests

### 8.1 Reducer tests (pure)

- [ ] Switching emits correct `effEngine*` effect sequences.
- [ ] Inbound phone message in local mode triggers handoff to remote and queues message.
- [ ] `evEngineReady` flushes queued messages in remote mode.
- [ ] Permission request routing depends on `ControlledByUser`.
- [ ] Stale generation events are ignored.

### 8.2 Adapter/runtime tests

- [ ] Codex rolloutPath capture integration test (already added; keep).
- [ ] Rollout tailer unit test: appending JSONL lines produces expected `AgentEvent`s.

---

## 9) Documentation

- [ ] Update `docs/SESSION_MANAGER_ACTOR_FSM_TODO.md` with a link to this plan if needed.
- [ ] Add a short "agent engine architecture" section to the iOS docs (view-only; no business logic).

