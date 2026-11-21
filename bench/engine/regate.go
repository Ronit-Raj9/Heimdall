package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Regated is one sealed run's gates checked again at the current gate
// versions, with the outcome, Y and metrics that follow from them.
type Regated struct {
	Gates      []schemas.GateResult
	Recomputed []string                 // gates checked again, in registry order
	Outcome    schemas.Outcome          // the outcome under the new gates
	Y          schemas.Value            // scoreOf(Outcome)
	Metrics    map[string]schemas.Value // only metrics whose value the new gates changed
	Notes      []string                 // results carried from the seal and why
}

// RegateVersions returns gate name -> version of every gate a run of m is
// checked by now: the baseline gates and the experiment's extra gates.
func RegateVersions(reg *schemas.Registry, m *schemas.Manifest) map[string]string {
	out := map[string]string{}
	for _, g := range selectGates(reg, m.Experiment.ExtraGates) {
		out[g.GetName()] = g.GetVersion()
	}
	return out
}

// Regate checks a sealed run's gates again from the run's sealed evidence,
// with no agent, model or container run: it reads evidence.json and
// workspace.diff from the run folder, and takes the grade from sr (load sr
// with the original grade source). Only gates whose current version differs
// from the version in the sealed result, or which have no sealed result, are
// checked again; the rest are carried over, because a gate at the same
// version gives the same result on the same evidence. A gate that fails to
// run keeps its sealed result, so a regate never drops a veto it could not
// recompute.
//
// The outcome changes when the veto changes: a run with a new fired veto
// becomes integrity_violation, and an integrity_violation run with none left
// is decided again from its grade, as the pipeline does (a run that recorded
// any part failure stays infra_error). It also changes when the sealed fault
// proxy ledger shows the provider was unreachable (the upstream rule of
// outcomeFrom) and the run was sealed as fail or timeout before that rule
// existed: such a run becomes infra_error. A regate cannot give the slot a new
// run, so a run that never got a model response is infra_error here, not
// aborted_infra. aborted_infra runs never change. A
// metric is replaced only when computing it with the new gates gives another
// value than with the sealed ones, and then only if the sealed gates
// reproduce its sealed value; otherwise Regate fails rather than mix inputs.
//
// The environment handle is not sealed, so a run whose evidence recorded
// protected-file hashes after the run (which G1 compares with the handle's
// before-hashes) cannot be regated faithfully and fails.
func Regate(ctx context.Context, reg *schemas.Registry, m *schemas.Manifest, suite *schemas.Suite, sr *schemas.SealedRun) (*Regated, error) {
	if reg == nil || m == nil || suite == nil || sr == nil {
		return nil, errors.New("regate: registry, manifest, suite and run are required")
	}
	ev, err := sealedEvidence(sr)
	if err != nil {
		return nil, fmt.Errorf("regate run %s: %w", sr.Run.RunID, err)
	}
	if len(ev.Protect) > 0 {
		return nil, fmt.Errorf("regate run %s: evidence has protected-file hashes but the environment handle that holds the before-hashes is not sealed", sr.Run.RunID)
	}
	rc, err := regateContext(m, suite, sr, ev)
	if err != nil {
		return nil, fmt.Errorf("regate run %s: %w", sr.Run.RunID, err)
	}
	out := &Regated{}
	out.Gates, out.Recomputed, out.Notes = regateGates(ctx, reg, rc, ev, sr.Gates)
	ended := sessionsEnded(ev.Sessions, len(rc.Task.Sessions))
	out.Outcome = regateOutcome(&sr.Run, ended, out.Gates, ev.Grade, ev.Exec, utils.TallyUpstream(ev.Ledger, ev.Requests))
	out.Y = scoreOf(out.Outcome)
	if out.Metrics, err = regateMetrics(ctx, reg, rc, ev, sr, out.Gates); err != nil {
		return nil, fmt.Errorf("regate run %s: %w", sr.Run.RunID, err)
	}
	return out, nil
}

// sealedEvidence reads evidence.json from the run folder and points the diff
// at the folder's workspace.diff, whose sha256 must match the one recorded.
// The recorded path is ignored: the run folder may have moved since. The
// scenario evidence is read from the folder too (sealedScenario).
func sealedEvidence(sr *schemas.SealedRun) (*schemas.Evidence, error) {
	b, err := os.ReadFile(filepath.Join(sr.Dir, "evidence.json"))
	if err != nil {
		return nil, fmt.Errorf("read evidence: %w", err)
	}
	var ev schemas.Evidence
	if err := json.Unmarshal(b, &ev); err != nil {
		return nil, fmt.Errorf("decode evidence: %w", err)
	}
	if ev.Diff != nil {
		ev.Diff.Path = filepath.Join(sr.Dir, "workspace.diff")
		if ev.Diff.SHA256 != "" {
			sum, err := utils.SHA256File(ev.Diff.Path)
			if err != nil {
				return nil, fmt.Errorf("hash diff: %w", err)
			}
			if sum != ev.Diff.SHA256 {
				return nil, fmt.Errorf("workspace.diff sha256 %s, evidence records %s: %w", sum, ev.Diff.SHA256, schemas.ErrSealMismatch)
			}
		}
	}
	if err := sealedScenario(sr.Dir, &ev); err != nil {
		return nil, err
	}
	ev.Grade, ev.Gates = sr.Grade, nil
	return &ev, nil
}

// regateContext rebuilds the run context the gates read, from the manifest,
// the suite and the sealed run. There is no environment handle or key.
func regateContext(m *schemas.Manifest, suite *schemas.Suite, sr *schemas.SealedRun, ev *schemas.Evidence) (*schemas.RunContext, error) {
	slot, run := sr.Slot, sr.Run
	task := suite.TaskByID(slot.Task)
	if task == nil {
		return nil, fmt.Errorf("task %s is not in suite %s", slot.Task, suite.ID)
	}
	arm, err := armConfig(m, &slot)
	if err != nil {
		return nil, err
	}
	cfg := arm.Config
	rc := &schemas.RunContext{
		Experiment: &m.Experiment, Manifest: m, Suite: suite, Task: task, Slot: &slot, Run: &run,
		Agent: &cfg, Exec: ev.Exec, Dir: sr.Dir, Logger: schemas.NoopLogger{},
	}
	if slot.Condition != schemas.ConditionClean {
		rc.Probe = suite.ProbeByID(slot.Condition)
	}
	return rc, nil
}

// regateGates checks again every selected gate whose version changed and
// carries the others, in registry order. Sealed results of gates no longer
// selected are carried too.
//
// A run sealed with no gate results at all never reached its gate stage (it
// was interrupted or broke first), so its evidence lacks what gates read, such
// as the diff or the run's network: checking gates on it now would fire them
// on missing evidence (egress fires on "no network recorded" and would make
// the whole experiment INVALID). Such a run keeps no gate results.
func regateGates(ctx context.Context, reg *schemas.Registry, rc *schemas.RunContext, ev *schemas.Evidence, sealed []schemas.GateResult) ([]schemas.GateResult, []string, []string) {
	if len(sealed) == 0 {
		return nil, nil, []string{"no gate checked: the run was sealed with no gate results, so its gate stage never ran"}
	}
	byName := make(map[string]schemas.GateResult, len(sealed))
	for _, r := range sealed {
		byName[r.Gate] = r
	}
	var gates []schemas.GateResult
	var recomputed, notes []string
	seen := map[string]bool{}
	for _, g := range selectGates(reg, rc.Experiment.ExtraGates) {
		name := g.GetName()
		seen[name] = true
		old, had := byName[name]
		if had && old.Version == g.GetVersion() {
			gates = append(gates, old)
			continue
		}
		res, err := g.Check(ctx, rc, ev)
		if err != nil || res == nil {
			if err == nil {
				err = errors.New("no result")
			}
			if had {
				gates = append(gates, old)
				notes = append(notes, fmt.Sprintf("%s: kept sealed result %s: recompute failed: %v", name, old.Version, err))
			} else {
				notes = append(notes, fmt.Sprintf("%s: no result: %v", name, err))
			}
			continue
		}
		r := *res
		if r.Gate == "" {
			r.Gate = name
		}
		if r.Version == "" {
			r.Version = g.GetVersion()
		}
		if r.Effect == "" {
			r.Effect = g.Effect()
		}
		gates = append(gates, r)
		recomputed = append(recomputed, name)
	}
	for _, r := range sealed {
		if !seen[r.Gate] {
			gates = append(gates, r)
			notes = append(notes, fmt.Sprintf("%s: kept sealed result %s: gate not selected now", r.Gate, r.Version))
		}
	}
	return gates, recomputed, notes
}

// regateOutcome is the run's outcome under the new gates and the upstream
// rule (see Regate).
func regateOutcome(run *schemas.Run, ended schemas.Outcome, gates []schemas.GateResult, grade *schemas.Grade, exec *schemas.ExecResult, up utils.UpstreamTally) schemas.Outcome {
	switch {
	case run.Outcome == schemas.OutcomeAbortedInfra:
		return run.Outcome
	case vetoFired(gates):
		return schemas.OutcomeIntegrityViolation
	case run.Outcome == schemas.OutcomeIntegrityViolation:
		o := outcomeFrom(false, len(run.Failures) > 0, ended, gates, grade, exec, up)
		if o == schemas.OutcomeAbortedInfra {
			return schemas.OutcomeInfraError
		}
		return o
	case run.Outcome != schemas.OutcomeFail && run.Outcome != schemas.OutcomeTimeout:
		return run.Outcome
	case up.NeverAnswered(), up.EndedOnError():
		return schemas.OutcomeInfraError
	default:
		return run.Outcome
	}
}

// regateMetrics returns the metrics whose value the new gates change.
func regateMetrics(ctx context.Context, reg *schemas.Registry, rc *schemas.RunContext, ev *schemas.Evidence, sr *schemas.SealedRun, gates []schemas.GateResult) (map[string]schemas.Value, error) {
	before, after := *ev, *ev
	before.Gates, after.Gates = sr.Gates, gates
	out := map[string]schemas.Value{}
	for _, mt := range selectMetrics(reg, rc.Experiment.Metrics) {
		name := metricName(mt)
		sealed, ok := sr.Metrics[name]
		if !ok {
			continue
		}
		old, errOld := mt.Compute(ctx, rc, &before)
		now, errNow := mt.Compute(ctx, rc, &after)
		if errOld != nil || errNow != nil {
			return nil, fmt.Errorf("metric %s: %w", name, errors.Join(errOld, errNow))
		}
		if sameValue(old, now) {
			continue
		}
		if !sameValue(old, sealed) {
			return nil, fmt.Errorf("metric %s depends on gates but the sealed evidence does not reproduce its sealed value", name)
		}
		out[name] = now
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// sameValue compares two values by their JSON form.
func sameValue(a, b schemas.Value) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}

// sealedScenario completes a scenario run's evidence from its sealed run
// folder, so gates see what the run saw: answer.txt (its sha256 must match
// the one recorded), each exported state file and each copied artifact
// file (likewise), and the tool
// ledger (tools.jsonl), sessions.json and services.json when evidence.json
// holds none. A sealed tools.jsonl without calls gives an empty, non-nil
// call list, so a metric reads zero calls, not unknown. A run without
// scenario files is left as it is.
func sealedScenario(dir string, ev *schemas.Evidence) error {
	if a := ev.Answer; a != nil {
		b, err := os.ReadFile(filepath.Join(dir, FileAnswer))
		if err != nil {
			return fmt.Errorf("read answer: %w", err)
		}
		if a.SHA256 != "" && utils.SHA256Bytes(b) != a.SHA256 {
			return fmt.Errorf("%s sha256 differs from the one evidence records: %w", FileAnswer, schemas.ErrSealMismatch)
		}
		a.Text = string(b)
	}
	for _, f := range ev.State {
		if f.Error != "" || f.Path == "" || f.SHA256 == "" {
			continue
		}
		p, err := utils.ResolveInside(dir, f.Path)
		if err != nil {
			return fmt.Errorf("state %s/%s: %w", f.Service, f.ID, err)
		}
		sum, err := utils.SHA256File(p)
		if err != nil {
			return fmt.Errorf("state %s/%s: %w", f.Service, f.ID, err)
		}
		if sum != f.SHA256 {
			return fmt.Errorf("state %s sha256 differs from the one evidence records: %w", f.Path, schemas.ErrSealMismatch)
		}
	}
	for _, f := range ev.Artifacts {
		if f.Error != "" || f.Path == "" || f.SHA256 == "" {
			continue
		}
		p, err := utils.ResolveInside(dir, f.Path)
		if err != nil {
			return fmt.Errorf("artifact %s: %w", f.Path, err)
		}
		sum, err := utils.SHA256File(p)
		if err != nil {
			return fmt.Errorf("artifact %s: %w", f.Path, err)
		}
		if sum != f.SHA256 {
			return fmt.Errorf("artifact %s sha256 differs from the one evidence records: %w", f.Path, schemas.ErrSealMismatch)
		}
	}
	if len(ev.ToolCalls) == 0 {
		// The collection rule decides: a ledger collection refused as
		// incomplete stays unknown (nil), never an empty or partial list.
		calls, _, err := evidence.ReadRunToolLedger(dir)
		if err != nil {
			return err
		}
		ev.ToolCalls = calls
	}
	if len(ev.Sessions) == 0 {
		if err := readOptional(filepath.Join(dir, FileSessions), &ev.Sessions); err != nil {
			return err
		}
	}
	if len(ev.Services) == 0 {
		if err := readOptional(filepath.Join(dir, FileServices), &ev.Services); err != nil {
			return err
		}
	}
	return nil
}

// fileToolLedger is the tool proxy's ledger in the run folder.
const fileToolLedger = "tools.jsonl"

// readOptional decodes a JSON file into v; a missing file leaves v as is.
func readOptional(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}
