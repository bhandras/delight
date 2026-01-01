package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Level is the verbosity threshold used by the logger.
//
// Lower values are more verbose.
type Level int

const (
	// LevelTrace enables extremely verbose logs (protocol events, FSM inputs, etc).
	LevelTrace Level = iota
	// LevelDebug enables verbose logs intended for debugging.
	LevelDebug
	// LevelInfo enables informational logs (default).
	LevelInfo
	// LevelWarn enables only warnings and errors.
	LevelWarn
	// LevelError enables only error logs.
	LevelError
)

// String returns the normalized string representation of the log level.
func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// ParseLevel parses a log level string into a Level.
func ParseLevel(raw string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return LevelInfo, nil
	case "trace":
		return LevelTrace, nil
	case "debug":
		return LevelDebug, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("invalid log level %q (expected trace, debug, info, warn, or error)", raw)
	}
}

type state struct {
	mu     sync.RWMutex
	level  Level
	logger *log.Logger
}

var global = state{
	level:  LevelInfo,
	logger: log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds),
}

// SetOutput replaces the writer used by the global logger.
func SetOutput(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	global.logger.SetOutput(w)
}

// SetFlags sets the underlying log flags used for all output.
func SetFlags(flags int) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.logger.SetFlags(flags)
}

// SetLevel sets the global log level threshold.
func SetLevel(level Level) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.level = level
}

// Enabled reports whether a level would be emitted by the current configuration.
func Enabled(level Level) bool {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return level >= global.level
}

// logf emits a formatted line when the given level is enabled.
func logf(level Level, format string, args ...any) {
	global.mu.RLock()
	enabled := level >= global.level
	l := global.logger
	global.mu.RUnlock()
	if !enabled {
		return
	}

	// We intentionally avoid fmt.Sprintf here so that formatting is not done when
	// the log level is disabled.
	l.Printf(strings.ToUpper(level.String())+" "+format, args...)
}

// Tracef logs at TRACE level.
func Tracef(format string, args ...any) {
	logf(LevelTrace, format, args...)
}

// Debugf logs at DEBUG level.
func Debugf(format string, args ...any) {
	logf(LevelDebug, format, args...)
}

// Infof logs at INFO level.
func Infof(format string, args ...any) {
	logf(LevelInfo, format, args...)
}

// Warnf logs at WARN level.
func Warnf(format string, args ...any) {
	logf(LevelWarn, format, args...)
}

// Errorf logs at ERROR level.
func Errorf(format string, args ...any) {
	logf(LevelError, format, args...)
}
