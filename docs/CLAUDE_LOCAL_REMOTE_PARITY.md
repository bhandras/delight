# Claude Local/Remote Mode Parity (Happy → Delight)

This document tracks the work required to make Delight CLI + server + iOS app
behave like Happy with respect to **Claude local vs remote mode**, including:

- automatic handoff when the phone sends a message
- manual control switch (phone ↔ desktop)
- clear “who controls Claude” UI in iOS
- permission prompts flowing to the phone in remote mode
- durable “permission required” state across reconnects

## Glossary / mental model

### Modes (per session)

- **Local mode** (“desktop controls”)
  - Claude runs in interactive TUI (PTY).
  - Tool approvals happen inside the TUI.
  - Phone is a viewer; if the phone sends input we should hand off.
- **Remote mode** (“phone controls”)
  - Claude runs in SDK stream-json mode with `--input-format stream-json` and
    `--permission-prompt-tool stdio`.
  - Tool permission prompts appear as structured `control_request` events and
    are routed to the phone.
  - Phone input drives Claude.

### Source-of-truth control bit

The UI should consistently reflect:

- `agentState.controlledByUser == true` → **Desktop controls**
- `agentState.controlledByUser == false` → **Phone controls**

This matches Happy’s `agentState.controlledByUser` contract.

## Parity requirements (Happy behavior)

### A) Automatic handoff on phone input

If the session is in Local mode and the phone sends a message:

1. CLI detects inbound user text from server.
2. CLI switches the session to Remote mode (kills the local TUI).
3. CLI forwards the user message to the remote runner.
4. CLI updates `agentState.controlledByUser=false` immediately.

Happy reference: `happy-cli/src/claude/claudeLocalLauncher.ts` uses a message
queue callback to trigger `doSwitch()` whenever a phone message arrives.

### B) Manual switch (from phone)

iOS should expose:

- **Take Control** → RPC `{mode:"remote"}`
- **Return Control to Desktop** → RPC `{mode:"local"}`

Happy reference: `rpcHandlerManager.registerHandler('switch', doSwitch)` in both
local and remote launchers.

### C) Manual switch (from desktop)

When in Remote mode, a desktop action should switch back to Local. (This is
Phase 5; not part of this doc’s initial implementation scope.)

### D) Permission prompts

- In Remote mode, permission prompts must be delivered to the phone as
  `permission-request` events and correlated to a `sessionId:permission` RPC.
- In Local mode, the phone should *not* get permission modals; approvals are on
  desktop.

Happy reference: `happy-cli/src/claude/sdk/query.ts` handling `control_request →
control_response` plus durable request bookkeeping.

### E) iOS: show “who controls”

iOS terminal detail view should show a clear control banner:

- Controlled by Desktop → hint: approvals appear on desktop; CTA “Take Control”
- Controlled by Phone → hint: approvals appear here; CTA “Return Control…”

## Implementation plan (Delight Phase 1–4)

This repo already has a Claude remote-mode bridge:

- `delight/cli/scripts/claude_remote_bridge.cjs` (spawns Claude Code with
  stream-json + stdio permission prompts)
- `delight/cli/internal/claude/remote.go` (Go wrapper + permission callback)

The remaining parity work is primarily about **control semantics**, **handoff**,
and **durable permission state**.

### Phase 1 — Remote runner parity (SDK stream-json)

Goal: Ensure remote mode is true stream-json + `control_request` permissions.

- Confirm remote runner uses:
  - `--output-format stream-json --input-format stream-json`
  - `--permission-prompt-tool stdio`
  - optional `--resume <sessionId>`
- Ensure the Go layer routes:
  - `control_request` → phone permission request
  - phone RPC response → `control_response` back to Claude

### Phase 2 — Session mode semantics + handoff

Goal: Match Happy’s “phone message switches to remote” behavior.

- On inbound user message:
  - if current mode is Local and message origin is mobile (not local echo),
    call `SwitchToRemote()`, then forward message to remote runner.
  - if current mode is Remote, forward to remote runner normally.
  - avoid re-injecting “echoes” of locally-authored user messages.

### Phase 3 — Durable permission state (`agentState.requests`)

Goal: Make “permission required” survive reconnects and be visible in iOS UI.

- Extend encrypted agent state to include:
  - `requests: { [requestId]: { toolName, input, createdAt } }`
- On permission request:
  - add to `agentState.requests`
  - emit ephemeral `permission-request` for immediate UI
- On permission response:
  - remove from `agentState.requests`
  - (optional) add to `completedRequests`

### Phase 4 — Manual switch controls (phone ↔ desktop)

Goal: Expose explicit control toggles in iOS.

- Terminal detail banner shows current controller and a CTA:
  - Desktop controls → “Take Control” (RPC switch → remote)
  - Phone controls → “Return Control to Desktop” (RPC switch → local)

## Acceptance tests / checklist

1. Start on desktop (Local mode)
   - Claude TUI works normally.
   - iOS shows “Controlled by Desktop”.
2. Send message on phone
   - CLI switches to Remote mode automatically.
   - iOS shows “Controlled by Phone”.
3. Permission request while Remote
   - iOS receives modal + can allow/deny.
   - If app reconnects, terminal list shows “permission required”.
4. Manual switch
   - iOS can force switch both directions via RPC.

