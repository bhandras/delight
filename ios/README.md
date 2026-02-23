# Delight iOS App

SwiftUI app for pairing with a Delight CLI terminal and viewing/controlling the
session transcript.

This app is experimental and intended primarily for personal use.

## Run in Simulator (Recommended)

From the repo root:

```bash
make ios-run
```

This builds the Go SDK (`DelightSDK.xcframework`), boots a simulator if needed,
installs the app, and launches it.

Delight currently targets iOS 18+.

## Build SDK (Manual)

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
./cli/scripts/build_ios_sdk.sh
```

This produces `cli/build/DelightSDK.xcframework`, which the Xcode project
references.

## Open in Xcode

```bash
open ios/DelightApp.xcodeproj
```

Select a simulator or device, then Run.

## TestFlight Upload

From the repo root:

```bash
export IOS_EXPORT_SIGNING_STYLE=manual
export IOS_PROFILE_NAME=delight  # profile name, UUID, filename, or full path
export ASC_API_KEY_ID=...
export ASC_API_ISSUER_ID=...
export ASC_API_KEY_PATH=...
export ASC_APPLE_ID=...

make ios-testflight-upload
```

Notes:
- `make ios-testflight-upload` now resolves the provisioning profile metadata
  automatically and pins archive signing to the profile UUID/certificate.
- By default, archive build number auto-bumps to the current unix timestamp to
  avoid duplicate `CFBundleVersion` upload failures.
- Override build number explicitly with `IOS_BUILD_NUMBER=<number>`.
- Disable auto-bump with `IOS_AUTO_BUMP_BUILD=0`.
- For full release/upload variable docs, see `docs/BUILD_AND_RELEASE.md`.

## App Flow

1. Set `Server URL` (default `http://localhost:3005`).
2. Pair with a terminal via the CLI `auth` flow, or add a terminal from the UI.
3. Connect and view sessions/messages.

Notes:
- The SDK expects base64 master key (32 bytes).
- The QR URL should look like `delight://terminal?<base64url-public-key>`.
- APNs push notifications require a real iOS device and valid push signing.
- The app registers APNs tokens to `POST /v1/push-tokens` when authenticated.
- Full push setup docs: `docs/PUSH_NOTIFICATIONS.md`.

## Related Docs

- `README.md` - Repo overview
- `cli/README.md` - CLI usage and development
- `server/README.md` - Server configuration and hosting
- `docs/BUILD_AND_RELEASE.md` - Build and TestFlight pipeline details
- `docs/PUSH_NOTIFICATIONS.md` - Encrypted APNs/gorush setup and flow
