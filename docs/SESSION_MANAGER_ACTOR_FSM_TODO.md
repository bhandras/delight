# Session Manager Actor/FSM Refactor — Detailed TODO

This checklist tracks the work to refactor the Delight CLI session manager into
an actor-style, single-threaded FSM, following `docs/ACTOR_FSM_FRAMEWORK.md`.

Scope: `delight/cli/internal/session` and any runtime adapters used by the CLI.

Conventions:

- “Actor loop” means a single goroutine that owns all mutable state.
- Reducers must be side-effect free; they emit declarative effects.
- Runtimes interpret effects and emit events back to the actor mailbox.
- Use **generation IDs** for runner lifecycle and **request IDs** for promises.

---

## 0) Preflight

### 0.1 Contract confirmation

- [x] Re-read `docs/ACTOR_FSM_FRAMEWORK.md` and confirm the contract:
  - [x] side-effect free reducer
  - [x] runtime executes effects
  - [x] runtime emits events back
  - [x] state owned by actor loop only

### 0.2 Socket ownership decision (Phase 1 migration)

We will start with the incremental approach and revisit once the actor loop owns
mode switching and `Wait()`:

- [x] Option B (initial): sockets remain external, callbacks only enqueue events
- [ ] Option A (later): socket lifecycle becomes actor-owned (preferred end state)

Rationale:

- This minimizes churn during the first migration steps and keeps the refactor
  focused on eliminating the highest-risk concurrency hazards (runner lifecycle,
  mode switching, and `Wait()` behavior).
- Once those hazards are removed, moving sockets fully into effects/events is a
  mechanical follow-up.

### 0.3 Baseline parity invariants (must remain true throughout)

- [x] local mode: desktop controls, phone cannot send
- [x] remote mode: phone controls, desktop "space twice" takes back control
- [x] permission prompts durable on phone in remote mode only
- [x] switching does not crash / leak processes
- [x] remote replies appear on phone without extra send
- [x] offline/disconnected sessions disable phone sending/control UI

---

## 1) Framework scaffolding (Go)

### 1.1 Package layout

- [x] Create `cli/internal/actor` (or `cli/internal/fsm`) with:
  - [x] `Input` interface (`Event` + `Command`)
  - [x] `Effect` interface
  - [x] `Reducer[S]` interface or function type
  - [x] `Runtime` interface
  - [x] `Actor[S]` loop harness (mailbox, done channel, stop)
- [ ] Add minimal utilities:
  - [x] typed logger hooks (`OnInput`, `OnEffect`, `OnStateChange`) (optional but recommended)
  - [x] `Clock` abstraction for deterministic timers (or explicit timer effects)

### 1.2 Testing harness utilities

- [x] Add a fake runtime:
  - [x] records effects
  - [x] can be scripted to emit events
- [x] Add a reducer test helper:
  - [x] `Step(state, input) -> (state, effects)`
  - [x] snapshot-friendly state printing (if needed)

---

## 2) SessionActor: typed inputs, effects, state

### 2.1 Define state (single owner)

- [x] Create `sessionState` struct to replace scattered `Manager` fields:
  - [x] `mode` + explicit FSM state (`LocalStarting`, `LocalRunning`, …)
  - [x] `runnerGen` (monotonic)
  - [ ] `localRunner` handle (gen + pid / started bool / etc.)
  - [ ] `remoteRunner` handle (gen + pid / ready bool / etc.)
  - [ ] durable agent state (`controlledByUser`, requests, versions)
  - [x] pending permission promises (map requestID → promise)
  - [ ] dedupe windows (recent remote input IDs, recent outbound localIDs)
  - [ ] connection state (ws connected? machine socket connected?)
  - [ ] debouncer state (pending refresh/persist timers)

### 2.2 Define inputs

- [ ] Events:
  - [ ] `evWSConnected`, `evWSDisconnected`
  - [ ] `evSessionUpdate`, `evMessageUpdate`, `evEphemeral`
  - [x] `evRunnerReady{gen}`, `evRunnerExited{gen, err}`
  - [x] `evPermissionRequested{requestID, tool, input}`
  - [x] `evDesktopTakeback` (desktop “space twice” takeback)
  - [ ] `evTimerFired{name, nowMs}`
  - [ ] `evNow{nowMs}` (if using explicit clock ticks)
- [ ] Commands:
  - [x] `cmdSwitchMode{target, reply}`
  - [x] `cmdRemoteSend{text, meta, localID?, reply}`
  - [x] `cmdAbortRemote{reply}`
  - [x] `cmdPermissionDecision{requestID, allow, message, reply}`
  - [ ] `cmdShutdown{reply}`

### 2.3 Define effects

- [ ] Runner lifecycle:
  - [x] `effStartLocalRunner{gen, workDir, resume}`
  - [x] `effStopLocalRunner{gen}`
  - [x] `effStartRemoteRunner{gen, workDir, resume}`
  - [x] `effStopRemoteRunner{gen}`
- [ ] Messaging:
  - [x] `effRemoteSend{gen, text, meta}`
  - [x] `effRemoteAbort{gen}`
- [ ] Persistence / state propagation:
  - [x] `effPersistAgentState{agentStateJSON, expectedVersion?}`
  - [ ] `effEmitEphemeral{payload}`
  - [ ] `effEmitMessage{encryptedPayload}`
- [ ] Timers / debounce:
  - [ ] `effStartTimer{name, afterMs}`
  - [ ] `effCancelTimer{name}`

### 2.4 Reducer logic (pure)

- [ ] Implement reducer transitions:
  - [ ] `cmdSwitchMode(remote)`:
    - [x] if already remote-running: idempotent success
    - [x] else transition to `RemoteStarting`
    - [x] increment gen
    - [x] stop local runner effects
    - [x] start remote runner effect
    - [ ] set `controlledByUser=false` in agent state
    - [x] emit persist effect (non-debounced)
  - [ ] `cmdSwitchMode(local)`:
    - [x] symmetric, transition to `LocalStarting`
    - [ ] set `controlledByUser=true`
  - [ ] `evRunnerReady{gen}`:
    - [x] ignore if `gen != state.runnerGen`
    - [x] transition to `LocalRunning`/`RemoteRunning` depending on target runner
    - [x] complete pending switch reply
  - [ ] `evRunnerExited{gen, err}`:
    - [x] ignore if stale gen
    - [x] if exiting during “Starting”, complete reply with error
    - [ ] if exiting during “Running”, transition to safe state (typically `LocalRunning` fallback or `Closed`)
  - [ ] `cmdRemoteSend`:
    - [x] only allowed in `RemoteRunning`
    - [x] emit remote send effect
  - [ ] `evPermissionRequested`:
    - [ ] only in remote mode; otherwise ignore/log
    - [ ] persist into `agentState.requests`
    - [ ] emit ephemeral to phone
  - [ ] `cmdPermissionDecision`:
    - [ ] resolve promise (if exists)
    - [ ] update `agentState.requests` and `completedRequests`
    - [ ] emit control response effect to remote runner

---

## 3) Runtime adapters (Go)

### 3.1 Local runner runtime

- [ ] Implement `LocalRunnerRuntime`:
  - [ ] start process (PTY, /dev/tty handling)
  - [ ] emit `evRunnerReady{gen}` only after fully started and published
  - [ ] spawn waiter goroutine that emits `evRunnerExited{gen, err}`
  - [ ] attach session ID detection + thinking events as events (not direct state writes)

### 3.2 Remote runner runtime

- [ ] Implement `RemoteRunnerRuntime`:
  - [ ] start Node bridge, wait for “ready”
  - [ ] emit `evRunnerReady{gen}`
  - [ ] forward bridge messages as `evRemoteMessage{...}` events
  - [ ] forward permission requests as `evPermissionRequested`
  - [ ] emit `evRunnerExited{gen, err}`

### 3.3 Socket runtime

- [ ] Decide and implement socket ownership strategy (A or B):
  - [ ] Connect/disconnect events emitted into actor
  - [ ] All outbound socket writes are effects (`effEmitMessage`, `effEmitEphemeral`, `effPersistAgentState`)
  - [ ] Version-mismatch retry behavior handled deterministically by actor state + effects

---

## 4) Replace `Manager` with façade over SessionActor

- [ ] Create a thin `Manager` wrapper that:
  - [ ] constructs actor + runtime adapters
  - [ ] exposes existing public methods by sending commands
  - [ ] wires websocket callbacks to enqueue typed events (no state mutation)
- [ ] Delete/stop using:
  - [ ] `modeMu`, `stateMu`, `permissionMu`
  - [ ] polling `Wait()` loop (replace with `actor.Wait()` / `done`)

---

## 5) Incremental migration steps (to keep parity stable)

### 5.1 Step-by-step cutover

- [ ] Step 1: introduce actor loop but keep existing internal logic behind effects
- [ ] Step 2: move mode switching into reducer; keep legacy updateState until stable
- [ ] Step 3: move updateState persistence into actor (remove `go m.updateState()`)
- [ ] Step 4: move permissions into actor-owned promises
- [ ] Step 5: remove remaining mutexes and direct state reads

### 5.2 Compatibility checkpoints after each step

- [ ] remote take control from phone works
- [ ] desktop takeback (space twice) works
- [ ] permission prompt appears on phone and resolves
- [ ] CLI does not crash on rapid switching

---

## 6) Test plan (must-haves)

### 6.1 Unit tests (pure reducer)

- [ ] local→remote switch emits correct effect sequence
- [ ] remote→local switch emits correct effect sequence
- [x] stale `evRunnerExited{oldGen}` ignored
- [ ] permission request adds durable `agentState.requests`
- [ ] permission decision removes durable request and emits control response effect
- [ ] debounced persistence coalesces multiple updates

### 6.2 Integration tests

- [ ] stress test: switch modes 50 times without exiting session
- [ ] remote run produces assistant reply and shows on phone
- [ ] offline/online transitions update UI state properly

### 6.3 Race detector

- [x] `go test -race ./...` (at least `internal/session`)

---

## 7) Cleanup / deletion list (endgame)

- [ ] Remove `Manager.Wait()` polling and any `Wait()` calls outside runtime waiters
- [ ] Remove the following fields once actor owns them:
  - [ ] `modeMu`, `stateMu`, `permissionMu`, `localRunMu`
  - [x] `spawnMu`, `spawnStoreMu`
  - [ ] `recentRemoteInputsMu`, `recentOutboundUserLocalIDsMu`
  - [ ] `desktopTakebackMu` (takeback watcher lifecycle can be actor-owned)
- [ ] Remove `enqueueInbound(func())` in favor of typed mailbox inputs
