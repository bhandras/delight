# Session Manager Actor/FSM Refactor Plan (Delight CLI)

This document proposes a refactor of the Delight CLI session manager to a
single-threaded **actor-style control loop** with an explicit **finite state
machine (FSM)**.

The goal is to eliminate the current class of concurrency races that arise from
shared mutable state guarded by multiple mutexes across many goroutines. These
races have already surfaced as production bugs (e.g. `process not started` on
mode switch), and they will continue to appear as local/remote parity work
expands.

## Why this refactor

### Current symptoms

- Mode switching can race the session’s `Wait()` loop and crash the session:
  - example: `SwitchToLocal()` publishes a `claudeProcess` pointer before it is
    started, and concurrently `Wait()` observes it and calls `Process.Wait()`,
    returning `process not started`, which tears down the CLI session.
- Several state updates are performed from background goroutines (`go
  m.updateState()` etc.), requiring mutexes and still leaving edge windows.
- Control state, permission durability, and runner lifecycle events are handled
  through mixed concurrency patterns (queue + mutexes), increasing complexity.

### Root cause

The `Manager` already has an `inboundQueue` that serializes some inbound events,
but that queue is **not the single source of truth** for *all* state mutations
and observations. Core fields (`mode`, runner pointers, agent state, permissions
maps) are still read/written concurrently.

## Target architecture

### Core invariant (must hold always)

> Only the session loop goroutine may read/write mutable session state.

This eliminates most mutexes because there is no concurrent access to those
fields. Goroutines still exist, but they are limited to **blocking I/O** and
they communicate exclusively by sending typed events into the loop.

### Components

1. **Session actor loop**
   - Owns all mutable state.
   - Receives `command`s (requests) and `event`s (observations).
   - Applies state transitions through a single switch statement / transition
     table.

2. **Runners**
   - Local runner: interactive Claude PTY/TUI process + session scanner.
   - Remote runner: bridge process + SDK stream-json handlers.
   - Runner start/stop/exit are coordinated by the session loop.

3. **Event sources** (goroutines allowed)
   - Websocket callbacks (`update`, `session-update`, `ephemeral`)
   - Runner exit waiters (`proc.Wait`, `bridge.Wait`)
   - PTY/session scanner output
   - Timers (keepalive tick)
   - Desktop hotkey watcher (`Ctrl+L` takeback)

Each event source is a goroutine that *never touches state directly*. It only
enqueues typed events.

## Explicit FSM

### Suggested states

- `LocalStarting`
- `LocalRunning`
- `RemoteStarting`
- `RemoteRunning`
- `Closing`
- `Closed`

### Suggested state transitions (examples)

- `LocalRunning + cmdSwitch(Remote)` → `RemoteStarting`
  - stop local watchers/scanner
  - kill local process
  - start remote bridge
  - `agentState.controlledByUser=false`
  - persist state
  - → `RemoteRunning`

- `RemoteRunning + cmdSwitch(Local)` → `LocalStarting`
  - stop remote bridge
  - start local Claude process
  - `agentState.controlledByUser=true`
  - persist state
  - → `LocalRunning`

### Runner generation IDs (critical)

To avoid “late” exits affecting the current runner, the loop should maintain a
monotonic `runnerGen`:

- Starting a runner increments `runnerGen` and attaches it to the waiter.
- `eventRunnerExited{gen: X}` is ignored unless `X == currentGen`.

This prevents old exits from killing a new mode.

## Command API (sync request/reply)

Public methods become thin wrappers that send a command into the loop and await
the result:

- `SwitchToRemote()` → `cmdSwitch{mode: remote, reply: chan error}`
- `SwitchToLocal()` → `cmdSwitch{mode: local, reply: chan error}`
- `SendUserMessage()` → `cmdRemoteSend{... reply ...}`
- RPC handlers call into the same commands (so RPC is automatically serialized)

## State ownership

All of the below become loop-owned and should no longer require mutexes:

- `mode`
- `claudeProcess` / `remoteBridge`
- agent state (`controlledByUser`, `requests`, `completedRequests`, versions)
- pending permission requests
- recent remote inputs / outbound local IDs (dedupe windows)
- takeback watcher lifecycle state
- spawned session tracking

## Migration plan (incremental and safe)

### Phase 0 — Agree on invariants and boundaries

- Define the new loop-owned state struct (`sessionState`).
- Decide what may exist outside the loop:
  - sockets/clients may exist outside, but **callbacks must only enqueue events**
  - no direct reads of `m.mode`, runner pointers, or agent state outside loop

### Phase 1 — Replace `inboundQueue chan func()` with typed `event` + `command`

- Introduce:
  - `type event interface { isEvent() }`
  - `type command interface { isCommand(); replyCh() }`
- Implement `runLoop()` to `select` over:
  - commands channel
  - events channel
  - timers / context cancellation
- Keep `enqueueInbound(func())` temporarily as a compatibility shim that wraps
  `func()` into a command/event.

### Phase 2 — Move `Wait()` to be loop-owned (remove polling reads)

Current issue: `Wait()` polls shared state and calls runner waits directly.

New design:

- The loop starts a runner waiter goroutine that sends `eventRunnerExited`.
- `Manager.Wait()` waits on a `loopDone` channel or context cancellation.

This alone removes a major class of races.

### Phase 3 — Move mode switching fully into loop

- `SwitchToLocal/Remote` become commands handled in the loop.
- Remove `modeMu`.
- Loop guarantees “publish runner pointer only after Start() succeeds”.
- Late exit events are ignored via runner generations.

### Phase 4 — Move `agentState` persistence into the loop

- Replace `stateMu` and `stateDirty` with loop-owned:
  - `agentState`
  - `agentStateVersion`
  - `needPersist bool` (or small queue)
- Persist on:
  - control changes
  - permission request add/remove
  - reconnect
  - periodic tick

Eliminate `go m.updateState()` and any concurrent persistence attempts.

### Phase 5 — Move permission plumbing into loop

- Pending permission requests map is loop-owned.
- Remote runner permission request becomes:
  - enqueue `eventPermissionRequested{...}`
  - loop persists `agentState.requests` and emits ephemeral event
- Permission response RPC becomes:
  - enqueue `cmdPermissionDecision{...}`
  - loop completes pending promise and removes `agentState.requests`

Remove `permissionMu`.

### Phase 6 — Spawned sessions become separate actors

- Parent session actor owns children handles.
- Each spawned session has its own event loop.
- Parent interacts via commands/events only.

Remove `spawnMu` and `spawnStoreMu`.

### Phase 7 — Delete mutexes and simplify

As each phase absorbs ownership into the loop, remove now-unused mutexes and
invariants:

- `modeMu`
- `stateMu`
- `permissionMu`
- `localRunMu`
- `spawnMu`
- `spawnStoreMu`
- `recentRemoteInputsMu`
- `recentOutboundUserLocalIDsMu`
- `desktopTakebackMu`

## Testing strategy

### Deterministic unit tests (no real processes)

Create a fake runner that can:

- Start
- Stop
- Exit (immediately or delayed)
- Emit transcript/perms events

Test:

- Rapid `remote ↔ local` switching
- Switch while runner is starting
- Old exit events ignored (runner generation)
- Permission request/response deadlock-free behavior

### Integration tests

Keep existing `internal/itest` coverage and add one new scenario:

- Start remote → switch local → ensure session does not exit
- Repeat 20–50 times to catch flakiness

### Race detection

Run:

- `go test -race ./...` (or at least `./internal/session`)

## Open design choice (decide before Phase 1)

Should the actor loop own the websocket clients entirely (connect/reconnect,
send, and callback wiring), or can sockets remain outside the loop as long as
their callbacks only enqueue events?

Either approach works; fully loop-owned sockets reduce complexity further but
may require more upfront change.

