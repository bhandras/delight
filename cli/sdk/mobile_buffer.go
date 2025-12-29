package sdk

import (
	"fmt"
	"unsafe"
)

// Buffer is a gomobile-safe byte container.
//
// Important: do not expose methods returning string/[]byte to gomobile callers.
// Returning pointer-bearing values (like Go strings/slices) can crash at runtime
// due to cgo/gomobile generating packed argument structs and the Go runtime
// requiring aligned write-barrier destinations.
//
// Use Len + CopyTo on the Swift side to materialize data.
type Buffer struct {
	b []byte
}

func newBufferFromString(s string) *Buffer {
	// Copy to a byte slice so Swift can read via CopyTo.
	return &Buffer{b: []byte(s)}
}

// Len returns the number of bytes held in this buffer.
func (buf *Buffer) Len() int {
	if buf == nil {
		return 0
	}
	return len(buf.b)
}

// CopyTo copies up to dstLen bytes into the memory pointed to by dstPtr.
//
// dstPtr must point to writable memory of at least dstLen bytes (e.g. from Swift
// via malloc/Data.withUnsafeMutableBytes).
//
// Returns the number of bytes written.
func (buf *Buffer) CopyTo(dstPtr int64, dstLen int) (int, error) {
	if buf == nil {
		return 0, fmt.Errorf("buffer is nil")
	}
	if dstLen < 0 {
		return 0, fmt.Errorf("dstLen must be >= 0")
	}
	if dstLen == 0 || len(buf.b) == 0 {
		return 0, nil
	}
	if dstPtr == 0 {
		return 0, fmt.Errorf("dstPtr is null")
	}
	n := len(buf.b)
	if n > dstLen {
		n = dstLen
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(dstPtr))), n)
	copy(dst, buf.b[:n])
	return n, nil
}

