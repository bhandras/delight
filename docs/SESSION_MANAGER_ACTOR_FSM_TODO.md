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
- [x] Add minimal utilities:
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
  - [x] `localRunner` handle (gen + pid / started bool / etc.)
  - [x] `remoteRunner` handle (gen + pid / ready bool / etc.)
  - [x] durable agent state (`controlledByUser`, requests, versions)
  - [x] pending permission promises (map requestID → promise)
  - [x] dedupe windows (recent remote input IDs, recent outbound localIDs)
  - [x] connection state (ws connected? machine socket connected?)
  - [x] debouncer state (pending refresh/persist timers)

### 2.2 Define inputs

- [x] Events:
  - [x] `evWSConnected`, `evWSDisconnected`
  - [x] `evSessionUpdate`, `evMessageUpdate`, `evEphemeral`
  - [x] `evRunnerReady{gen}`, `evRunnerExited{gen, err}`
  - [x] `evPermissionRequested{requestID, tool, input}`
  - [x] `evDesktopTakeback` (desktop “space twice” takeback)
  - [x] `evTimerFired{name, nowMs}`
  - [ ] `evNow{nowMs}` (if using explicit clock ticks)
- [x] Commands:
  - [x] `cmdSwitchMode{target, reply}`
  - [x] `cmdRemoteSend{text, meta, localID?, reply}`
  - [x] `cmdAbortRemote{reply}`
  - [x] `cmdPermissionDecision{requestID, allow, message, reply}`
  - [x] `cmdShutdown{reply}`

### 2.3 Define effects

- [x] Runner lifecycle:
  - [x] `effStartLocalRunner{gen, workDir, resume}`
  - [x] `effStopLocalRunner{gen}`
  - [x] `effStartRemoteRunner{gen, workDir, resume}`
  - [x] `effStopRemoteRunner{gen}`
- [x] Messaging:
  - [x] `effRemoteSend{gen, text, meta}`
  - [x] `effRemoteAbort{gen}`
- [x] Persistence / state propagation:
  - [x] `effPersistAgentState{agentStateJSON, expectedVersion?}`
  - [x] `effEmitEphemeral{payload}`
  - [x] `effEmitMessage{encryptedPayload}`
- [x] Timers / debounce:
  - [x] `effStartTimer{name, afterMs}`
  - [x] `effCancelTimer{name}`

### 2.4 Reducer logic (pure)

- [x] Implement reducer transitions:
  - [x] `cmdSwitchMode(remote)`:
    - [x] if already remote-running: idempotent success
    - [x] else transition to `RemoteStarting`
    - [x] increment gen
    - [x] stop local runner effects
    - [x] start remote runner effect
    - [x] set `controlledByUser=false` in agent state
    - [x] emit persist effect (non-debounced)
  - [x] `cmdSwitchMode(local)`:
    - [x] symmetric, transition to `LocalStarting`
    - [x] set `controlledByUser=true`
  - [x] `evRunnerReady{gen}`:
    - [x] ignore if `gen != state.runnerGen`
    - [x] transition to `LocalRunning`/`RemoteRunning` depending on target runner
    - [x] complete pending switch reply
  - [x] `evRunnerExited{gen, err}`:
    - [x] ignore if stale gen
    - [x] if exiting during “Starting”, complete reply with error
    - [x] if exiting during “Running”, transition to safe state (typically `LocalRunning` fallback or `Closed`)
  - [x] `cmdRemoteSend`:
    - [x] only allowed in `RemoteRunning`
    - [x] emit remote send effect
  - [x] `evPermissionRequested`:
    - [x] only in remote mode; otherwise ignore/log
    - [x] persist into `agentState.requests`
    - [x] emit ephemeral to phone
  - [x] `cmdPermissionDecision`:
    - [x] resolve promise (if exists)
    - [x] update `agentState.requests` and `completedRequests`
    - [x] emit control response effect to remote runner

---

## 3) Runtime adapters (Go)

### 3.1 Local runner runtime

- [x] Implement `LocalRunnerRuntime`:
  - [x] start process (PTY, /dev/tty handling)
  - [x] emit `evRunnerReady{gen}` only after fully started and published
  - [x] spawn waiter goroutine that emits `evRunnerExited{gen, err}`
  - [ ] attach session ID detection + thinking events as events (not direct state writes)

### 3.2 Remote runner runtime

- [x] Implement `RemoteRunnerRuntime`:
  - [x] start Node bridge, wait for “ready”
  - [x] emit `evRunnerReady{gen}`
  - [ ] forward bridge messages as `evRemoteMessage{...}` events
  - [x] forward permission requests as `evPermissionRequested`
  - [x] emit `evRunnerExited{gen, err}`

### 3.3 Socket runtime

- [x] Decide and implement socket ownership strategy (A or B):
  - [x] Connect/disconnect events emitted into actor
  - [x] All outbound socket writes are effects (`effEmitMessage`, `effEmitEphemeral`, `effPersistAgentState`)
  - [x] Version-mismatch retry behavior handled deterministically by actor state + effects

---

## 4) Replace `Manager` with façade over SessionActor

- [x] Create a thin `Manager` wrapper that:
  - [x] constructs actor + runtime adapters
  - [x] exposes existing public methods by sending commands
  - [x] wires websocket callbacks to enqueue typed events (no state mutation)
- [x] Delete/stop using:
  - [x] `modeMu`, `stateMu`, `permissionMu`
  - [x] polling `Wait()` loop (replace with `actor.Wait()` / `done`)

---

## 5) Incremental migration steps (to keep parity stable)

### 5.1 Step-by-step cutover

- [x] Step 1: introduce actor loop but keep existing internal logic behind effects
- [x] Step 2: move mode switching into reducer; keep legacy updateState until stable
- [x] Step 3: move updateState persistence into actor (remove `go m.updateState()`)
- [x] Step 4: move permissions into actor-owned promises
- [x] Step 5: remove remaining mutexes and direct state reads

### 5.2 Compatibility checkpoints after each step

- [x] remote take control from phone works
- [x] desktop takeback (space twice) works
- [x] permission prompt appears on phone and resolves
- [x] CLI does not crash on rapid switching

---

## 6) Test plan (must-haves)

### 6.1 Unit tests (pure reducer)

- [x] local→remote switch emits correct effect sequence
- [x] remote→local switch emits correct effect sequence
- [x] stale `evRunnerExited{oldGen}` ignored
- [x] permission request adds durable `agentState.requests`
- [x] permission decision removes durable request and emits control response effect
- [x] debounced persistence coalesces multiple updates

### 6.2 Integration tests

- [x] stress test: switch modes 50 times without exiting session
- [ ] remote run produces assistant reply and shows on phone
- [ ] offline/online transitions update UI state properly

### 6.3 Race detector

- [x] `go test -race ./...` (at least `internal/session`)

---

## 7) Cleanup / deletion list (endgame)

- [x] Remove `Manager.Wait()` polling and any `Wait()` calls outside runtime waiters
- [x] Remove the following fields once actor owns them:
  - [x] `modeMu`, `stateMu`, `permissionMu`, `localRunMu`
  - [x] `spawnMu`, `spawnStoreMu`
  - [x] `recentRemoteInputsMu`, `recentOutboundUserLocalIDsMu`
  - [x] `desktopTakebackMu` (takeback watcher lifecycle can be actor-owned)
- [x] Remove `enqueueInbound(func())` in favor of typed mailbox inputs
