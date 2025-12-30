# Socket.IO Server Handler Refactor Plan

## Context

`server/internal/websocket/socketio.go` currently contains a large amount of
inline Socket.IO event handler logic. While functional, it is hard to:

- understand and review (business logic mixed with transport concerns),
- add unit tests (handlers depend on live socket callbacks),
- evolve wire protocol safely (request/ACK mapping is scattered),
- reason about concurrency (shared registries and per-socket state are accessed
  from many inline closures).

This document proposes an incremental refactor that keeps behavior stable while
moving logic into small, testable handler functions. The end state is a thin
network/transport layer that only:

- extracts auth/socket context,
- decodes wire payloads,
- calls a pure(ish) handler function,
- returns typed ACKs and emits/broadcasts updates.

## Goals

- Reduce complexity in `server/internal/websocket/socketio.go`.
- Make event handling deterministic and unit-testable.
- Centralize wire request/ACK typing using `protocol/wire`.
- Make concurrency ownership explicit (who owns locks, who owns state).
- Preserve current behavior and compatibility with existing clients.

Non-goals (for this refactor):

- Changing event names, wire field names, or semantics.
- Replacing Socket.IO with another transport.
- Broad database schema changes.

## Proposed Structure

### Transport layer (thin)

`server/internal/websocket/socketio.go`

- Declares event registration (event name -> adapter function).
- Converts Socket.IO inbound args -> typed request (`protocol/wire`).
- Invokes handler.
- Encodes handler output into:
  - ACK response (typed payload structs),
  - outbound update events (broadcast to a session/account).

### Handler layer (testable)

New package: `server/internal/websocket/handlers`

- One file per “area” or per event family (start simple; keep files small).
- Exposes functions that accept typed inputs and dependencies and returns typed
  outputs. No Socket.IO types in this package.

Example shape (indicative, not prescriptive):

```go
type AuthContext struct {
	UserID     string
	ClientType string
	SocketID   string
}

type Result[A any] struct {
	Ack     A
	Updates []protocolwire.UpdateToEmit
}

func UpdateState(
	ctx context.Context,
	deps Deps,
	auth AuthContext,
	req protocolwire.UpdateStatePayload,
) (Result[protocolwire.VersionedAck], error)
```

### Dependency surface (narrow interfaces)

New file: `server/internal/websocket/deps.go` (or `server/internal/websocket/handlers/deps.go`)

Define the minimal interfaces needed by handlers. This is what makes unit tests
easy and prevents handlers from reaching into transport or global singletons.

Typical deps:

- DB query methods (backed by `models.Queries`)
- Update/broadcast emitter (implemented by `SocketIOServer`)
- RPC registry (method -> socket mapping), with explicit locking rules

### Transport helpers (optional)

New package: `server/internal/websocket/transport`

- `DecodeAny` helpers (or wrap existing `decodeAny`).
- ACK extraction helpers:
  - `getFirstAnyWithAck` (already exists in `socketio.go`)
  - consistent error-to-ack mapping utilities

This is optional; avoid over-engineering. The key is to keep Socket.IO types
from leaking into the handler package.

## Event Inventory (target extraction list)

These are the handlers that currently carry most of the complexity / risk:

- Machine:
  - `machine-update-metadata`
  - `machine-update-state`
- Sessions:
  - `update-metadata`
  - `update-state`
- Access keys:
  - `access-key-get`
- Artifacts:
  - `artifact-read`
  - `artifact-create`
  - `artifact-update`
  - `artifact-delete`
- RPC:
  - `rpc-register`
  - `rpc-unregister`
  - `rpc-call` (includes forward via `rpc-request`)

## Milestones

### M1 — Scaffolding: handler API + deps

Deliverables:

- Define `AuthContext` type.
- Define `Deps` interfaces/struct:
  - DB operations required by extracted handlers.
  - RPC registry interface.
  - An abstract “update emitter” interface (or return “updates to emit” and
    let the transport do the emitting).
- Define handler output types:
  - ACK payload type (typed struct from `protocol/wire`).
  - Zero or more “updates to emit” instructions.

Acceptance criteria:

- No behavior change.
- A minimal unit-test harness can be written for handlers by faking `Deps`.

### M2 — Extract 1–2 representative handlers + first tests

Recommended first extractions:

- `artifact-update` (complex branching, version mismatch)
- `update-state` (nullable payload, versioned ack)

Deliverables:

- Move logic from `socketio.go` into `handlers/*.go`.
- Replace the inline handler bodies with:
  - decode -> call handler -> ack -> emit updates.
- Add unit tests for the extracted handlers:
  - invalid params,
  - not found / wrong account,
  - version mismatch,
  - success.

Acceptance criteria:

- `socketio.go` is smaller and still readable.
- Unit tests validate handler behavior without Socket.IO.

### M3 — Extract remaining CRUD-ish handlers + tests

Scope:

- `artifact-read/create/delete`
- `access-key-get`
- `update-metadata`
- `machine-update-metadata/state`

Deliverables:

- One handler function per event.
- Unit tests for each event, prioritizing:
  - parameter validation,
  - auth/account checks,
  - optimistic concurrency version mismatch paths,
  - ack payload shape correctness.

Acceptance criteria:

- Most request/ACK logic lives outside `socketio.go`.
- Wire typing (`protocol/wire`) is used consistently for requests and ACKs.

### M4 — Extract RPC logic and isolate concurrency

Scope:

- `rpc-register`
- `rpc-unregister`
- `rpc-call`

Deliverables:

- Introduce a small RPC registry type with explicit locking and ownership
  (`sync.RWMutex` encapsulated behind methods).
- `rpc-call` handler returns a “forward request” instruction; the transport
  layer performs the actual `EmitWithAck("rpc-request", ...)` to the target
  socket and translates the result back into a typed ACK.
- Unit tests:
  - missing method,
  - calling self-socket disallowed,
  - target not available,
  - forward timeout/error mapping to ack.

Acceptance criteria:

- RPC registry concurrency is explicit and testable.
- `socketio.go` no longer contains RPC business logic.

### M5 — Make `socketio.go` a declarative router

Deliverables:

- Replace repeated boilerplate with a small, consistent adapter pattern:
  - event name, request type, handler function.
- Standardize logging and error mapping:
  - e.g., handler returns sentinel errors (`ErrInvalidParams`, `ErrNotFound`,
    `ErrForbidden`, etc.) and the adapter maps them to a typed ACK.

Acceptance criteria:

- `socketio.go` is primarily:
  - event registration,
  - decoding,
  - calling handler,
  - emitting updates.
- Handler implementations contain the logic; adapters are consistent.

### M6 — Optional: lightweight adapter-level tests

Only if it stays cheap:

- Add tests around adapter decode/ack wiring using a minimal fake “socket”
  abstraction (not a full socket.io server).

Acceptance criteria:

- Most logic remains tested at the handler level; adapter tests only validate
  glue invariants.

## Concurrency Notes (design constraints)

- Handlers should not reach into shared maps directly; shared state must be
  owned by a small component with well-defined locking.
- Socket-derived context (`UserID`, `SocketID`, etc.) should be passed explicitly
  into handler functions; avoid global lookups.
- DB optimistic concurrency remains in handlers:
  - compare expected version,
  - return typed mismatch ACK (with current version/data when applicable).

## Suggested “Definition of Done”

At completion of M5 (or M6):

- `server/internal/websocket/socketio.go` is significantly smaller and mostly
  wiring.
- Each event handler is in `server/internal/websocket/handlers/*` with unit
  tests for the critical branches.
- The wire schema is the single source of truth (`protocol/wire`) for requests
  and ACKs.

