# Terminal UI Agent Settings (iOS ↔ CLI)

This document describes how the iOS harness terminal UI should expose agent
configuration (model, reasoning effort, and permission level), and how those
settings flow through the Delight stack (iOS → SDK/RPC → CLI session manager →
agent engine).

## Goals

- Make terminal sessions feel like a dedicated full-screen view on iOS.
  - The transcript should use most of the vertical space (hide the tab bar in
    the terminal detail view).
- Expose two small controls in the terminal session UI:
  - `gearshape` opens a "Model & Effort" sheet.
  - `exclamationmark.circle` opens a "Permissions" sheet.
- Settings are agent properties:
  - Durable (persisted in session `agentState` JSON).
  - Applied by the agent engine (Codex/Claude/ACP) rather than being ad-hoc
    per-message metadata.
- Settings changes are only allowed when the phone controls the session
  (remote mode).

## Non-goals

- Redesign the global app navigation; the app can keep a root `TabView` for
  Terminals/Settings.
- Fetching models directly from upstream CLIs (Codex/Claude). Delight treats
  the local agent engine as the source of truth and exposes a capabilities RPC
  for the iOS UI.

## UI Design (iOS)

### Terminal detail layout

- Terminal detail is `TerminalDetailView` in `ios/DelightHarness/TerminalUI.swift`.
- When showing `TerminalDetailView`, hide the bottom tab bar so the transcript
  occupies more height:
  - iOS 16+: `toolbar(.hidden, for: .tabBar)`

### Controls

- The session UI shows two compact icon buttons near the bottom controls:
  - Gear: model + reasoning effort.
  - Exclamation: permission mode.
- Both buttons are disabled unless the phone controls the session:
  - The same gate used for enabling the message composer:
    - session state is `remote`, and `controlledByUser == false`.
- When disabled, show explanatory affordance text (e.g. "Take Control to change
  settings") rather than silently doing nothing.

### Model list fallback (Codex)

The iOS UI does not hardcode model lists. It queries the session-scoped
capabilities endpoint and renders whatever the agent engine reports.

### Reasoning effort (Codex)

Codex supports a "reasoning effort" knob. The canonical values are:

- `low`
- `medium` (default)
- `high`
- `xhigh` (available on `gpt-5.1-codex-max` and `gpt-5.2`)

These values correspond to Codex config `model_reasoning_effort`.

The UI includes effort only for agents that report support for it.

Note: `gpt-5.1-codex-mini` only supports `medium` and `high`.

### Permissions

Permissions are exposed as agent-engine values (agent-specific):

- Codex:
  - `default`
  - `read-only`
  - `safe-yolo`
  - `yolo`
- Claude Code:
  - `default`
  - `plan`
  - `acceptEdits`
  - `bypassPermissions`

## Data Model

### Durable agent properties (persisted)

The CLI persists an agent state JSON blob (plaintext) alongside sessions so that
mobile clients can recover after reconnect.

We extend `types.AgentState` to include:

- `agentType` (e.g. `codex`, `claude`, `acp`, `fake`)
- `model` (optional; empty implies engine default)
- `reasoningEffort` (optional; Codex-specific today)
- `permissionMode` (optional; empty implies `default`)

iOS decodes these fields from session summaries and uses them to display the
current selection and seed the picker sheets.

### Engine configuration contract

We keep a typed configuration surface area and allow agent-specific extras:

- Core keys:
  - model
  - permissionMode
- Codex extra key:
  - reasoningEffort (mapped to `model_reasoning_effort`)

The implementation may carry this as a typed struct plus an extras map (to avoid
spreading Codex-specific fields everywhere).

## Transport / RPC

We add two session-scoped RPC endpoints:

- Method: `<sessionID>:agent-config`
- Request payload:
  - `model` (optional)
  - `permissionMode` (optional)
  - `reasoningEffort` (optional)
- Response payload:
  - `{success: true}` on success

- Method: `<sessionID>:agent-capabilities`
- Request payload: `{}` (empty)
- Response payload:
  - `{success: true, agentType, capabilities, desiredConfig, effectiveConfig}`
  - `capabilities` contains lists for:
    - `models`
    - `reasoningEfforts`
    - `permissionModes`

### Remote-mode enforcement

This RPC must be rejected unless the session is phone-controlled (remote mode).
This is enforced both in the iOS UI (disabled buttons) and in the CLI handler.

## Application semantics

- Applying config is best-effort and engine-specific:
  - Remote-mode Codex should apply immediately (by reconfiguring the remote
    Codex MCP client session config for subsequent turns).
  - Engines that do not support a setting may ignore it but must not crash.
- The CLI persists the config values into `agentState` so the mobile UI and
  other clients can display them consistently.

## Validation

- iOS builds: `xcodebuild -project ios/DelightHarness.xcodeproj -scheme DelightHarness ... build`
- CLI builds/tests: run the package-level unit tests affected by new RPC and
  agent state persistence logic.
