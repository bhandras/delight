# Delight iOS Harness

Minimal SwiftUI app for exercising the Delight Go SDK on simulator or device.

## Build SDK

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
./delight/cli/scripts/build_ios_sdk.sh
```

This produces `delight/cli/build/DelightSDK.xcframework`, which the Xcode project references. The Xcode project also runs the build script automatically on each build.

## Open in Xcode

```bash
open delight/ios/DelightApp.xcodeproj
```

Select a simulator or device, then Run.

## Harness flow

1. Set `Server URL` (default `http://localhost:3005`).
2. Generate keys or paste existing `Master Key`/`Token`.
3. Approve terminal QR URL (from CLI) if pairing.
4. Connect, list sessions, send messages.

Notes:
- The SDK expects base64 master key (32 bytes).
- The QR URL should look like `delight://terminal?<base64url-public-key>`.
