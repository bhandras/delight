# Gomobile notes (DelightSDK)

We ship the Delight Go SDK to iOS via `gomobile bind` as an `.xcframework`.

This comes with a few sharp edges that are easy to miss, and (unfortunately)
can crash the iOS app in ways that look like “random SIGABRT” with very little
Swift-side information.

## The big one: don’t return `string` / `[]byte` to mobile

`gomobile bind` uses cgo and generates C/ObjC wrappers for exported Go
functions/methods. The generated C ABI uses packed argument/return structs.

If an exported Go function returns a **pointer-bearing value** (notably `string`
or `[]byte`) through those packed structs, the destination can be **unaligned**.
On arm64 this can trip the Go write barrier and crash with:

```
fatal error: bulkBarrierPreWrite: unaligned arguments
```

We hit this on iOS with:

```
_cgoexp_*_proxysdk_Client_GetSessionMessages
```

### What we do instead

For gomobile builds we only expose **Buffer-based** APIs that do not return Go
`string`/`[]byte` across the boundary.

- Go side returns `*sdk.Buffer`
- Swift allocates `Data` and calls `Buffer.CopyTo(...)`
- Swift then decodes UTF-8 / JSON from that `Data`

See:

- `cli/sdk/mobile_buffer.go`
- `cli/sdk/mobile_keypair_buffers.go`
- `ios/DelightHarness/SDKBridge.swift` (`stringFromBuffer(_:)`)

## We also avoid exporting string-returning APIs on iOS/Android

Even if the iOS app *intends* to call the safe `*Buffer` methods, having the
old string-returning methods still exported makes it easy to accidentally call
them (or keep an old compiled binary that still does).

So we make the string-returning APIs **desktop-only** using build tags:

```go
//go:build !ios && !android
```

Files:

- `cli/sdk/sdk_desktop_api.go`
- `cli/sdk/keypair_desktop.go`
- `cli/sdk/logserver_desktop.go`

This ensures the iOS `.xcframework` header only exposes Buffer methods.

## Regression guard

We have a test that checks the generated ObjC header and fails if any of the
unsafe string-returning APIs show up again:

- `cli/sdk/mobile_header_test.go`

Note: it skips if the xcframework hasn’t been built yet (`make ios-sdk`).

## Practical guidance when adding new SDK APIs

When adding a new SDK function/method that needs to be called from iOS:

1. **Do not** export `func (...) (...) string` or `[]byte` for iOS/Android.
2. Prefer `(...)(*Buffer, error)` and return `newBufferFromString(...)`.
3. If you need binary data, return it as `*Buffer` as well.
4. Keep the “desktop string API” (if you still want it) in a `//go:build !ios && !android` file.

