# Unified Actor + FSM Framework (Design Doc)

This document defines a **unified actor + finite state machine (FSM) framework**
intended for reuse across Delight (CLI, server-side components, and mobile
clients where applicable).

The goal is to make complex orchestration (local/remote switching, permission
flows, socket reconnects, process lifecycle, UI control state) **deterministic**,
**testable**, and **race-free** by enforcing:

1. **Single-threaded state mutation** (actor loop owns all mutable state)
2. **Side-effect free transitions** (pure reducer returns effects)
3. **Explicit effect execution** (effects are data, interpreted by a runtime)

---

## 1) Objectives and non-objectives

### Objectives

- **Expressive** enough to model:
  - multi-step transitions (start runner → await ready → mark controlledByUser)
  - retries/backoff
  - timeouts and cancellation
  - correlation (generation IDs, request IDs)
  - hierarchical composition (session actor owns runner actors)
- **Side-effect free FSM execution**:
  - reducer has no I/O, no sleeps, no goroutine spawn
  - effects are pure descriptions (data structures)
  - a runtime interprets effects and emits events back to the actor
- **Seamless testing**:
  - unit tests can run the reducer with synthetic events
  - integration tests can run with real runtime adapters
- **Operational clarity**:
  - traceable event/effect logs
  - deterministic ordering guarantees

### Non-objectives

- This is not a general distributed consensus framework.
- It does not require global ordering across different actors.
- It does not mandate a UI architecture (SwiftUI/React/etc.), only the state
  semantics and event model.

---

## 2) Core model

### 2.1 Actor loop: single owner of state

Each actor has:

- `State` (mutable only within the loop)
- `Reducer` (pure)
- `Runtime` (effect interpreter)
- `Mailbox` (events + commands)

The actor loop is conceptually:

```
for {
  select event/command from mailbox
  (newState, effects) = reducer(state, input)
  state = newState
  runtime.execute(effects) // async; emits new events back
}
```

**Key invariant**: only the actor loop goroutine can mutate `state`.

### 2.2 Side-effect free reducer

Reducer signature:

- `Reduce(state, input) -> (nextState, []Effect)`

Reducer rules:

- no I/O
- no goroutines
- no time reads (`time.Now`) inside reducer
- no random UUID generation inside reducer
- all external values come in via events (including "now")

This enables deterministic replay and cheap unit testing.

### 2.3 Effects are data, not execution

Effects are declarative instructions such as:

- `StartProcess{kind, args, env, workDir, generation}`
- `StopProcess{generation, signal}`
- `EmitSocket{eventName, payload}`
- `PersistState{key, value}`
- `StartTimer{name, afterMs}`
- `CancelTimer{name}`
- `SpawnChild{actorType, childID, initialState}`
- `SendChild{childID, message}`

Effects must be **idempotent** when possible and must carry enough identifiers
to make retries safe (generation IDs, request IDs).

---

## 3) Inputs: Events vs Commands

We distinguish:

### 3.1 Events (observations)

Events come from the outside world (runtime adapters) and represent facts:

- `WSConnected`, `WSDisconnected{reason}`
- `MessageReceived{sid, encryptedPayload}`
- `RunnerReady{generation}`, `RunnerExited{generation, err}`
- `PermissionRequested{requestID, tool, input}`
- `TimerFired{name}`
- `Now{unixMs}` (optional explicit clock event)

Events are always enqueued into the actor mailbox; the reducer processes them
sequentially.

### 3.2 Commands (requests)

Commands are requests from a caller that expects a response:

- `SwitchMode{targetMode, reply chan error}`
- `SendUserMessage{text, localID, reply chan error}`
- `SubmitPermissionDecision{requestID, allow, message, reply chan error}`

Implementation note:

- A command is handled inside the reducer (or a thin dispatcher before reducer),
  which emits effects and also completes the `reply` either immediately or when
  a later event arrives.

---

## 4) Effect runtime: the interpreter boundary

### 4.1 Runtime responsibilities

The runtime executes effects and sends events back:

- spawn processes, attach stdio/pty, watch exit
- manage websocket clients (connect/reconnect, send)
- schedule timers
- persist state (db/file) and report success/failure
- bridge external callbacks into mailbox events

### 4.2 Runtime constraints

- Runtime must never mutate actor state directly.
- Runtime may run concurrently, but only communicates via events.
- Runtime must include correlation fields when emitting events (e.g. `generation`).

### 4.3 Fake runtime (tests)

For unit tests:

- replace runtime with a fake interpreter that records effects
- tests can assert:
  - next state
  - effect sequence
  - absence/presence of specific effects

---

## 5) Determinism: time, randomness, and IDs

To keep reducer deterministic:

- Any use of time must come in as an event field:
  - either include timestamps on each event from runtime
  - or have an explicit `Now{unixMs}` tick event
- Any random IDs (UUID/CUID) must be generated by runtime and supplied in:
  - command payloads (client-provided local IDs)
  - runtime-generated event fields (`generatedID`)

---

## 6) Concurrency safety patterns

### 6.1 Generation IDs for runner lifecycle

When (re)starting a runner:

- actor increments `runnerGen`
- actor emits `StartProcess{generation: runnerGen}`
- runtime emits `RunnerReady{generation}` and `RunnerExited{generation}`

Reducer ignores any `RunnerExited` that does not match the current generation.

This eliminates stale exit races during mode switches.

### 6.2 Promises via request IDs (permissions, RPC)

For flows that block an external party (e.g. permission request):

- reducer creates `pending[requestID] = promise`
- runtime emits `PermissionRequested{requestID}`
- command `SubmitPermissionDecision{requestID}` completes promise
- reducer emits `SendControlResponse{requestID, decision}`

This keeps the core state machine single-threaded while still supporting
long-lived waits without blocking the loop.

### 6.3 Debounced refresh patterns

Some effects should be coalesced:

- `RefreshSessions` (mobile) or `PersistAgentState` (cli) should be debounced
  in reducer using:
  - last-at timestamp in state
  - a timer effect (start/cancel)

Debouncing is deterministic because time is provided by events.

---

## 7) Composition: hierarchical actors

### 7.1 Parent-child actors

Large systems should be decomposed:

- `SessionActor` (owns control mode, permissions, durable agentState)
  - `LocalRunnerActor`
  - `RemoteRunnerActor`
  - `SocketActor` (optional; see below)

Child actors encapsulate their own FSM and effects. Parent communicates by:

- `SpawnChild{...}`
- `SendChild{...}`
- receiving child events (`ChildEvent{childID, event}`)

### 7.2 Socket ownership decision

Two valid strategies:

1) **Loop-owned sockets (recommended where feasible)**
   - socket connects/sends are effects
   - socket callbacks emit events
   - actor controls all lifecycle transitions

2) **External sockets with event-only callbacks**
   - socket objects live outside actor
   - callbacks *only* enqueue events

Strategy (1) reduces ambiguity and makes reconnect behavior deterministic.
Strategy (2) can be a stepping stone during migration.

---

## 8) Reference implementation sketch (Go)

### 8.1 Types

```
type Input interface{ isInput() } // event or command

type Event interface{ Input }
type Command interface{
  Input
  replyCh() chan any
}

type Effect interface{ isEffect() }

type Reducer[S any] interface {
  Reduce(s S, in Input) (S, []Effect)
}

type Runtime interface {
  Execute([]Effect)
}
```

### 8.2 Actor structure

```
type Actor[S any] struct {
  state   S
  reduce  func(S, Input) (S, []Effect)
  runtime Runtime
  inbox   chan Input
  done    chan struct{}
}
```

### 8.3 Execution rules

- only one goroutine executes the reduce/apply loop
- runtime execution is asynchronous and must enqueue events back via `inbox`
- commands must be completed by reducer (immediate) or by stateful correlation
  later (pending map)

---

## 9) How we’ll apply this to Delight session manager

The following components become actors:

- `SessionActor` (the orchestrator)
- `LocalClaudeActor` (PTY runner)
- `RemoteClaudeActor` (bridge runner)

The current mutex-heavy `Manager` becomes a thin façade:

- public methods send commands into `SessionActor`
- websocket callbacks enqueue events
- process/scanner callbacks enqueue events

This directly prevents races like:

- `Wait()` observing partially started runners
- late exit events affecting current mode
- persistence attempts racing state mutation

---

## 9.1 Sequence diagrams

The diagrams below use Mermaid syntax.

### A) Command-driven mode switch (local → remote)

```mermaid
sequenceDiagram
    autonumber
    participant Caller as Caller (RPC/UI)
    participant Actor as SessionActor (loop)
    participant RT as Runtime (effects)
    participant Bridge as Remote Runner (process)

    Caller->>Actor: cmdSwitch(to=remote, replyCh)
    Actor->>Actor: Reduce(state, cmdSwitch)\n=> state=RemoteStarting, gen++
    Actor-->>RT: Effect(StartRemoteRunner{gen})
    Actor-->>RT: Effect(PersistAgentState{controlledByUser=false})

    RT->>Bridge: spawn process + wire stdio
    Bridge-->>RT: "ready" (observed)
    RT-->>Actor: eventRunnerReady{gen}
    Actor->>Actor: Reduce(state, eventRunnerReady)\n=> state=RemoteRunning
    Actor-->>Caller: replyCh <- nil
```

Notes:

- The reducer stays pure: it only computes `(nextState, effects)`.
- The runtime performs I/O and emits an event back when the effect completes.
- The `gen` correlates readiness/exits to the currently active runner.

### B) Permission prompt round-trip (async “promise”)

```mermaid
sequenceDiagram
    autonumber
    participant Bridge as Remote Runner (process)
    participant RT as Runtime (effects)
    participant Actor as SessionActor (loop)
    participant Phone as Phone UI
    participant Caller as RPC Handler

    Bridge-->>RT: control_request{requestID, tool, input}
    RT-->>Actor: eventPermissionRequested{requestID, tool, input}
    Actor->>Actor: Reduce(...)\n=> pending[requestID]=promise
    Actor-->>RT: Effect(EmitEphemeralPermissionRequest{requestID,...})
    RT-->>Phone: permission-request event

    Phone->>Caller: RPC session:permission {requestID, allow}
    Caller->>Actor: cmdPermissionDecision{requestID, allow, replyCh}
    Actor->>Actor: Reduce(...)\n=> resolve pending[requestID]
    Actor-->>RT: Effect(SendControlResponse{requestID, allow})
    Actor-->>Caller: replyCh <- nil

    RT-->>Bridge: control_response{requestID, allow}
```

Notes:

- The actor never blocks waiting for the phone. It stores a pending entry keyed
  by `requestID`.
- The external runner is blocked waiting for its control response, but the actor
  loop continues processing other events/commands normally.

---

## 10) Testing and rollout

### 10.1 Unit tests

- reducer tests:
  - `SwitchToRemote` produces expected effects sequence
  - `RunnerExited{staleGen}` is ignored
  - permission request/decision updates durable request maps

### 10.2 Integration tests

- repeated local↔remote switching
- permission prompt round-trip (remote)
- reconnect sequences (socket disconnect/reconnect)

### 10.3 Migration strategy

- Introduce framework utilities (`internal/fsm` or `internal/actor`)
- Port one subsystem first (session manager)
- Keep current interfaces; replace internals incrementally
