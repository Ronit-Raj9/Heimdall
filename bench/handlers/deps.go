// Package handlers serves the bench HTTP API under /api/bench (DESIGN.md
// 16.7). Each file is one area: a struct with injected dependencies and a
// RegisterRoutes method, as in transports/bifrost-http/handlers.
//
// Handlers never import the engine, store or verdict packages. They depend on
// the small consumer-side interfaces below, which cmd/bifrost-bench satisfies
// with the real implementations. A nil dependency answers 503, so a recorded
// or synthetic server can run without a launcher.
package handlers

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// APIPrefix is the path prefix of every bench API route.
const APIPrefix = "/api/bench"

// RunView is one run as the API reads it from the store's in-memory index.
type RunView struct {
	Slot     schemas.Slot `json:"slot"`
	Run      schemas.Run  `json:"run"`
	Sealed   bool         `json:"sealed"`
	SealHash string       `json:"seal_hash,omitempty"`
}

// ReadIndex is the read side of the run folder. It is rebuilt from disk at
// start and never written through the API.
type ReadIndex interface {
	Experiments() []string
	Manifest(expID string) (*schemas.Manifest, bool)
	Runs(expID string) []RunView
	Run(runID string) (*RunView, bool)
	Pair(pairID string) []RunView
	// RunFile returns one file of a run folder. Handlers only ask for names
	// on their allowlist; implementations must still refuse path separators.
	RunFile(runID, name string) ([]byte, error)
}

// Analyzer computes the analysis of an experiment over its sealed runs with
// the chosen grade source ("original" or a regrade id).
type Analyzer interface {
	Analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error)
}

// Launcher plans, creates and starts experiments. Start receives a
// request-scoped context: the experiment's lifetime must not depend on it.
type Launcher interface {
	// Plan resolves an experiment into its manifest. Nothing runs.
	Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error)
	Create(ctx context.Context, m *schemas.Manifest) error
	Start(ctx context.Context, expID string) error
	// Events subscribes to an experiment's live events. The channel closes
	// when the experiment ends; the func unsubscribes.
	Events(expID string) (<-chan schemas.LiveEvent, func())
}

// ActivityReporter is optionally implemented by a Launcher. Active reports
// whether the experiment is executing in this process, so an experiment
// between two slots, or analyzing after its last one, still reads as running.
type ActivityReporter interface {
	Active(expID string) bool
}

// AnalysisIndex is optionally implemented by a ReadIndex. LatestAnalysis
// returns the id of the newest analysis written for the experiment, if any.
type AnalysisIndex interface {
	LatestAnalysis(expID string) (string, bool)
}

// CatalogSource returns every registered part.
type CatalogSource interface {
	Catalog() *schemas.Catalog
}

// Auditor turns a published per-task table into a verdict: as CSV with the
// header task,a_pass,a_n,b_pass,b_n, or as the JSON request.
type Auditor interface {
	AuditCSV(ctx context.Context, csv []byte) (*schemas.Verdict, error)
	Audit(ctx context.Context, req *schemas.AuditRequest) (*schemas.Verdict, error)
}

// DoctorFunc runs the preflight checks.
type DoctorFunc func(ctx context.Context) []schemas.DoctorCheck
