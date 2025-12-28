# Delight Session Runtime Refactor (Design Doc)

Status: Draft (v0)  
Owner: bhandras  
Last updated: 2025-12-28  

## 1. Executive Summary

`cli/internal/session/manager.go` currently behaves as a “god object”: it mixes session identity, machine/daemon lifecycle, encryption, transport wiring (socket.io + HTTP), agent lifecycle (Claude TUI + fd3 tracking, RemoteBridge, Codex, ACP), spawned-session persistence, and RPC handling. It also runs many goroutines with partially locked state and relies on socket handlers that can execute concurrently.

This architecture is:
- fragile under concurrency (e.g. “send two messages quickly” crashes),
- hard to reason about (“what state are we in?”),
- hard to test (logic tightly coupled to sockets/process IO),
- hard to evolve safely (large file, many inline JSON structs, `map[string]any` everywhere).

This doc proposes an incremental refactor toward an explicit, single-threaded **SessionRuntime** (actor/event-loop) with clear boundaries:
- `session/runtime` (state machine + reducer),
- `transport` (socket + HTTP),
- `protocol` (typed wire parsing + typed record building),
- `crypto` (keys + codecs),
- `agent` adapters (Claude/Codex/ACP),
- `store` for persistence (spawn registry, keys),
- `sdk` serialization layer so gomobile calls are safe from any Swift thread.

We do not need a big-bang rewrite. The migration plan is staged so each phase reduces crash surface and improves readability.

## 2. Goals & Non-Goals

### 2.1 Goals

1. Deterministic concurrency model
   - All mutable session state is owned by a single goroutine (runtime loop).
   - Swift/gomobile can call concurrently; Go serializes safely.

2. Improved readability & structure
   - Replace inline JSON structs and `map[string]any` with typed protocol objects.
   - Split `manager.go` into cohesive files/modules.

3. Testability
   - Core behavior can be unit-tested without sockets/processes.
   - Add regression tests for concurrency/racey paths.

4. Stability under load
   - “Send two messages quickly” should never crash due to Go concurrency bugs.
   - Mode switches and permission flows should not create deadlocks or races.

5. Minimal disruption to product
   - Preserve JSON + socket.io for now.
   - Keep behavior compatible while refactoring.

### 2.2 Non-Goals (for this refactor)

- Replace socket.io with gRPC.
- Replace JSON with protobuf.
- Rewrite server protocol end-to-end.
- Re-implement iOS app in pure Swift.
- Achieve 100% integration test reliability immediately.

## 3. Current State: Critical Review

### 3.1 Responsibility overload

`session.Manager` currently owns at least:
- Session creation, tags, metadata encryption, state versioning.
- Machine creation/update, daemon state/version, machine RPC.
- Transport setup (two socket connections + HTTP).
- RPC request/response wiring and encryption.
- Agent orchestration for Claude/Codex/ACP.
- Spawn registry persistence + child Manager lifetimes.
- Mode switching between local/remote.
- Keep-alive scheduling and various background goroutines.

This creates wide coupling and makes local changes high-risk.

### 3.2 Concurrency model is implicit and inconsistent

Symptoms already seen:
- crashes correlated with rapid repeated user actions,
- UI desync (“no bubbles after crash”) likely due to partial state reset,
- gomobile issues where Go runtime aborts (SIGABRT) with missing callstacks.

Likely causes:
- shared fields accessed from multiple goroutines without consistent synchronization,
- socket handler callbacks executed concurrently by default,
- mixed use of locks (some maps locked, many fields not),
- reentrancy across callbacks (socket → manager → socket, etc.).

### 3.3 “Inline JSON structs” indicates missing typed boundaries

Inline request structs and pervasive `map[string]interface{}` make it easy to:
- miss fields,
- accept invalid types silently,
- create divergent representations of the same concept.

Typed protocol objects would make code and tests much simpler.

## 4. Proposed Architecture

### 4.1 Core principle: single-writer session runtime (actor model)

Introduce a **SessionRuntime** where:
- all mutable session state is owned by one goroutine,
- every external input is converted into an event and sent into the runtime inbox,
- the runtime handles events sequentially and produces commands (side effects).

This provides a single source of truth and eliminates “who is allowed to mutate state?” confusion.

### 4.2 Target module boundaries (final shape)

Suggested structure (exact naming can be tweaked):

```
cli/internal/
  session/
    runtime/
      runtime.go
      state.go
      events.go
      commands.go
      mode.go
    api/
      manager.go
    spawnstore/
      store.go
    machine/
      daemon.go
  transport/
    socketio/
      client.go
      events.go
    http/
      sessions.go
      machines.go
  protocol/
    wire/
      update.go
      rpc.go
      message.go
    records/
      rawrecord.go
  agent/
    claude/
      local.go
      remote.go
    codex/
      adapter.go
    acp/
      adapter.go
  sdk/
    sdk.go
    dispatch.go
```

Key idea: runtime contains business logic; other packages translate IO into typed events/commands.

### 4.3 Runtime: State, Events, Commands

#### 4.3.1 Runtime state (sketch)

```
type RuntimeState struct {
  cfg *config.Config
  token string

  machine MachineState
  session SessionState

  mode Mode
  agent AgentHandle

  pendingPermissions map[string]PermissionWaiter
  spawned map[string]SpawnedSessionHandle

  thinking bool
  agentState types.AgentState
  agentStateVersion int64

  crypto CryptoContext
  transport TransportHandle
}
```

#### 4.3.2 Events (sketch)

Events come from SDK entrypoints, socket updates, agent adapters, timers:

```
type EventUserMessage struct { Text string; Meta map[string]any }
type EventSocketUpdate struct { Update protocolwire.UpdateEvent }
type EventAgentThinking struct { Thinking bool }
type EventPermissionRequest struct { RequestID, ToolName string; Input json.RawMessage }
type EventPermissionResponse struct { RequestID string; Allow bool; Message string }
type EventModeSwitch struct { Mode Mode }
type EventShutdown struct{}
```

#### 4.3.3 Commands (side effects)

Reducer emits commands instead of doing IO directly:
- Send socket message / ephemeral update
- Start/stop agent
- Persist spawn registry
- HTTP create/update calls
- Keepalive tick

Commands are executed by an effects runner and results re-enter runtime as events.

## 5. Protocol Strategy: Remove `map[string]any`

### 5.1 Typed RPC requests/responses

Replace inline structs with named types:
- `SwitchModeRequest { Mode string }`
- `PermissionResponse { RequestID string; Allow bool; Message string }`
- `SpawnSessionRequest { Directory string; ApprovedNewDirectoryCreation bool; Agent string; ... }`

### 5.2 Typed wire parsing

Create `protocol/wire` that accepts both:
- legacy message events,
- structured update events (`update.body.t == "new-message"`),
and produces a canonical internal representation.

### 5.3 Typed RawRecord builder

Make one builder for the iOS RawRecord schema, instead of ad-hoc maps throughout the code.

## 6. Concurrency Model: End-to-End

All inbound sources must be serialized:
- gomobile exported SDK methods,
- socket update handlers,
- RPC request handlers,
- agent adapter callbacks,
- scanner outputs.

Outbound IO must not block the runtime loop.

## 7. Migration Plan (Incremental)

### Phase 0: Serialization guardrails (already started)
- Queue socket updates through a single inbound queue.
- Route session RPC methods through same queue.

Remaining for Phase 0:
- Route machine RPC handlers through the same serialization.
- Route all gomobile SDK entrypoints through a single Go dispatcher.

### Phase 1: SDK-level serialization (next)

Goal: Swift can call concurrently; Go serializes all SDK entrypoints.

Deliverables:
- `sdk/dispatch.go` dispatcher (single goroutine)
- All exported gomobile methods go through dispatcher.
- Tests proving concurrent calls do not race.

### Phase 2: Protocol typing

Deliverables:
- `protocol/wire` typed structs for update events and RPC requests.
- Remove most inline JSON structs.

### Phase 3: Split `manager.go` mechanically

Deliverables:
- Split into cohesive files: rpc, crypto, mode, machine, spawn, agent.

### Phase 4: Introduce `session/runtime`

Deliverables:
- Move state + reducer into runtime package.
- Manager becomes a facade that wires adapters.

### Phase 5: Command/effects runner

Deliverables:
- Runtime emits commands, effects runner executes.
- Deterministic reducer tests without sockets/processes.

## 8. Testing Strategy

- Unit tests for protocol parsing and record building.
- Runtime reducer tests (pure, deterministic).
- SDK concurrency tests.
- Run targeted tests by default; integration tests (itest) can be stabilized later.

## 9. Risks & Mitigations

- Refactor slows feature work → incremental phases with value each step.
- Protocol compatibility → accept legacy wire forms in typed parsers.
- Deadlocks via blocking RPC → enqueue + wait via channel, never hold locks while waiting.
- Complexity moved to adapters → define clear interfaces and test adapters.

## 10. Immediate Next Steps

1. Implement SDK serialization for gomobile entrypoints (single dispatcher).
2. Route machine RPC handlers through the same serialized execution path.
3. Add concurrency regression tests for SDK/Manager entrypoints.

