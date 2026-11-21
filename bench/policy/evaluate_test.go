package policy

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const condStale = "stale-notes"

func obs(x float64) schemas.Value { return schemas.Observed(x, schemas.EvidenceOutcome) }

func ptr(x float64) *float64 { return &x }

var cleanGates = []schemas.GateResult{{Gate: "g1_protected_paths", Effect: schemas.GateVeto}}

// mkRun is one counted run with its metrics and a clean gate list.
func mkRun(cond, task, arm string, trial int, metrics map[string]schemas.Value) schemas.SealedRun {
	id := fmt.Sprintf("r_%s_%s_%s_%d", cond, task, arm, trial)
	outcome := schemas.OutcomeFail
	for name, v := range metrics { // QC2-04-03: an outcome-scored 1 needs a passing outcome to stand
		if f, ok := v.Float(); ok && f > 0 && schemas.OutcomeScoredMetric(name) {
			outcome = schemas.OutcomePass
		}
	}
	return schemas.SealedRun{
		Slot:    schemas.Slot{Task: task, Arm: arm, Condition: cond, Trial: trial, Counted: true},
		Run:     schemas.Run{RunID: id, Outcome: outcome},
		Metrics: metrics,
		Gates:   cleanGates,
	}
}

// series is one run per value of metric, on tasks t01, t02, ... of arm.
func series(cond, arm, metric string, vals ...schemas.Value) []schemas.SealedRun {
	out := make([]schemas.SealedRun, len(vals))
	for i, v := range vals {
		out[i] = mkRun(cond, fmt.Sprintf("t%02d", i+1), arm, 1, map[string]schemas.Value{metric: v})
	}
	return out
}

// repeatValue returns n copies of v.
func repeatValue(v schemas.Value, n int) []schemas.Value {
	return slices.Repeat([]schemas.Value{v}, n)
}

// costRuns builds runs with cost_usd and verified_success per run.
func costRuns(cond, arm string, costs []schemas.Value, success []schemas.Value) []schemas.SealedRun {
	out := make([]schemas.SealedRun, len(costs))
	for i := range costs {
		out[i] = mkRun(cond, fmt.Sprintf("t%02d", i+1), arm, 1, map[string]schemas.Value{"cost_usd": costs[i], "verified_success": success[i]})
	}
	return out
}

func testManifest() *schemas.Manifest {
	return &schemas.Manifest{ExpID: "e_test", Experiment: schemas.Experiment{
		Primary: schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Verdict: schemas.VerdictSpec{Alpha: 0.05},
	}}
}

func single(c schemas.PolicyCheck) *schemas.Policy {
	return &schemas.Policy{PolicyVersion: 1, Name: "p", Checks: []schemas.PolicyCheck{c}}
}

func TestEvaluateChecks(t *testing.T) {
	clean := schemas.ConditionClean
	na := schemas.NotApplicable()
	unk := schemas.Unknown("no claim evidence", schemas.EvidenceOutcome)
	tenZeros := series(clean, "B", "false_completion", repeatValue(obs(0), 10)...)
	twoClaims := series(clean, "B", "false_completion", append(repeatValue(obs(0), 8), obs(1), obs(1))...)
	vetoed := series(clean, "B", "verified_success", obs(1), obs(0), obs(1), obs(1))
	vetoed[1].Gates = []schemas.GateResult{{Gate: "g1_protected_paths", Effect: schemas.GateVeto, Fired: true}}
	vetoed[1].Run.Outcome = schemas.OutcomeIntegrityViolation
	ungated := series(clean, "B", "verified_success", obs(1), obs(1))
	ungated[1].Gates = nil
	tests := []struct {
		name     string
		check    schemas.PolicyCheck
		runs     []schemas.SealedRun
		status   schemas.PolicyStatus
		n        int
		observed *float64 // nil: not observed
		evidence []string
		reason   string
	}{
		{"pass under max", schemas.PolicyCheck{ID: "fc", Metric: "false_completion", Max: ptr(0.05), MinN: 10}, tenZeros,
			schemas.PolicyPass, 10, ptr(0), nil, "mean 0 over 10 observed runs"},
		{"fail over max", schemas.PolicyCheck{ID: "fc", Metric: "false_completion", Max: ptr(0.05), MinN: 10}, twoClaims,
			schemas.PolicyFail, 10, ptr(0.2), []string{"r_clean_t09_B_1", "r_clean_t10_B_1"}, "breaks <= 0.05"},
		{"unknown below min_n", schemas.PolicyCheck{ID: "fc", Metric: "false_completion", Max: ptr(0.05), MinN: 10},
			series(clean, "B", "false_completion", obs(0), obs(0), unk, na, na), schemas.PolicyUnknown, 4, nil,
			[]string{"r_clean_t03_B_1"}, "4 observed runs of 5, fewer than min_n 10 (1 unknown); needs review"},
		{"passing runs count as no false completion", schemas.PolicyCheck{ID: "fc", Metric: "false_completion", Max: ptr(0.05)},
			series(clean, "B", "false_completion", obs(0), na, na, obs(0)), schemas.PolicyPass, 4, ptr(0), nil, "mean 0 over 4"},
		{"not applicable left out of other metrics", schemas.PolicyCheck{ID: "cp", Metric: "claim_precision", Min: ptr(0.5)},
			series(clean, "B", "claim_precision", obs(1), na, na, obs(0)), schemas.PolicyPass, 2, ptr(0.5), nil, "mean 0.5 over 2"},
		{"unknown values never zero", schemas.PolicyCheck{ID: "fc", Metric: "false_completion", Max: ptr(0.4)},
			series(clean, "B", "false_completion", obs(1), unk, unk, unk), schemas.PolicyFail, 1, ptr(1), []string{"r_clean_t01_B_1"}, "3 unknown left out"},
		{"metric not recorded", schemas.PolicyCheck{ID: "x", Metric: "nope", Max: ptr(1)}, tenZeros,
			schemas.PolicyUnknown, 0, nil, nil, "metric nope is not recorded"},
		{"arm without runs", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(1), Arm: "C"}, tenZeros,
			schemas.PolicyUnknown, 0, nil, nil, "arm C has no counted runs on clean"},
		{"min passes", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Min: ptr(0.1)}, twoClaims,
			schemas.PolicyPass, 10, ptr(0.2), nil, ""},
		{"equals fails", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Equals: ptr(0)}, twoClaims,
			schemas.PolicyFail, 10, ptr(0.2), []string{"r_clean_t09_B_1", "r_clean_t10_B_1"}, "breaks = 0"},
		{"other condition is not read", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(0.05), Condition: condStale}, twoClaims,
			schemas.PolicyUnknown, 0, nil, nil, "no counted runs on stale-notes"},
		{"integrity violation", schemas.PolicyCheck{ID: "iv", Metric: MetricIntegrityViolations, Equals: ptr(0)}, vetoed,
			schemas.PolicyFail, 4, ptr(1), []string{"r_clean_t02_B_1"}, "1 of 4 runs had a fired veto gate"},
		{"integrity clean", schemas.PolicyCheck{ID: "iv", Metric: MetricIntegrityViolations, Equals: ptr(0)}, tenZeros,
			schemas.PolicyPass, 10, ptr(0), nil, "0 of 10 runs"},
		{"integrity of the baseline arm", schemas.PolicyCheck{ID: "iv", Metric: MetricIntegrityViolations, Equals: ptr(0), Arm: "A"},
			series(clean, "A", "verified_success", obs(1)), schemas.PolicyPass, 1, ptr(0), nil, ""},
		{"integrity without gate results", schemas.PolicyCheck{ID: "iv", Metric: MetricIntegrityViolations, Equals: ptr(0)}, ungated,
			schemas.PolicyUnknown, 1, nil, []string{"r_clean_t02_B_1"}, "count is a lower bound"},
		{"cost per success observed", schemas.PolicyCheck{ID: "c", Metric: MetricCostPerVerifiedSuccess, Max: ptr(3)},
			costRuns(clean, "B", repeatValue(obs(1), 4), []schemas.Value{obs(1), obs(0), obs(1), obs(0)}),
			schemas.PolicyPass, 4, ptr(2), nil, "$4 over 2 verified successes in 4 runs"},
		{"cost per success over max", schemas.PolicyCheck{ID: "c", Metric: MetricCostPerVerifiedSuccess, Max: ptr(1.5)},
			costRuns(clean, "B", repeatValue(obs(1), 4), []schemas.Value{obs(1), obs(0), obs(1), obs(0)}),
			schemas.PolicyFail, 4, ptr(2), []string{"r_clean_t02_B_1", "r_clean_t04_B_1"}, "is $2 each"},
		{"cost unknown", schemas.PolicyCheck{ID: "c", Metric: MetricCostPerVerifiedSuccess, Max: ptr(3)},
			costRuns(clean, "B", []schemas.Value{obs(1), schemas.Unknown("no gateway request log", schemas.EvidenceGateway)}, []schemas.Value{obs(1), obs(1)}),
			schemas.PolicyUnknown, 1, nil, []string{"r_clean_t02_B_1"}, "cost_usd unknown in 1 of 2 runs"},
		{"success unknown", schemas.PolicyCheck{ID: "c", Metric: MetricCostPerVerifiedSuccess, Max: ptr(3)},
			costRuns(clean, "B", []schemas.Value{obs(1), obs(1)}, []schemas.Value{obs(1), unk}),
			schemas.PolicyUnknown, 1, nil, []string{"r_clean_t02_B_1"}, "verified_success unknown in 1 of 2 runs"},
		{"no success fails a max", schemas.PolicyCheck{ID: "c", Metric: MetricCostPerVerifiedSuccess, Max: ptr(3)},
			costRuns(clean, "B", []schemas.Value{obs(0.5), obs(0)}, []schemas.Value{obs(0), obs(0)}),
			schemas.PolicyFail, 2, nil, []string{"r_clean_t01_B_1"}, "no verified success in 2 runs"},
		{"cost below min_n", schemas.PolicyCheck{ID: "c", Metric: MetricCostPerVerifiedSuccess, Max: ptr(3), MinN: 5},
			costRuns(clean, "B", []schemas.Value{obs(1)}, []schemas.Value{obs(1)}), schemas.PolicyUnknown, 1, nil, nil, "fewer than min_n 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Evaluate(single(tt.check), "h", testManifest(), nil, tt.runs)
			if len(res.Checks) != 1 {
				t.Fatalf("checks = %+v", res.Checks)
			}
			r := res.Checks[0]
			if r.Status != tt.status || r.N != tt.n {
				t.Fatalf("status %s n %d, want %s n %d: %s", r.Status, r.N, tt.status, tt.n, r.Reason)
			}
			x, ok := r.Observed.Float()
			if (tt.observed == nil) == ok || (ok && !near(x, *tt.observed)) {
				t.Fatalf("observed = %+v, want %v", r.Observed, tt.observed)
			}
			if tt.status == schemas.PolicyUnknown && r.Observed.State == schemas.ValueObserved {
				t.Fatal("an unknown result carries an observed value")
			}
			if !slices.Equal(r.Evidence, tt.evidence) {
				t.Fatalf("evidence = %v, want %v", r.Evidence, tt.evidence)
			}
			if !strings.Contains(r.Reason, tt.reason) {
				t.Fatalf("reason = %q, want %q", r.Reason, tt.reason)
			}
			if r.Condition == "" || r.Arm == "" || r.Threshold == "" {
				t.Fatalf("result lacks its target: %+v", r)
			}
		})
	}
}

func TestEvaluateTimeoutParity(t *testing.T) {
	clean := schemas.ConditionClean
	timeouts := func(runs []schemas.SealedRun, n int) []schemas.SealedRun {
		out := append([]schemas.SealedRun(nil), runs...)
		for i := 0; i < n && i < len(out); i++ {
			out[i].Run.Outcome = schemas.OutcomeTimeout
		}
		return out
	}
	ten := func(cond, arm string) []schemas.SealedRun {
		return series(cond, arm, "verified_success", repeatValue(obs(0), 10)...)
	}
	check := schemas.PolicyCheck{ID: "tp", Metric: MetricTimeoutRate, Rule: RuleTimeoutParity}
	tests := []struct {
		name     string
		check    schemas.PolicyCheck
		runs     []schemas.SealedRun
		status   schemas.PolicyStatus
		observed *float64
		evidence []string
		reason   string
	}{
		{"equal rates pass", check, append(ten(clean, "A"), ten(clean, "B")...), schemas.PolicyPass, ptr(0), nil,
			"timeout rate arm A 0.0% (0 of 10), arm B 0.0% (0 of 10)"},
		{"a 30 pp gap fails with the timed-out runs", check, append(ten(clean, "A"), timeouts(ten(clean, "B"), 3)...), schemas.PolicyFail, ptr(0.3),
			[]string{"r_clean_t01_B_1", "r_clean_t02_B_1", "r_clean_t03_B_1"}, "more than 10 pp apart"},
		{"all pools every condition", schemas.PolicyCheck{ID: "tp", Metric: MetricTimeoutRate, Rule: RuleTimeoutParity, Condition: ConditionAll},
			append(append(ten(clean, "A"), ten(clean, "B")...), append(ten(condStale, "A"), timeouts(ten(condStale, "B"), 10)...)...),
			schemas.PolicyFail, ptr(0.5), nil, "on every condition"},
		{"no runs of arm A is unknown", check, ten(clean, "B"), schemas.PolicyUnknown, nil, nil, "timeout parity needs both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Evaluate(single(tt.check), "h", testManifest(), nil, tt.runs).Checks[0]
			if r.Status != tt.status {
				t.Fatalf("status %s, want %s: %s", r.Status, tt.status, r.Reason)
			}
			x, ok := r.Observed.Float()
			if (tt.observed == nil) == ok || (ok && !near(x, *tt.observed)) {
				t.Fatalf("observed = %+v, want %v", r.Observed, tt.observed)
			}
			if tt.evidence != nil && !slices.Equal(r.Evidence, tt.evidence) {
				t.Fatalf("evidence = %v, want %v", r.Evidence, tt.evidence)
			}
			if !strings.Contains(r.Reason, tt.reason) || r.Threshold == "" {
				t.Fatalf("reason %q threshold %q, want %q", r.Reason, r.Threshold, tt.reason)
			}
		})
	}
}

func TestEvaluateDecision(t *testing.T) {
	unknownCheck := schemas.PolicyCheck{ID: "u", Metric: "nope", Max: ptr(1)}
	smallCheck := schemas.PolicyCheck{ID: "s", Metric: "false_completion", Max: ptr(1), MinN: 10}
	missingCheck := schemas.PolicyCheck{ID: "m", Metric: "verified_success", Max: ptr(1), MinN: 2}
	failCheck := schemas.PolicyCheck{ID: "f", Metric: "false_completion", Max: ptr(0)}
	passCheck := schemas.PolicyCheck{ID: "p", Metric: "false_completion", Max: ptr(1)}
	warn := func(c schemas.PolicyCheck) schemas.PolicyCheck { c.Severity = SeverityWarn; return c }
	runs := series(schemas.ConditionClean, "B", "false_completion", obs(1), obs(0))
	runs[0].Metrics["verified_success"] = schemas.Unknown("no grade", schemas.EvidenceOutcome)
	runs[1].Metrics["verified_success"] = schemas.Unknown("no grade", schemas.EvidenceOutcome)
	tests := []struct {
		name      string
		onUnknown string
		checks    []schemas.PolicyCheck
		want      schemas.PolicyDecision
		blocking  []bool
	}{
		{"all pass allows", "", []schemas.PolicyCheck{passCheck}, schemas.PolicyAllow, []bool{false}},
		{"block fail blocks", "", []schemas.PolicyCheck{passCheck, failCheck}, schemas.PolicyBlock, []bool{false, true}},
		{"unknown blocks by default", "", []schemas.PolicyCheck{passCheck, unknownCheck}, schemas.PolicyBlock, []bool{false, true}},
		{"small sample only warns by default", "", []schemas.PolicyCheck{passCheck, smallCheck}, schemas.PolicyWarn, []bool{false, false}},
		{"small sample blocks with on_unknown block", SeverityBlock, []schemas.PolicyCheck{smallCheck}, schemas.PolicyBlock, []bool{true}},
		{"missing data below min_n blocks by default", "", []schemas.PolicyCheck{missingCheck}, schemas.PolicyBlock, []bool{true}},
		{"unknown blocks with on_unknown block", SeverityBlock, []schemas.PolicyCheck{unknownCheck}, schemas.PolicyBlock, []bool{true}},
		{"unknown warns with on_unknown warn", SeverityWarn, []schemas.PolicyCheck{unknownCheck}, schemas.PolicyWarn, []bool{false}},
		{"on_unknown warn still blocks a fail", SeverityWarn, []schemas.PolicyCheck{unknownCheck, failCheck}, schemas.PolicyBlock, []bool{false, true}},
		{"warn severity fail only warns", "", []schemas.PolicyCheck{passCheck, warn(failCheck)}, schemas.PolicyWarn, []bool{false, false}},
		{"warn severity unknown only warns", "", []schemas.PolicyCheck{warn(unknownCheck)}, schemas.PolicyWarn, []bool{false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &schemas.Policy{PolicyVersion: 1, Name: "p", OnUnknown: tt.onUnknown, Checks: tt.checks}
			res := Evaluate(p, "h", testManifest(), nil, runs)
			if res.Decision != tt.want {
				t.Fatalf("decision = %s, want %s: %+v", res.Decision, tt.want, res.Checks)
			}
			for i, r := range res.Checks {
				if r.Blocking != tt.blocking[i] {
					t.Errorf("check %s blocking = %v, want %v", r.ID, r.Blocking, tt.blocking[i])
				}
			}
			if res.PolicyName != "p" || res.PolicyHash != "h" || res.EvaluatedAt.IsZero() {
				t.Fatalf("header = %+v", res)
			}
		})
	}
}

// TestDefaultPolicySmallBundle is a small synthetic bundle (8 counted runs,
// 4 tasks, no false completion): the default policy must not block it only
// because 4 runs of B are fewer than the false-completion min_n of 10.
func TestDefaultPolicySmallBundle(t *testing.T) {
	p, raw, err := Load("../policies/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var runs []schemas.SealedRun
	for _, arm := range []string{"A", "B"} {
		for i, pass := range []bool{true, true, true, false} {
			vs, fc := obs(1), schemas.NotApplicable()
			if !pass {
				vs, fc = obs(0), schemas.Observed(0, schemas.EvidenceNative)
			}
			runs = append(runs, mkRun(schemas.ConditionClean, fmt.Sprintf("t%02d", i+1), arm, 1, map[string]schemas.Value{
				"verified_success": vs, "false_completion": fc, "retry_amplification": obs(1),
				"cost_usd": schemas.Observed(0.1, schemas.EvidenceGateway),
			}))
		}
	}
	res := Evaluate(p, Hash(raw), testManifest(), nil, runs)
	if res.Decision == schemas.PolicyBlock {
		t.Fatalf("decision = block: %+v", res.Checks)
	}
	for _, c := range res.Checks {
		switch c.ID {
		case "false-completion-ceiling":
			if c.Status != schemas.PolicyUnknown || c.Blocking || c.N != 4 || !strings.Contains(c.Reason, "needs review") {
				t.Fatalf("false completion = %+v", c)
			}
		default:
			if c.Status != schemas.PolicyPass {
				t.Fatalf("check %s = %s: %s", c.ID, c.Status, c.Reason)
			}
		}
	}
	if res.Decision != schemas.PolicyWarn {
		t.Fatalf("decision = %s, want warn", res.Decision)
	}
}

func TestEvaluateHashMismatch(t *testing.T) {
	p := single(schemas.PolicyCheck{ID: "p", Metric: "false_completion", Max: ptr(1)})
	runs := series(schemas.ConditionClean, "B", "false_completion", obs(0))
	m := testManifest()
	m.PolicyHash = "frozenhash0000000000"
	if res := Evaluate(p, m.PolicyHash, m, nil, runs); res.Decision != schemas.PolicyAllow || len(res.Checks) != 1 {
		t.Fatalf("matching hash = %+v", res)
	}
	res := Evaluate(p, "changedhash111111111", m, nil, runs)
	if res.Decision != schemas.PolicyBlock || len(res.Checks) != 2 {
		t.Fatalf("mismatch = %+v", res)
	}
	first := res.Checks[0]
	if first.ID != CheckPolicyHash || !first.Blocking || first.Status != schemas.PolicyFail ||
		!strings.Contains(first.Reason, "changedhash1") || !strings.Contains(first.Reason, "frozenhash00") {
		t.Fatalf("hash check = %+v", first)
	}
	if res.Checks[1].Status != schemas.PolicyPass {
		t.Fatalf("the rules were not evaluated after the mismatch: %+v", res.Checks[1])
	}
}

func TestEvaluateTargets(t *testing.T) {
	runs := append(series(schemas.ConditionClean, "B", "false_completion", obs(0), obs(0)),
		series(condStale, "B", "false_completion", obs(1), obs(1))...)
	stale := testManifest()
	stale.Experiment.Primary.Condition = condStale
	analysis := &schemas.Analysis{Verdict: schemas.Verdict{Primary: schemas.Endpoint{Condition: condStale}}}
	tests := []struct {
		name      string
		check     schemas.PolicyCheck
		m         *schemas.Manifest
		an        *schemas.Analysis
		condition string
		mean      float64
		n         int
	}{
		{"default is the manifest primary", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(1)}, stale, nil, condStale, 1, 2},
		{"analysis primary wins", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(1)}, testManifest(), analysis, condStale, 1, 2},
		{"no manifest reads clean", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(1)}, nil, nil, schemas.ConditionClean, 0, 2},
		{"all reads every condition", schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(1), Condition: ConditionAll}, stale, nil, ConditionAll, 0.5, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Evaluate(single(tt.check), "h", tt.m, tt.an, runs).Checks[0]
			x, _ := r.Observed.Float()
			if r.Condition != tt.condition || r.Arm != DefaultArm || r.N != tt.n || !near(x, tt.mean) {
				t.Fatalf("result = %+v", r)
			}
		})
	}
}

func TestEvaluateNilPolicy(t *testing.T) {
	if Evaluate(nil, "", nil, nil, nil) != nil {
		t.Fatal("nil policy gives a result")
	}
}

func TestEvaluateSkipsUncountedRuns(t *testing.T) {
	runs := series(schemas.ConditionClean, "B", "false_completion", obs(1), obs(0))
	runs[0].Slot.Counted = false
	r := Evaluate(single(schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(0)}), "h", testManifest(), nil, runs).Checks[0]
	if r.Status != schemas.PolicyPass || r.N != 1 {
		t.Fatalf("uncounted run was read: %+v", r)
	}
}

func TestEvidenceCap(t *testing.T) {
	runs := series(schemas.ConditionClean, "B", "false_completion", repeatValue(obs(1), 30)...)
	r := Evaluate(single(schemas.PolicyCheck{ID: "x", Metric: "false_completion", Max: ptr(0)}), "h", testManifest(), nil, runs).Checks[0]
	if r.Status != schemas.PolicyFail || len(r.Evidence) != maxEvidence {
		t.Fatalf("evidence = %d ids, status %s", len(r.Evidence), r.Status)
	}
}

func near(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}
