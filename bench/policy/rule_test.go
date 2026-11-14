package policy

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// trials builds one arm's runs of metric on cond from one string per task:
// '1' and '0' observed values, 'u' an unknown, one character per trial.
func trials(cond, arm, metric string, tasks ...string) []schemas.SealedRun {
	var out []schemas.SealedRun
	for i, p := range tasks {
		for j, ch := range p {
			v := schemas.Unknown("no grade", schemas.EvidenceOutcome)
			switch ch {
			case '1':
				v = obs(1)
			case '0':
				v = obs(0)
			}
			out = append(out, mkRun(cond, fmt.Sprintf("t%02d", i+1), arm, j+1, map[string]schemas.Value{metric: v}))
		}
	}
	return out
}

// valuesPerTask builds runs with one observed value per task and trial.
func valuesPerTask(cond, arm, metric string, perTask ...[]schemas.Value) []schemas.SealedRun {
	var out []schemas.SealedRun
	for i, vals := range perTask {
		for j, v := range vals {
			out = append(out, mkRun(cond, fmt.Sprintf("t%02d", i+1), arm, j+1, map[string]schemas.Value{metric: v}))
		}
	}
	return out
}

func six(s string) []string { return slices.Repeat([]string{s}, 6) }

func TestRuleChecks(t *testing.T) {
	clean, vs := schemas.ConditionClean, "verified_success"
	noReg := schemas.PolicyCheck{ID: "nr", Metric: vs, Condition: clean, Rule: RuleNoRegression}
	improved := schemas.PolicyCheck{ID: "im", Metric: vs, Condition: clean, Rule: RuleImproved}
	regressed := append(trials(clean, "A", vs, six("111")...), trials(clean, "B", vs, six("000")...)...)
	better := append(trials(clean, "A", vs, six("000")...), trials(clean, "B", vs, six("111")...)...)
	same := append(trials(clean, "A", vs, six("101")...), trials(clean, "B", vs, six("101")...)...)
	oneBound := append(trials(clean, "A", vs, six("111")...), trials(clean, "B", vs, six("1uu")...)...)
	costUp := append(valuesPerTask(clean, "A", "cost_usd", slices.Repeat([][]schemas.Value{{obs(1)}}, 6)...),
		valuesPerTask(clean, "B", "cost_usd", slices.Repeat([][]schemas.Value{{obs(5)}}, 6)...)...)
	stepsUnknown := append(valuesPerTask(clean, "A", "steps", slices.Repeat([][]schemas.Value{{obs(10)}}, 6)...),
		valuesPerTask(clean, "B", "steps", [][]schemas.Value{{obs(9)}, {schemas.Unknown("no gateway request log", schemas.EvidenceGateway)}}...)...)
	allConds := append(append([]schemas.SealedRun(nil), same...),
		append(trials(condStale, "A", vs, six("111")...), trials(condStale, "B", vs, six("000")...)...)...)
	tests := []struct {
		name     string
		check    schemas.PolicyCheck
		runs     []schemas.SealedRun
		status   schemas.PolicyStatus
		dbar     *float64
		evidence int
		reason   string
	}{
		{"no_regression fails on a regressed fixture", noReg, regressed, schemas.PolicyFail, ptr(-1), 18, "clean: regressed at both bounds"},
		{"no_regression passes with no change", noReg, same, schemas.PolicyPass, ptr(0), 0, "not regressed at either bound"},
		{"no_regression passes on an improvement", noReg, better, schemas.PolicyPass, ptr(1), 0, ""},
		{"no_regression unknown when one bound regresses", noReg, oneBound, schemas.PolicyUnknown, ptr(0), 0, "regressed at one bound only"},
		{"improved passes", improved, better, schemas.PolicyPass, ptr(1), 0, "improved at both bounds"},
		{"improved fails with no change", improved, same, schemas.PolicyFail, ptr(0), 0, "not improved"},
		{"improved fails on a regression", improved, regressed, schemas.PolicyFail, ptr(-1), 18, ""},
		{"a rise in cost is a regression", schemas.PolicyCheck{ID: "c", Metric: "cost_usd", Condition: clean, Rule: RuleNoRegression},
			costUp, schemas.PolicyFail, ptr(4), 6, "regressed at both bounds"},
		{"unbounded unknown of a count metric", schemas.PolicyCheck{ID: "s", Metric: "steps", Condition: clean, Rule: RuleNoRegression},
			stepsUnknown, schemas.PolicyUnknown, nil, 1, "no bounds"},
		{"all conditions: the worst stands", schemas.PolicyCheck{ID: "nr", Metric: vs, Condition: ConditionAll, Rule: RuleNoRegression},
			allConds, schemas.PolicyFail, ptr(-1), 18, "stale-notes: regressed at both bounds"},
		{"min_n counts the arm's observed runs", schemas.PolicyCheck{ID: "nr", Metric: vs, Condition: clean, Rule: RuleNoRegression, MinN: 7},
			oneBound, schemas.PolicyUnknown, nil, 0, "6 observed runs of 18, fewer than min_n 7"},
		{"metric not recorded", schemas.PolicyCheck{ID: "nr", Metric: "safe_success", Condition: clean, Rule: RuleNoRegression},
			same, schemas.PolicyUnknown, nil, 0, "not recorded"},
		{"no baseline runs", noReg, trials(clean, "B", vs, six("111")...), schemas.PolicyUnknown, nil, 0, "no task ran in both arms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Evaluate(single(tt.check), "h", testManifest(), nil, tt.runs).Checks[0]
			if r.Status != tt.status {
				t.Fatalf("status = %s, want %s: %s", r.Status, tt.status, r.Reason)
			}
			x, ok := r.Observed.Float()
			if (tt.dbar == nil) == ok || (ok && !near(x, *tt.dbar)) {
				t.Fatalf("observed = %+v, want d_bar %v", r.Observed, tt.dbar)
			}
			if len(r.Evidence) != min(tt.evidence, maxEvidence) {
				t.Fatalf("evidence = %v, want %d ids", r.Evidence, tt.evidence)
			}
			if !strings.Contains(r.Reason, tt.reason) {
				t.Fatalf("reason = %q, want %q", r.Reason, tt.reason)
			}
			if tt.check.Rule == RuleNoRegression && r.Threshold != ThresholdNoRegression {
				t.Fatalf("threshold = %q", r.Threshold)
			}
		})
	}
}

func TestDirection(t *testing.T) {
	for metric, want := range map[string]int{"verified_success": 1, "safe_success": 1, "claim_precision": 1, "custom": 1,
		"false_completion": -1, "steps": -1, "tool_calls": -1, "tokens": -1, "cost_usd": -1, "wall_time_s": -1, "retry_amplification": -1} {
		if got := direction(metric); got != want {
			t.Errorf("direction(%s) = %d, want %d", metric, got, want)
		}
	}
	if !unitInterval("verified_success") || unitInterval("steps") {
		t.Error("unitInterval")
	}
}
