// Package schemas holds every type and interface shared across Bifrost Bench.
//
// It mirrors core/schemas in Bifrost: implementation packages depend on these
// types and interfaces, never on each other. The JSON field names here are the
// on-disk and over-the-wire contract (docs/DESIGN.md section 16); the
// TypeScript types in ui/lib/types/bench.ts mirror them and change with them.
package schemas

import "context"

// Part is implemented by every extension. Name and version are written into
// the run manifest so results from different part versions are never compared
// silently.
type Part interface {
	GetName() string
	GetVersion() string
}

// Logger is the logging interface used by library code. It has the same shape
// as Bifrost's schemas.Logger subset the bench needs.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NoopLogger discards everything. Useful in tests.
type NoopLogger struct{}

// Debug implements Logger.
func (NoopLogger) Debug(string, ...any) {}

// Info implements Logger.
func (NoopLogger) Info(string, ...any) {}

// Warn implements Logger.
func (NoopLogger) Warn(string, ...any) {}

// Error implements Logger.
func (NoopLogger) Error(string, ...any) {}

// SuiteLoader reads a benchmark pack and its lock from one kind of source.
type SuiteLoader interface {
	Part
	// Load resolves ref (for example "coding-core@1.0.0" or a folder path).
	Load(ctx context.Context, ref string) (*Suite, error)
}
