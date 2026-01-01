# Agent Engine Unification (SessionActor-Driven)

This document describes how Delight should unify Claude, Codex, and future agents
behind a single engine interface, while keeping the session control logic in
`SessionActor` (actor/FSM) as the source of truth.

## Goals

- **One session controller**: all control/mode semantics live in the CLI (not iOS).
- **UI-agnostic**: iOS/web/CLI views are pure renderers of SDK state + event stream.
- **No TUI scraping**: prefer structured streams (Claude stream-json, Codex MCP, Codex
  rollout JSONL) to reproduce transcript/tool/reasoning events reliably.
- **Deterministic + testable**: reducers remain pure and unit-testable with testify.
- **Incremental migration**: refactor without breaking existing working flows.

## Non-goals

- Unify the *UX* or *rendering* of Claude and Codex TUIs.
- Replace the Codex or Claude native TUIs.
- Perfectly model every upstream event; we start with a minimal common event set.

## Glossary

- **SessionActor**: the actor/FSM owning all session state and emitting declarative effects.
- **Engine**: the agent-specific process/protocol implementation (Claude remote, Codex MCP, etc.).
- **Local mode**: "desktop controls" (phone is viewer; approvals handled locally).
- **Remote mode**: "phone controls" (phone can send; approvals routed to phone).
- **Rollout JSONL**: Codex append-only event log, e.g. `~/.codex/sessions/.../rollout-*.jsonl`.

## Current state (why this doc exists)

Claude local/remote is already controlled by `SessionActor`, but:

- Codex/ACP/fake-agent logic still lives in `Manager` codepaths with direct websocket
  emissions and internal queues.
- "Thinking" activity and keepalive behavior is still partly Manager-owned.
- We want a single, testable control plane in the CLI for all agents.

## Proposed architecture

### Layering

1) **SessionActor (policy + orchestration)**

- Owns:
  - control mode (`ControlledByUser`)
  - runner lifecycle and generations
  - durable `agentState` persistence and version retries
  - permission requests (durable `agentState.requests`) and decisions
  - offline/online state and UI state derivation
  - debounces/timers

2) **Engine Runtime (agent-specific side effects)**

- Implements "start/stop/send/abort".
- Translates agent-specific process/protocol events into a common `AgentEvent` stream.
- Never mutates SessionActor state directly; it emits typed events back to the actor.

### Engine interface

The SessionActor runtime depends on a single interface, implemented by each agent adapter.

```go
type AgentEngine interface {
    Start(ctx context.Context, spec EngineStartSpec) error
    Stop(ctx context.Context) error
    SendUserMessage(ctx context.Context, msg UserMessage) error
    Abort(ctx context.Context) error
    Events() <-chan AgentEvent
    Wait() error
}
```

Key points:

- The engine interface is **protocol-agnostic**: no PTY/MCP/stdio references.
- The engine emits a single typed `AgentEvent` stream, which the runtime converts to actor inputs.
- `Start` includes the target **mode** (local/remote) and optional **resume token**.

### EngineStartSpec

```go
type EngineStartSpec struct {
    AgentType   string // "claude" | "codex" | "acp" | ...
    WorkDir     string
    Mode        string // "local" | "remote"
    ResumeToken string // agent-specific session id
    Config      map[string]any // model, permission mode, etc.
}
```

### UserMessage

```go
type UserMessage struct {
    Text    string
    Meta    map[string]any
    LocalID string
    AtMs    int64
}
```

### Common event model

We define a minimal common set of events sufficient for:

- transcript display (user/assistant)
- tool calls + results
- reasoning/thinking
- permissions prompts in remote mode
- runner lifecycle, ids, and rollout path

```go
type AgentEvent interface{ isAgentEvent() }

type EvEngineReady struct{ Gen int64 }
type EvEngineExited struct{ Gen int64; Err error }
type EvEngineThinking struct{ Gen int64; Thinking bool; AtMs int64 }

type EvEngineOutput struct {
    Gen       int64
    Role      string // user|assistant|tool|system
    Blocks    []ContentBlock
    UUID      string
    ParentUUID *string
    AtMs      int64
}

type EvEngineToolCall struct{ ... }
type EvEngineToolResult struct{ ... }

type EvEnginePermissionRequest struct {
    Gen       int64
    RequestID string
    ToolName  string
    InputJSON json.RawMessage
    AtMs      int64
}

type EvEngineSessionIdentified struct {
    Gen        int64
    ResumeToken string
}

type EvEngineRolloutPath struct {
    Gen  int64
    Path string
}
```

We intentionally keep this small and grow it only when needed for UX parity.

## Codex local/remote semantics

Codex supports both:

- **Remote mode**: `codex mcp-server` controlled via MCP/JSON-RPC, streaming `codex/event`.
- **Local mode**: native Codex TUI via `codex resume <SESSION_ID>`.

We will **not** scrape TUI output. Instead, we stream by tailing the rollout JSONL.

### Bootstrap rule (avoid rollout discovery gaps)

To avoid "phone connects while local is already running" discrepancies:

- Always start Codex sessions by establishing a remote/MCP session first.
  - Capture `sessionId` (resume token) and `rolloutPath`.
  - Persist them into CLI-owned session state.
- Switching to local uses `codex resume <sessionId>` and tails `rolloutPath`.

If rolloutPath is missing (rare), fallback discovery scans `~/.codex/sessions/**/rollout-*.jsonl`
and selects the most recently modified candidate.

## Claude local/remote semantics

Claude keeps the current behavior:

- Local mode: PTY-based interactive TUI.
- Remote mode: stream-json + permission-prompt-tool stdio.

Unlike Codex, Claude cannot reliably expose a structured transcript stream from local TUI,
so local mode is "desktop-only approvals" and phone is a viewer of server transcript.

## SessionActor changes (effects and inputs)

### Generic effects

Replace agent-specific start/stop effects with generic ones:

- `effEngineStart{Gen, Mode, ResumeToken, WorkDir, AgentType, Config}`
- `effEngineStop{Gen}`
- `effEngineSend{Gen, UserMessage}`
- `effEngineAbort{Gen}`

Keep:

- `effPersistAgentState`
- `effEmitEphemeral`
- `effEmitMessage`
- timer effects

### Generic inputs

Engine runtime converts engine events into actor events:

- `evEngineReady{Gen}`
- `evEngineExited{Gen, Err}`
- `evEngineThinking{...}`
- `evEnginePermissionRequested{...}`
- `evEngineOutput{...}`
- `evEngineSessionIdentified{ResumeToken}`
- `evEngineRolloutPath{Path}`

The reducer remains policy-focused.

## Migration plan

Phase 1: Engine interface + adapter wrappers

- Introduce `AgentEngine` and the common event types.
- Add `CodexEngine` wrapper around existing MCP client.
- Add `ClaudeEngine` wrapper around existing remote bridge and local PTY runner.
- Keep Manager public surface stable by forwarding to SessionActor commands.

Phase 2: SessionActor refactor

- Replace Claude-specific effects with engine-generic effects.
- Move "thinking" to SessionActor-owned state + effects (remove Manager-owned thinking).

Phase 3: Codex local mode parity

- Implement `codex resume <sessionId>` local runner in PTY.
- Implement rollout JSONL tailer that maps JSONL events to `AgentEvent`s.

Phase 4: Reduce remaining wired logic

- Convert direct websocket emits in agent managers into actor effects.
- Remove `manager_agent_codex.go` queue as source of truth; make it an adapter runtime.

## Test plan

### Reducer unit tests (testify)

- Switch local→remote emits stop/start effects and flips `ControlledByUser=false`.
- Phone input in local mode enqueues pending sends and triggers switch to remote.
- Engine ready in remote mode flushes pending sends.
- Permission request in remote mode:
  - persists `agentState.requests`
  - emits permission-request ephemeral
- Permission request in local mode:
  - does not emit phone prompt
  - still records durable state (optional) or drops per policy.
- Stale gen events ignored.

### Runtime/adapter tests (testify)

- Codex MCP integration: rolloutPath captured (gated by env var).
- Rollout tailer unit test: JSONL append produces ordered AgentEvents.
- Claude remote bridge emits permission request events deterministically (fake bridge).

## Open questions

- Should Codex local mode stream "user typed locally" events to phone (viewer), or only agent outputs?
- How do we reconcile message UUIDs between MCP events and rollout JSONL events (dedupe)?
- Should we persist rolloutPath into session agentState for UI debugging, or treat it as internal-only?

