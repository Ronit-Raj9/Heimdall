package utils

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// StdLogger writes one timestamped line per message. It implements
// schemas.Logger for the CLI and the server.
type StdLogger struct {
	mu    sync.Mutex
	w     io.Writer
	debug bool
}

// NewStdLogger returns a logger writing to w. Debug lines are written only
// when debug is true.
func NewStdLogger(w io.Writer, debug bool) *StdLogger {
	return &StdLogger{w: w, debug: debug}
}

// Debug implements schemas.Logger.
func (l *StdLogger) Debug(msg string, args ...any) {
	if l.debug {
		l.write("DEBUG", msg, args)
	}
}

// Info implements schemas.Logger.
func (l *StdLogger) Info(msg string, args ...any) { l.write("INFO", msg, args) }

// Warn implements schemas.Logger.
func (l *StdLogger) Warn(msg string, args ...any) { l.write("WARN", msg, args) }

// Error implements schemas.Logger.
func (l *StdLogger) Error(msg string, args ...any) { l.write("ERROR", msg, args) }

func (l *StdLogger) write(level, msg string, args []any) {
	line := msg
	if len(args) > 0 {
		line = fmt.Sprintf(msg, args...)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %-5s %s\n", time.Now().Format("15:04:05.000"), level, line)
}
