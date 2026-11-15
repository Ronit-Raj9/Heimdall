// Package taskhealth computes suite health (docs/suite-health.md): per task,
// whether its verifier grades the same output consistently, how much agent
// outcomes vary, how often runs end on infrastructure, and how hard the task
// is in practice compared with its declared difficulty. It reads sealed runs
// across experiments through a Source and writes nothing; quarantine is
// recorded by the suite package in a sidecar beside the lock.
//
// Two kinds of instability are kept apart in names and numbers. Verifier
// flakiness is the grader disagreeing with itself on the same output (or
// rejecting the reference): it corrupts verdicts, and it is the only signal
// auto-quarantine acts on. Outcome variance is one agent config both passing
// and failing a task across trials: that is ordinary agent behaviour, and it
// is reported, never treated as a defect.
package taskhealth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Source is the record health reads. cmd/bifrost-bench adapts the file
// store to it. Experiments lists every experiment id; SealedRuns returns the
// counted sealed runs of one, with a grade source of "" (original) or a
// regrade id; SlotAttempts returns every run of a slot, sealed or not,
// aborted_infra retries included; Regrades lists an experiment's regrades.
type Source interface {
	Experiments(ctx context.Context) ([]string, error)
	Manifest(ctx context.Context, expID string) (*schemas.Manifest, error)
	SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error)
	SlotAttempts(ctx context.Context, expID, slotID string) ([]schemas.RunRecord, error)
	Regrades(ctx context.Context, expID string) ([]Regrade, error)
}

// Regrade is one regrade of an experiment. A regrade that recomputed gates
// only (a regate) has an empty GraderVersion and says nothing about the
// verifier.
type Regrade struct {
	ID            string
	SuiteVersion  string
	GraderVersion string
}

// Options are the thresholds of one health computation. Zero fields take
// DefaultOptions.
type Options struct {
	MinRuns     int
	MinConfigs  int
	EasyAbove   float64
	HardBelow   float64
	InfraRate   float64
	TimeoutRate float64
	Now         func() time.Time
}

// DefaultOptions are the thresholds `suite health` uses unless told
// otherwise. Six scored runs over two configs is the least that can tell an
// always-pass task from a lucky streak; the 0.7 and 0.3 cut-offs split the
// pass-rate range so that medium is the widest band.
var DefaultOptions = Options{MinRuns: 6, MinConfigs: 2, EasyAbove: 0.7, HardBelow: 0.3, InfraRate: 0.1, TimeoutRate: 0.2}

// intervalLevel is the confidence of the Wilson interval, and wilsonZ its
// two-sided normal quantile.
const (
	intervalLevel = 0.95
	wilsonZ       = 1.959963984540054
)

// Compute reads every experiment of suite s from src and returns the health
// of each task of s, in suite order. quarantined is the suite's sidecar
// record (may be nil). Runs of another suite id, or of a task version other
// than the one loaded, are left out and counted in Notes, so a fixed task is
// judged on its current version only.
func Compute(ctx context.Context, src Source, s *schemas.Suite, quarantined map[string]schemas.TaskQuarantine, opts Options) (*schemas.SuiteHealth, error) {
	if src == nil || s == nil {
		return nil, errors.New("suite health: source and suite are required")
	}
	opts = withDefaults(opts)
	acc := newAccumulator(s)
	exps, err := src.Experiments(ctx)
	if err != nil {
		return nil, fmt.Errorf("suite health %s: list experiments: %w", s.ID, err)
	}
	for _, expID := range exps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, err := src.Manifest(ctx, expID)
		if err != nil {
			acc.notef("experiment %s skipped: %v", expID, err)
			continue
		}
		if m.SuiteID != s.ID {
			continue
		}
		if err := acc.readExperiment(ctx, src, m); err != nil {
			return nil, fmt.Errorf("suite health %s: experiment %s: %w", s.ID, expID, err)
		}
	}
	acc.admission()
	return acc.result(opts, quarantined), nil
}

// withDefaults fills zero thresholds from DefaultOptions.
func withDefaults(o Options) Options {
	d := DefaultOptions
	if o.MinRuns <= 0 {
		o.MinRuns = d.MinRuns
	}
	if o.MinConfigs <= 0 {
		o.MinConfigs = d.MinConfigs
	}
	if o.EasyAbove <= 0 {
		o.EasyAbove = d.EasyAbove
	}
	if o.HardBelow <= 0 {
		o.HardBelow = d.HardBelow
	}
	if o.InfraRate <= 0 {
		o.InfraRate = d.InfraRate
	}
	if o.TimeoutRate <= 0 {
		o.TimeoutRate = d.TimeoutRate
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Wilson returns the Wilson score interval for x successes in n trials at
// 95% confidence. It stays inside [0, 1] and is sensible at x = 0 and x = n,
// where the normal interval collapses to a point. n < 1 returns false.
func Wilson(x, n int) (lo, hi float64, ok bool) {
	if n < 1 || x < 0 || x > n {
		return 0, 0, false
	}
	p := float64(x) / float64(n)
	nf := float64(n)
	z2 := wilsonZ * wilsonZ
	den := 1 + z2/nf
	center := (p + z2/(2*nf)) / den
	half := wilsonZ * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf)) / den
	return math.Max(0, center-half), math.Min(1, center+half), true
}

// DifficultyLabel labels a pass rate easy, medium or hard, or unknown when
// there are fewer than the minimum scored runs or configs.
func DifficultyLabel(rate float64, runs, configs int, o Options) string {
	o = withDefaults(o)
	switch {
	case runs < o.MinRuns || configs < o.MinConfigs:
		return "unknown"
	case rate >= o.EasyAbove:
		return "easy"
	case rate <= o.HardBelow:
		return "hard"
	default:
		return "medium"
	}
}

// FlakyTasks returns the ids of tasks with verifier flakiness evidence, the
// only tasks `suite health -auto-quarantine` may quarantine.
func FlakyTasks(h *schemas.SuiteHealth) []string {
	var out []string
	for _, t := range h.Tasks {
		if t.Verifier.Flaky {
			out = append(out, t.Task)
		}
	}
	return out
}

// EvidenceLines renders a task's verifier evidence as one line each, for an
// auto quarantine record and the CLI.
func EvidenceLines(v schemas.VerifierHealth) []string {
	out := make([]string, 0, len(v.Evidence))
	for _, e := range v.Evidence {
		line := e.Kind + ": " + e.Detail
		if e.ExpID != "" {
			line += " (experiment " + e.ExpID
			if len(e.RunIDs) > 0 {
				line += ", runs " + strings.Join(e.RunIDs, ", ")
			}
			line += ")"
		}
		out = append(out, line)
	}
	return out
}

// sortedFlags returns the flag set sorted, never nil.
func sortedFlags(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
