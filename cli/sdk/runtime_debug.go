package sdk

import "runtime/debug"

func init() {
	// Best-effort diagnostics for Go panics/fatals inside a gomobile app.
	// - "all" includes all goroutines.
	// - panic-on-fault converts some SIGSEGV/SIGBUS into a panic so our
	//   panic logging can capture a stack (it won't help for runtime "fatal error"
	//   cases like concurrent map writes).
	debug.SetTraceback("all")
	debug.SetPanicOnFault(true)
}

