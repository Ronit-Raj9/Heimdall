package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ErrNotFeatureTest is returned for a comparison without a gateway test.
var ErrNotFeatureTest = errors.New("this comparison is not a Bifrost feature test")

// now is the clock of ComputedAt; tests may replace it.
var now = time.Now

// EffectsDir is where the computed effects of an org root are saved.
func EffectsDir(root string) string {
	return filepath.Join(root, ".heimdall", "gateway", "effects")
}

// validExpID keeps an experiment id a plain file name.
func validExpID(expID string) bool {
	return expID != "" && expID != "." && expID != ".." && !strings.ContainsAny(expID, `/\`) && !strings.ContainsRune(expID, 0)
}

// writeFileAtomic writes data through a temp file and a rename, mode 0600.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Save writes one Effect, replacing the comparison's previous one.
func Save(root string, e Effect) error {
	if !validExpID(e.ExpID) {
		return fmt.Errorf("gwfeature: bad comparison id %q", e.ExpID)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(EffectsDir(root), e.ExpID+".json"), data)
}

// Load reads the saved Effect of a comparison; false when there is none.
func Load(root, expID string) (Effect, bool, error) {
	if !validExpID(expID) {
		return Effect{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(EffectsDir(root), expID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Effect{}, false, nil
	}
	if err != nil {
		return Effect{}, false, err
	}
	var e Effect
	if err := json.Unmarshal(data, &e); err != nil {
		return Effect{}, false, err
	}
	return e, true, nil
}

// List returns every saved Effect, newest ComputedAt first. Unreadable files
// are skipped.
func List(root string) ([]Effect, error) {
	entries, err := os.ReadDir(EffectsDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Effect
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		if e, ok, err := Load(root, strings.TrimSuffix(name, ".json")); err == nil && ok {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Effect) int { return b.ComputedAt.Compare(a.ComputedAt) })
	return out, nil
}

// Compute returns the Effect of a Bifrost feature test: the saved one when
// its runs did not change and refresh is false, else a fresh one from the
// feature's Compute, saved. A comparison with no counted run is unknown and
// not saved.
func Compute(ctx context.Context, src leaderboard.Source, root, expID string, refresh bool) (Effect, error) {
	return ComputeWith(ctx, src, root, expID, refresh, Facts{})
}

// Facts is what the server knows of a comparison beyond its run folder.
// Either func may be nil: unknown.
type Facts struct {
	// Active reports whether the comparison's runs are queued or running now.
	Active func(expID string) bool
	// Problem returns why no run could start (the runner's refusal), "" when none.
	Problem func(expID string) string
}

// ComputeWith is Compute with the server's facts. A comparison whose runs
// are still going is StateRunning with its progress and no verdict, never
// broken, and is not saved.
func ComputeWith(ctx context.Context, src leaderboard.Source, root, expID string, refresh bool, facts Facts) (Effect, error) {
	m, ok := src.Manifest(expID)
	if !ok || m == nil || m.Experiment.GatewayTest == nil {
		return Effect{}, ErrNotFeatureTest
	}
	test := *m.Experiment.GatewayTest
	unknown := func(reason string) Effect {
		return Effect{Feature: test.Feature, ExpID: expID, Control: test.Control, State: StateUnknown, Reason: reason,
			Headline: "Unknown: " + reason, Arms: []ArmResult{}, Source: Source{Kind: "experiment", ExpID: expID, Text: fmt.Sprintf("comparison %s: no runs read", expID)}, ComputedAt: now().UTC()}
	}
	f, ok := Lookup(test.Feature)
	if !ok || f.Compute == nil {
		return unknown(fmt.Sprintf("this Heimdall does not know the feature %s", test.Feature)), nil
	}
	for _, arm := range append([]string{test.Control}, test.Variants...) {
		if _, ok := m.Arms[arm]; !ok {
			return unknown(fmt.Sprintf("setup %s is not in this comparison", arm)), nil
		}
	}
	runs, err := LoadRuns(src, expID, f.AllConditions)
	if err != nil {
		return Effect{}, err
	}
	if graded, planned := len(runs), plannedRuns(m, f.AllConditions); stillRunning(src, expID, facts) {
		e := unknown("")
		e.State, e.Reason = StateRunning, ""
		e.Headline = fmt.Sprintf("Running, %d of %d runs graded.", graded, planned)
		e.Source.Runs = graded
		e.Source.Text = fmt.Sprintf("comparison %s: %d of %d runs graded", expID, graded, planned)
		return e, nil
	}
	if len(runs) == 0 {
		e := unknown("no finished runs yet")
		problem := ""
		if facts.Problem != nil {
			problem = strings.TrimSpace(facts.Problem(expID))
		}
		// A comparison that ended (it has an analysis) with no counted run is
		// a result: say why and save it, so the Gateway page shows it.
		if _, done := src.LatestDecision(expID); done || problem != "" {
			e.Reason = noRunReason(src, expID, problem)
			e.Headline = "No result: " + e.Reason
			e.Fingerprint = Fingerprint(nil, test.Feature)
			if err := Save(root, e); err != nil {
				return e, err
			}
		}
		return e, nil
	}
	fp := Fingerprint(runs, test.Feature)
	if !refresh {
		if saved, ok, err := Load(root, expID); err == nil && ok && saved.Fingerprint == fp {
			return saved, nil
		}
	}
	e := f.Compute(ctx, Input{ExpID: expID, Manifest: m, Test: test, Runs: runs, Src: src})
	e.Feature, e.ExpID, e.Control, e.Fingerprint, e.ComputedAt = test.Feature, expID, test.Control, fp, now().UTC()
	NoVerdictGuard(&e, src, expID)
	if e.Arms == nil {
		e.Arms = []ArmResult{}
	}
	e.Source.Kind, e.Source.ExpID, e.Source.Runs = "experiment", expID, len(runs)
	e.Source.Rows, e.Source.RawRows = 0, 0
	for _, r := range runs {
		e.Source.Rows += len(r.Rows)
		for _, row := range r.Rows {
			if row.RawKnown {
				e.Source.RawRows++
			}
		}
	}
	if e.Source.Text == "" {
		e.Source.Text = fmt.Sprintf("comparison %s: %d runs, %d gateway calls", expID, e.Source.Runs, e.Source.Rows)
	}
	if err := Save(root, e); err != nil {
		return e, err
	}
	return e, nil
}

// Guard limits of every feature test result.
const GuardMinRuns = 10 // runs per setup before a winner or a saving is named

// noVerdict clears every part of an Effect that names a winner, a saving or
// a setting to copy.
func noVerdict(e *Effect, state, reason, headline string) {
	e.State, e.Reason, e.Headline = state, reason, headline
	e.Result, e.Variant, e.Setting = "", "", nil
	e.SolveDelta, e.CostDelta, e.LatencyDelta = nil, nil, nil
}

// NoVerdictGuard applies the rules every feature test result follows, in
// this order: a test whose analysis found it broken states no verdict of
// any kind; nothing solved on any setup gives no winner, no saving and no
// setting; under GuardMinRuns runs on a setup gives none either. The
// gateway overhead test answers from a stand-in model, so solving does not
// apply to it; the broken rule still does.
func NoVerdictGuard(e *Effect, src leaderboard.Source, expID string) {
	if as, ok := src.(leaderboard.AnalysisSource); ok {
		if _, reasons, bad := as.LatestInvalid(expID); bad {
			reason := "the check of this test failed and gave no reason"
			if len(reasons) > 0 {
				reason = strings.TrimSuffix(strings.Join(reasons, "; "), ".")
			}
			noVerdict(e, StateNotApplied, "the test was broken: "+reason, "Broken: "+reason+". Fix it and run it again.")
			return
		}
	}
	if e.Feature == schemas.GatewayFeatureOverhead || e.State != StateMeasured {
		if e.State != StateMeasured && e.State != StateNotApplied {
			e.Result, e.Setting = "", nil
		}
		return
	}
	graded, solved := false, false
	for _, a := range e.Arms {
		if a.SolveRate != nil {
			graded = true
			if *a.SolveRate > 0 {
				solved = true
			}
		}
	}
	if graded && !solved {
		noVerdict(e, StateNotEnoughData, "no setup solved a task", "No setup solved a task, so nothing can be recommended.")
		return
	}
	for _, a := range e.Arms {
		if a.Runs < GuardMinRuns {
			noVerdict(e, StateNotEnoughData, fmt.Sprintf("too few runs to tell: setup %s has %d runs, %d are needed", a.Arm, a.Runs, GuardMinRuns), "Too few runs to tell. Nothing is recommended yet.")
			return
		}
	}
}

// noRunReason says why a finished comparison has no counted run.
func noRunReason(src leaderboard.Source, expID, problem string) string {
	all := src.Runs(expID)
	if len(all) == 0 && problem != "" {
		return "no run started: " + strings.TrimSuffix(problem, ".")
	}
	if as, ok := src.(leaderboard.AnalysisSource); ok {
		if _, reasons, bad := as.LatestInvalid(expID); bad && len(reasons) > 0 && len(all) > 0 {
			return "no run finished: " + strings.Join(reasons, "; ")
		}
	}
	if len(all) == 0 {
		return "no run started. The runner refused every run before it began; the comparison's events list the refusal."
	}
	infra := 0
	for _, r := range all {
		if r.Run.Outcome == schemas.OutcomeAbortedInfra {
			infra++
		}
	}
	if infra > 0 {
		return fmt.Sprintf("no run finished: %d of %d were stopped by the machine, not by the agent", infra, len(all))
	}
	return fmt.Sprintf("no run finished: %d started and none was sealed", len(all))
}

// plannedRuns counts the comparison's slots a feature counts.
func plannedRuns(m *schemas.Manifest, allConditions bool) int {
	n := 0
	for _, sl := range m.Slots {
		if sl.Arm == "compat" {
			continue
		}
		if allConditions || sl.Condition == schemas.ConditionClean {
			n++
		}
	}
	return n
}

// stillRunning reports whether the comparison's runs are still going: the
// server says so, or, when it cannot tell, a run is started and not sealed.
func stillRunning(src leaderboard.Source, expID string, facts Facts) bool {
	if facts.Active != nil {
		return facts.Active(expID)
	}
	return slices.ContainsFunc(src.Runs(expID), func(r leaderboard.Run) bool {
		return !r.Sealed && !r.Superseded && r.Run.Outcome != schemas.OutcomeAbortedInfra
	})
}
