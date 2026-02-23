# Build System And TestFlight

This document describes the root `Makefile` workflow for building Delight and
uploading iOS builds to TestFlight.

## Root Make Targets

Common targets:

- `make test`
  - Runs Go tests in `cli`, `webclient`, and `server`.
- `make lint`
  - Runs `golangci-lint` for `shared`, `cli`, and `server`.
- `make ios-run`
  - Builds SDK + app, boots simulator, installs, launches app.
- `make ios-test`
  - Runs iOS tests in simulator.
- `make docker-build`
  - Builds the server Docker image (`server/Dockerfile`).

iOS release/upload chain:

- `make ios-signing-resolve`
- `make ios-release-archive`
- `make ios-export-ipa`
- `make ios-testflight-upload`

Each target depends on the previous one; running
`make ios-testflight-upload` executes the full chain.

## iOS Prerequisites

- Xcode + command line tools
- A valid Apple signing identity and provisioning profile
- App Store Connect API key (`.p8`) with upload permissions
- Correct bundle id and push entitlements in the Xcode project

The iOS SDK framework is built from Go sources via:

```bash
make ios-sdk
```

This runs `./cli/scripts/build_ios_sdk.sh` and produces
`cli/build/DelightSDK.xcframework`.

## TestFlight Upload (Manual Signing)

From repo root:

```bash
export IOS_EXPORT_SIGNING_STYLE=manual
export IOS_PROFILE_NAME=delight
export ASC_API_KEY_ID=...
export ASC_API_ISSUER_ID=...
export ASC_API_KEY_PATH=/absolute/path/AuthKey_XXXX.p8
export ASC_APPLE_ID=1234567890

make ios-testflight-upload
```

What these variables mean:

- `IOS_EXPORT_SIGNING_STYLE`
  - `manual` or `automatic` (default is `automatic`).
- `IOS_PROFILE_NAME`
  - Required for manual signing.
  - Can be profile name, UUID, filename, stem, or full path.
- `ASC_API_KEY_ID`
  - App Store Connect API key ID.
- `ASC_API_ISSUER_ID`
  - App Store Connect API issuer ID.
- `ASC_API_KEY_PATH`
  - Path to the `.p8` private key file.
- `ASC_APPLE_ID`
  - Numeric App Store Connect app ID (not Apple ID email).

Optional:

- `ASC_PUBLIC_ID`
  - Provider/public id if your account requires it.
- `ASC_OUTPUT_FORMAT`
  - `normal` (default) or other `altool` output format.

## Build Number Behavior

By default uploads auto-bump build number:

- `IOS_AUTO_BUMP_BUILD=1` (default)
- Build number becomes current Unix timestamp

This avoids duplicate `CFBundleVersion` upload failures.

Controls:

- Set explicit build number: `IOS_BUILD_NUMBER=<number>`
- Disable auto-bump: `IOS_AUTO_BUMP_BUILD=0`

## Important iOS Make Variables

Frequently used overrides:

- `SIMULATOR_DEVICE` (default: `iPhone 16`)
- `CONFIGURATION` (default: `Debug`)
- `DERIVED_DATA` (default: `/tmp/delight-ios`)
- `IOS_PROJECT` (default: `ios/DelightApp.xcodeproj`)
- `IOS_SCHEME` (default: `DelightApp`)
- `IOS_RELEASE_CONFIGURATION` (default: `Release`)

## Release Entitlements

Release builds archive with the Release entitlements file:

- `ios/DelightApp/DelightApp.Release.entitlements`

That file sets:

- `aps-environment = production`

This is required for TestFlight/APNs production push delivery.

## Troubleshooting

- `IOS_PROFILE_NAME is required when IOS_EXPORT_SIGNING_STYLE=manual`
  - Set `IOS_PROFILE_NAME` to a resolvable profile reference.
- `selected profile looks like a Development profile (get-task-allow=true)`
  - Use an App Store distribution profile for TestFlight.
- `ASC_* is required`
  - Export missing App Store Connect variables before upload.
- No IPA exported
  - Check archive success under `$(DERIVED_DATA)/archive` and export logs.

