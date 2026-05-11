# Push Notifications (Encrypted APNs via gorush)

This document explains how Delight push notifications work end-to-end, what
you need to configure, and how to verify the flow.

## Architecture

Delight push delivery is split across all three components:

1. The CLI decides when to notify (`turn-complete`, `attention`).
2. The CLI encrypts push metadata locally with a key derived from the account
   master key (`"Delight Push"` + `"notifications"` derivation path).
3. The CLI sends ciphertext to the server via
   `POST /v1/push-notifications`.
4. The server looks up APNs tokens for the authenticated account from
   `POST /v1/push-tokens` registrations.
5. The server forwards the ciphertext to gorush.
6. gorush sends the APNs push to iOS devices.
7. The iOS app extracts and decrypts `delight.ciphertext` locally, then shows
   a local notification with detailed context.

The server and gorush only see opaque ciphertext and routing metadata
(device token + APNs topic).

## Why You Sometimes See Two Notifications

The gorush payload includes:

- A generic visible APNs alert (`"Delight update"`, `"Open Delight for details."`)
- Encrypted data under `delight.ciphertext`

Then the app may post a second, detailed local notification after decrypting
the ciphertext.

This means two banners can happen for the same event:

- Generic APNs banner from the push gateway
- Detailed local banner from decrypted app-side payload

If iOS does not run the background callback (or decrypt fails), you may see
only the generic one.

## Setup Checklist

### 1. Apple Developer setup

- Enable Push Notifications on the app ID for your bundle id.
- Create an APNs Auth Key (`.p8`), and record `key_id` and `team_id`.

### 2. iOS app setup

- Bundle id in Xcode must match `DELIGHT_PUSH_TOPIC`.
- App entitlements must include `aps-environment`.
- Debug uses `development` (`ios/DelightApp/DelightApp.entitlements`).
- Release/TestFlight uses `production`
  (`ios/DelightApp/DelightApp.Release.entitlements`).

### 3. Server + gorush setup

- Configure the server push backend:
  - `DELIGHT_PUSH_BACKEND=gorush`
  - `DELIGHT_GORUSH_URL=http://gorush:8088/api/push`
  - `DELIGHT_PUSH_TOPIC=<your bundle id>`
- Configure gorush with your APNs key/team/key id.

### 4. CLI setup

- Use a CLI build that has an authenticated account and master key.
- Push is enabled by default (`push.mode = "auto"`) unless disabled in
  `~/.delight/config.toml` or with `--push=off`.

### 5. Device registration

- Run the iOS app on a real device, allow notifications, and authenticate.
- The app uploads APNs token(s) to `POST /v1/push-tokens`.

## gorush Configuration

If you use the bundled deployment helper:

```bash
./deploy/delight-server.sh init
${EDITOR:-vi} ./deploy-data/.env
${EDITOR:-vi} ./deploy-data/gorush/config.yml
./deploy/delight-server.sh up
```

Minimal working `deploy-data/gorush/config.yml` for Delight:

```yaml
core:
  enabled: true
  port: "8088"
  mode: "release"
  sync: true

api:
  push_uri: "/api/push"

android:
  enabled: false

huawei:
  enabled: false

ios:
  enabled: true
  key_path: "/data/apns.p8"
  key_type: "p8"
  key_id: "YOUR_APNS_KEY_ID"
  team_id: "YOUR_APPLE_TEAM_ID"
  production: true
```

Settings that must match Delight server expectations:

- `core.port` must stay `8088` when server uses
  `DELIGHT_GORUSH_URL=http://gorush:8088/api/push`.
- `api.push_uri` must stay `/api/push` for that same URL.
- `ios.enabled` must be `true`.
- `ios.key_path` must point to your mounted `.p8` file.
- `ios.key_type` must be `p8`.
- `ios.key_id` and `ios.team_id` must be valid Apple values for your APNs key.

Set `ios.production` based on app build type:

- `false`: debug/development builds from Xcode
- `true`: TestFlight/App Store builds

`android.enabled` and `huawei.enabled` are explicitly disabled because Delight
currently sends iOS-only notifications.

Place the APNs key at `deploy-data/gorush/apns.p8` (the compose file mounts it
read-only to `/data/apns.p8` inside the gorush container).

## CLI Controls

The CLI encrypted push knobs live in `~/.delight/config.toml`:

```toml
[push]
mode = "on"
events = ["turn-complete"]
cooldown_sec = 30
```

Use `--push`, `--push-events`, and `--push-cooldown-sec` for one-off CLI
overrides.

## Verify End-to-End Delivery

### 1. Confirm token registration

- Start the iOS app, authenticate, and allow notifications.
- Server should store a row in `account_push_tokens`.

### 2. Trigger an event

- Complete a turn in CLI or trigger a permission request (`attention`).

### 3. Check logs

- Server logs should show push send attempts and counts.
- gorush logs should show APNs handoff results.

### 4. Confirm iOS behavior

- Generic banner means APNs path is alive.
- Detailed banner means decrypt and local notification path is alive.

## Common Failures

- `push notifications not configured`
  - Server push backend was not configured (`DELIGHT_PUSH_*` missing).
- `no registered push tokens for account` (HTTP 409)
  - Device token has not been uploaded for this authenticated account.
- APNs `BadDeviceToken` or no delivery
  - Topic mismatch, wrong APNs environment (`production` flag), or stale token.
- Only generic notifications appear
  - App did not process/decrypt background payload for that push.
