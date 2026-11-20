// Package engine plans experiments and runs their slots through the fixed
// stage pipeline (DESIGN.md sections 8.2 and 15).
//
// The engine imports only schemas and utils. Every part (environment,
// adapter, probe layer, evidence source, grader, gate, metric, gateway,
// store) arrives through a schemas.Registry or a constructor argument, so
// adding or removing a part never edits this package.
package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// BenchVersion is recorded in every manifest.
const BenchVersion = "0.1.0"

// CompatArm is the arm id of compatibility slots. It is reserved: an
// experiment cannot declare an arm with this id.
const CompatArm = "compat"

// RandomizationUnit is the unit arm order is drawn for: one order per task,
// reused by every block of that task.
const RandomizationUnit = "task"

// maxAttempts is one run plus at most two retries after aborted_infra.
const maxAttempts = 3

// Event kinds sent to the live view.
const (
	EventRunStarted   = "run_started"
	EventStage        = "stage"
	EventRunSealed    = "run_sealed"
	EventRunRecovered = "run_recovered"
	EventSlotSkipped  = "slot_skipped"
	EventLeaseHeld    = "lease_held"
	EventBudgetStop   = "budget_stop"
	EventError        = "error"
	EventDone         = "done"
)

// Event is one progress update for the API's live view.
type Event struct {
	Kind    string
	SlotID  string
	RunID   string
	Stage   schemas.Stage
	Outcome schemas.Outcome
	At      time.Time
	Message string
}

// PartVersions returns "<kind>/<name>" -> version for every registered part.
// The planner writes it into the manifest so results from different part
// versions are never compared silently.
func PartVersions(reg *schemas.Registry) map[string]string {
	out := map[string]string{}
	if reg == nil {
		return out
	}
	addParts(out, "loader", reg.Loaders)
	addParts(out, "environment", reg.Environments)
	addParts(out, "adapter", reg.Adapters)
	addParts(out, "parser", reg.Parsers)
	addParts(out, "probe_layer", reg.ProbeLayers)
	addParts(out, "evidence", reg.Evidence)
	addParts(out, "grader", reg.Graders)
	addParts(out, "gate", reg.Gates)
	addParts(out, "metric", reg.Metrics)
	addParts(out, "verdict_policy", reg.Policies)
	addParts(out, "reporter", reg.Reporters)
	if reg.Gateway != nil {
		out["gateway/"+reg.Gateway.GetName()] = reg.Gateway.GetVersion()
	}
	if reg.Store != nil {
		out["store/"+reg.Store.GetName()] = reg.Store.GetVersion()
	}
	if reg.AdapterFaults != nil {
		out["adapter_faults/"+reg.AdapterFaults.GetName()] = reg.AdapterFaults.GetVersion()
	}
	return out
}

// ManifestHash returns the sha256 of the manifest's canonical JSON with Hash
// empty. The scheduler recomputes it before the first run, so an edited
// manifest file never runs.
func ManifestHash(m *schemas.Manifest) (string, error) {
	return utils.ManifestHash(m)
}

// LoadSuite resolves ref with the registered loaders, in registry order, and
// returns the first suite that loads.
func LoadSuite(ctx context.Context, reg *schemas.Registry, ref string) (*schemas.Suite, error) {
	if reg == nil || len(reg.Loaders) == 0 {
		return nil, errors.New("no suite loader registered")
	}
	var errs []error
	for _, l := range reg.Loaders {
		s, err := l.Load(ctx, ref)
		if err == nil && s != nil {
			return s, nil
		}
		if err == nil {
			err = errors.New("loader returned no suite")
		}
		errs = append(errs, fmt.Errorf("loader %s: %w", l.GetName(), err))
	}
	return nil, fmt.Errorf("load suite %s: %w", ref, errors.Join(errs...))
}

// partError tags an error with the part that produced it, so the run's
// failure list names the part, not just the stage.
type partError struct {
	part string
	err  error
}

func (e *partError) Error() string { return e.part + ": " + e.err.Error() }

func (e *partError) Unwrap() error { return e.err }

func wrapPart(kind string, p schemas.Part, err error) error {
	if err == nil {
		return nil
	}
	return &partError{part: kind + "/" + p.GetName(), err: err}
}

func addParts[T schemas.Part](out map[string]string, kind string, parts []T) {
	for _, p := range parts {
		out[kind+"/"+p.GetName()] = p.GetVersion()
	}
}

func findPart[T schemas.Part](parts []T, name string) (T, bool) {
	for _, p := range parts {
		if p.GetName() == name {
			return p, true
		}
	}
	var zero T
	return zero, false
}

func metricName(m schemas.Metric) string {
	if n := m.Descriptor().Name; n != "" {
		return n
	}
	return m.GetName()
}

// selectGates returns the baseline gates plus the experiment's extra gates,
// in registry order. Baseline gates cannot be switched off.
func selectGates(reg *schemas.Registry, extra []string) []schemas.Gate {
	want := map[string]bool{}
	for _, n := range extra {
		want[n] = true
	}
	var out []schemas.Gate
	for _, g := range reg.Gates {
		if g.Baseline() || want[g.GetName()] {
			out = append(out, g)
		}
	}
	return out
}

// selectMetrics returns every metric, or the named subset, in registry order.
func selectMetrics(reg *schemas.Registry, names []string) []schemas.Metric {
	if len(names) == 0 {
		return append([]schemas.Metric(nil), reg.Metrics...)
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []schemas.Metric
	for _, m := range reg.Metrics {
		if want[m.GetName()] || want[metricName(m)] {
			out = append(out, m)
		}
	}
	return out
}

// armConfig returns the resolved arm manifest for a slot.
func armConfig(m *schemas.Manifest, slot *schemas.Slot) (*schemas.ArmManifest, error) {
	if slot.Arm == CompatArm {
		if m.Compatibility == nil {
			return nil, fmt.Errorf("slot %s: compat slot without a compatibility agent", slot.SlotID)
		}
		return m.Compatibility, nil
	}
	a, ok := m.Arms[slot.Arm]
	if !ok {
		return nil, fmt.Errorf("slot %s: unknown arm %q", slot.SlotID, slot.Arm)
	}
	return &a, nil
}

// DefaultVerdictPolicy is the policy an experiment uses when verdict.policy is
// empty.
const DefaultVerdictPolicy = "paired_signflip"

// FindVerdictPolicy resolves an experiment's verdict.policy against the
// registry. Planning and analysis share it, so a policy name that analysis
// cannot find is refused before any run is scheduled.
func FindVerdictPolicy(reg *schemas.Registry, name string) (schemas.VerdictPolicy, error) {
	if name == "" {
		name = DefaultVerdictPolicy
	}
	if p, ok := findPart(reg.Policies, name); ok {
		return p, nil
	}
	return nil, fmt.Errorf("verdict policy %q is not registered", name)
}
