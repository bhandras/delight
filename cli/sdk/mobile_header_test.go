package sdk

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMobileObjCHeaderDoesNotExposeStringReturningAPIs(t *testing.T) {
	// This is a regression test for gomobile/cgo crashes like:
	//   fatal error: bulkBarrierPreWrite: unaligned arguments
	//
	// We avoid exporting pointer-bearing return values like `string` / `[]byte` to
	// the mobile bindings, and instead expose Buffer-based APIs.

	header := filepath.Join("..", "build", "DelightSDK.xcframework", "ios-arm64_x86_64-simulator", "DelightSDK.framework", "Headers", "Sdk.objc.h")
	b, err := os.ReadFile(header)
	if err != nil {
		t.Skipf("objc header not found (%s): %v (run `make ios-sdk`)", header, err)
	}

	disallowed := [][]byte{
		[]byte("authWithKeyPair:(NSString"),
		[]byte("getSessionMessages:(NSString"),
		[]byte("listSessions:(NSError"),
		[]byte("listMachines:(NSError"),
		// NOTE: We only disallow the string-returning StartLogServer API. The
		// Buffer-based `startLogServerBuffer` is allowed (and should remain
		// available) for mobile.
		[]byte("startLogServer:("),
	}
	for _, needle := range disallowed {
		require.Falsef(t, bytes.Contains(b, needle), "mobile ObjC header unexpectedly contains %q; string-returning APIs should not be exported in gomobile builds", needle)
	}
}
