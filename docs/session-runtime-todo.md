# Session Runtime Refactor TODO Plan (Detailed)

Status: Draft / active  
Owner: bhandras  
Last updated: 2025-12-28  

This plan is designed to deliver incremental value with minimal disruption. Each phase ends with something testable and shippable.

## Phase 0 — Stabilize concurrency (in progress)

### 0.1 Route *all inbound events* through one serialized queue

- [x] Session socket updates (`update`, `session-update`) queued in `session.Manager`
- [x] Session RPC handlers (`switch`, `abort`, `permission`) routed through the same serialized queue
- [ ] Machine RPC handlers (`spawn-happy-session`, `stop-session`, `stop-daemon`) routed through the same serialized queue
  - Rationale: machine RPC may be invoked concurrently with session events and mode switching.
  - Implementation idea:
    - create `runInboundRPCMachine(fn)` or reuse `runInboundRPC` but ensure it works for machine RPC too
    - ensure handlers don’t mutate `spawnedSessions` outside the queue

### 0.2 Route agent callbacks through the queue

- [ ] Ensure Codex event handler callback enqueues rather than mutating state directly
- [ ] Ensure RemoteBridge callbacks enqueue rather than mutating state directly
- [ ] Ensure Claude scanner forwarding loop does not touch shared fields directly

### 0.3 Verify

- [ ] Add a test that calls queued handlers concurrently (where possible) and verifies order and no deadlock.

## Phase 1 — SDK-level serialization (done)

Problem: gomobile exported methods can be invoked concurrently from Swift; Go side must be the single source of concurrency control.

- [x] Add `cli/sdk/dispatch.go` dispatcher
- [x] Wrap exported SDK entrypoints to run on a single dispatch queue
- [x] Run listener callbacks on a separate callback queue (avoid deadlocks when callback calls back into SDK)
- [x] Add tests for dispatch ordering and concurrency safety

## Phase 2 — Typed protocol objects (readability + correctness)

### 2.1 Add `internal/protocol/wire`

- [ ] `update.go`: typed parsing for `update` events and `new-message` payload extraction
- [ ] `rpc.go`: typed request/response structs for:
  - session RPC (`switch`, `abort`, `permission`)
  - machine RPC (`spawn-happy-session`, `stop-session`, `stop-daemon`)
- [ ] `message.go`: canonical internal representation for:
  - encrypted payload
  - plaintext user message

### 2.2 Replace inline structs and map parsing

- [ ] Replace inline RPC structs in `session/manager.go`
- [ ] Replace `handleUpdate` / `handleMessage` manual extraction logic with typed parser

### 2.3 Tests

- [ ] Golden tests with real captured payloads from logs.

## Phase 3 — Mechanical split of `manager.go`

Goal: reduce cognitive load without behavior changes.

- [ ] Split into:
  - `manager_start.go`
  - `manager_rpc.go`
  - `manager_machine.go`
  - `manager_spawn.go`
  - `manager_mode.go`
  - `manager_crypto.go`
  - `manager_agent_codex.go`
  - `manager_agent_claude.go`
  - `manager_agent_acp.go`

## Phase 4 — Introduce `session/runtime`

Goal: replace ad-hoc state mutation and locks with a formal state machine.

- [ ] Create `internal/session/runtime` with:
  - `state.go`, `events.go`, `runtime.go` (loop), `commands.go` (effects)
- [ ] Move inbound queue responsibility from Manager into Runtime
- [ ] Manager becomes a facade that wires:
  - crypto context
  - transports
  - agent adapters
  - runtime loop

## Phase 5 — Commands/effects runner + deterministic tests

- [ ] Runtime reducer produces commands instead of IO
- [ ] Effects runner executes commands and posts result events
- [ ] Add deterministic reducer tests:
  - send 2 messages quickly
  - switch mode mid-request
  - permission request lifecycle
  - spawn/stop session lifecycle

