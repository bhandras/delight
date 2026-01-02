package logger

import (
	"io"

	protocollogger "github.com/bhandras/delight/shared/logger"
)

// Level is the verbosity threshold used by the logger.
//
// Lower values are more verbose.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
type Level = protocollogger.Level

const (
	// LevelTrace enables extremely verbose logs (protocol events, FSM inputs, etc).
	//
	// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
	LevelTrace = protocollogger.LevelTrace
	// LevelDebug enables verbose logs intended for debugging.
	//
	// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
	LevelDebug = protocollogger.LevelDebug
	// LevelInfo enables informational logs (default).
	//
	// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
	LevelInfo = protocollogger.LevelInfo
	// LevelWarn enables only warnings and errors.
	//
	// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
	LevelWarn = protocollogger.LevelWarn
	// LevelError enables only error logs.
	//
	// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
	LevelError = protocollogger.LevelError
)

// ParseLevel parses a log level string into a Level.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func ParseLevel(raw string) (Level, error) {
	return protocollogger.ParseLevel(raw)
}

// SetOutput replaces the writer used by the global logger.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func SetOutput(w io.Writer) {
	protocollogger.SetOutput(w)
}

// SetFlags sets the underlying log flags used for all output.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func SetFlags(flags int) {
	protocollogger.SetFlags(flags)
}

// SetLevel sets the global log level threshold.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func SetLevel(level Level) {
	protocollogger.SetLevel(level)
}

// Enabled reports whether a level would be emitted by the current configuration.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func Enabled(level Level) bool {
	return protocollogger.Enabled(level)
}

// Tracef logs at TRACE level.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func Tracef(format string, args ...any) {
	protocollogger.Tracef(format, args...)
}

// Debugf logs at DEBUG level.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func Debugf(format string, args ...any) {
	protocollogger.Debugf(format, args...)
}

// Infof logs at INFO level.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func Infof(format string, args ...any) {
	protocollogger.Infof(format, args...)
}

// Warnf logs at WARN level.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func Warnf(format string, args ...any) {
	protocollogger.Warnf(format, args...)
}

// Errorf logs at ERROR level.
//
// Deprecated: Use `github.com/bhandras/delight/shared/logger`.
func Errorf(format string, args ...any) {
	protocollogger.Errorf(format, args...)
}
