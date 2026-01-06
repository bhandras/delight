# Testing TODO (Long-Form)

This file tracks the recommended test coverage work for Delight, focusing on
correctness, determinism, and isolating agent-engine integrations (Codex/Claude/
ACP) from protocol/core state-machine behavior.

## SessionActor invariants (cli/internal/session/actor)

- [x] Ack ordering: reply implies state applied (via runtime effects)
- [x] Durable progress: `ui.event` ephemerals also persisted as messages
- [x] Reconnect persistence: on `WSConnected`, schedule re-persist of AgentState
- [x] Dedupe: local-id echo suppression window
- [x] Dedupe: suppress user echoes from remote injection
- [x] Failure recovery: remote exit while starting falls back to local injection
- [x] Failure recovery: remote exit while running falls back to local start
- [x] Persist retry: version mismatch retry
- [x] Persist retry: non-mismatch failure while online re-arms debounce

## Runtime↔Engine integration contract (cli/internal/session/actor runtime)

- [x] Takeback watcher: safe, deterministic tests (no real `/dev/tty`)
- [x] Takeback watcher: space-space triggers takeback event
- [x] Takeback watcher: Ctrl+C byte triggers shutdown event
- [x] Engine wiring: start/stop local and remote with stub engine
- [x] Engine wiring: ensureEngine replaces mismatched engine
- [x] Terminal printer: basic UI event printing
- [x] Terminal printer: user-text record printing

## Manager behavior (cli/internal/session)

- [x] Socket lifecycle bridging:
  - `OnConnect` -> enqueue `WSConnected`
  - `OnDisconnect` -> enqueue `WSDisconnected`
  - terminal socket connect/disconnect -> enqueue terminal events
- [x] Session create/update hydration:
  - Hydrate `dataEncryptionKey` from `new-session` update payload
- [x] Encrypted inbound user messages:
  - decrypt + parse user text record
  - enqueue `InboundUserMessage` with correct meta/localID
- [x] Agent config apply:
  - `SetAgentConfig` enqueues config update + waits for persistence

## Auth / token refresh invariants (cli/internal/cli, websocket)

- [x] Auth refresh: can mint a JWT via `/v1/auth/challenge` + `/v1/auth` (httptest)
- [x] Websocket client: token refresher callback invoked on auth-like connect errors

## In-process "FakeMobile" end-to-end (no subprocess, deterministic)

- [x] Phone -> CLI: inbound user message switches to remote and responds (fake engine)
- [x] Phone -> CLI: permission await + decision round trip (fake engine)
- [x] CLI -> server: ui.event persisted as message
- [x] Reconnect flow:
  - disconnect
  - tool prompt created while disconnected
  - reconnect schedules persist
