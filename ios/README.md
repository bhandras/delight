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

## App Flow

1. Set `Server URL` (default `http://localhost:3005`).
2. Pair with a terminal via the CLI `auth` flow, or add a terminal from the UI.
3. Connect and view sessions/messages.

Notes:
- The SDK expects base64 master key (32 bytes).
- The QR URL should look like `delight://terminal?<base64url-public-key>`.

## Related Docs

- `README.md` - Repo overview
- `cli/README.md` - CLI usage and development
- `server/README.md` - Server configuration and hosting
