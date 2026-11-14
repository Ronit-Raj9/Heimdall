package verdict

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

const (
	testManifestHash = "manifest-hash"
	condStale        = "stale-notes"
)

var testFrozenAt = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// testManifest returns a manifest with the given arms, primary clean and
// verified_success, alpha 0.05 and assumed SD 0.30, and no declared guards (so
// the default guards apply).
func testManifest(arms ...string) *schemas.Manifest {
	m := &schemas.Manifest{
		ExpID:     "e_test",
		CreatedAt: testFrozenAt,
		Hash:      testManifestHash,
		Arms:      map[string]schemas.ArmManifest{},
		Experiment: schemas.Experiment{
			ID:         "e_test",
			Primary:    schemas.Endpoint{Condition: schemas.ConditionClean, Metric: MetricVerifiedSuccess},
			Verdict:    schemas.VerdictSpec{Policy: PolicyPairedSignFlip, Alpha: 0.05, AssumedSD: 0.30},
			Conditions: []schemas.ConditionSpec{{ID: schemas.ConditionClean, Trials: 3}},
		},
	}
	for _, a := range arms {
		m.Arms[a] = schemas.ArmManifest{Label: a}
	}
	return m
}

// taskID names the i-th (0-based) task.
func taskID(i int) string { return fmt.Sprintf("t%02d", i+1) }

// sealedRun is one counted run whose verified_success is y.
func sealedRun(task, arm, cond string, trial int, y schemas.Value) schemas.SealedRun {
	id := fmt.Sprintf("r_%s_%s_%s_%d", task, arm, cond, trial)
	out := schemas.OutcomeUnknown
	if x, ok := y.Float(); ok {
		out = schemas.OutcomeFail
		if x == 1 {
			out = schemas.OutcomePass
		}
	}
	return schemas.SealedRun{
		Slot: schemas.Slot{
			SlotID: "s_" + id, PairID: fmt.Sprintf("p_%s_%s_%d", task, cond, trial), ExpID: "e_test",
			Task: task, TaskVersion: "1.0.0", Arm: arm, Condition: cond, Trial: trial, Counted: true,
		},
		Run:         schemas.Run{RunID: id, ManifestHash: testManifestHash, Outcome: out},
		GradeSource: "original",
		Metrics:     map[string]schemas.Value{MetricVerifiedSuccess: y},
		Gateway:     cleanTally(),
	}
}

// cleanTally is a complete gateway log: two model requests, both with usage,
// and a drop counter of 0.
func cleanTally() *schemas.GatewayTally {
	zero := int64(0)
	return &schemas.GatewayTally{Requests: 2, WithUsage: 2, Complete: true, Dropped: &zero}
}

// withTimeouts marks the first n runs of arm as timed out, keeping their grade.
func withTimeouts(runs []schemas.SealedRun, arm string, n int) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	for i := range out {
		if n > 0 && out[i].Slot.Arm == arm {
			out[i].Run.Agent.TimedOut = true
			n--
		}
	}
	return out
}

// withGateway sets the gateway tally of every run of arm with f.
func withGateway(runs []schemas.SealedRun, arm string, f func(i int) *schemas.GatewayTally) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	i := 0
	for k := range out {
		if out[k].Slot.Arm == arm {
			out[k].Gateway = f(i)
			i++
		}
	}
	return out
}

// dropped is a complete gateway tally whose drop counter reads n.
func dropped(n int64) *schemas.GatewayTally {
	t := cleanTally()
	t.Dropped = &n
	return t
}

// pattern builds runs for one arm and condition from one string per task:
// '1' a pass, '0' a fail, 'u' an unknown, one character per trial.
func pattern(cond, arm string, tasks ...string) []schemas.SealedRun {
	var out []schemas.SealedRun
	for i, p := range tasks {
		for j, c := range p {
			var y schemas.Value
			switch c {
			case '1':
				y = schemas.Observed(1, schemas.EvidenceOutcome)
			case '0':
				y = schemas.Observed(0, schemas.EvidenceOutcome)
			default:
				y = schemas.Unknown("grader could not decide", schemas.EvidenceOutcome)
			}
			out = append(out, sealedRun(taskID(i), arm, cond, j+1, y))
		}
	}
	return out
}

// passes builds runs where task i passes passes[i] of trials.
func passes(cond, arm string, trials int, counts ...int) []schemas.SealedRun {
	tasks := make([]string, len(counts))
	for i, k := range counts {
		tasks[i] = strings.Repeat("1", k) + strings.Repeat("0", trials-k)
	}
	return pattern(cond, arm, tasks...)
}

// repeat returns n copies of s.
func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// withVeto marks the run of task/arm/trial 1 in cond as vetoed by G1.
func withVeto(runs []schemas.SealedRun, task, arm string) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	for i, r := range out {
		if r.Slot.Task == task && r.Slot.Arm == arm && r.Slot.Trial == 1 {
			r.Gates = []schemas.GateResult{{Gate: "g1_protected_paths", Effect: schemas.GateVeto, Fired: true}}
			r.Run.Outcome = schemas.OutcomeIntegrityViolation
			r.Metrics = map[string]schemas.Value{MetricVerifiedSuccess: schemas.Observed(0, schemas.EvidenceOutcome)}
			out[i] = r
		}
	}
	return out
}

func concat(parts ...[]schemas.SealedRun) []schemas.SealedRun {
	var out []schemas.SealedRun
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// stalePrimaryManifest has stale-notes as the primary condition and clean as
// a no_regression guard.
func stalePrimaryManifest() *schemas.Manifest {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Primary.Condition = condStale
	m.Experiment.Guards = []schemas.GuardSpec{
		{Condition: schemas.ConditionClean, Rule: RuleNoRegression},
		{Rule: RuleNoNewIntegrityViolation},
	}
	return m
}

func decideOrFail(t *testing.T, m *schemas.Manifest, runs []schemas.SealedRun) *schemas.Analysis {
	t.Helper()
	a, err := newPairedSignFlipPolicy().Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	return a
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestDecide(t *testing.T) {
	workedA := []int{3, 3, 0, 2, 0, 2, 1, 3}
	workedB := []int{3, 3, 1, 3, 0, 3, 2, 3}
	none := []int{0, 0, 0, 0, 0, 0, 0, 0}
	all := []int{3, 3, 3, 3, 3, 3, 3, 3}
	clean := schemas.ConditionClean

	tests := []struct {
		name      string
		m         *schemas.Manifest
		runs      []schemas.SealedRun
		decision  schemas.Decision
		action    schemas.Action
		integrity schemas.IntegrityStatus
		p         float64 // checked when >= 0
		dbar      float64 // checked when p >= 0
		reason    string  // substring of some invalid reason
	}{
		{
			name:     "worked example is inconclusive",
			m:        testManifest(ArmBaseline, ArmCandidate),
			runs:     concat(passes(clean, ArmBaseline, 3, workedA...), passes(clean, ArmCandidate, 3, workedB...)),
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, integrity: schemas.IntegrityClean,
			p: 0.125, dbar: 1.0 / 6,
		},
		{
			name:     "clear improvement ships",
			m:        testManifest(ArmBaseline, ArmCandidate),
			runs:     concat(passes(clean, ArmBaseline, 3, none...), passes(clean, ArmCandidate, 3, all...)),
			decision: schemas.DecisionImproved, action: schemas.ActionShip, integrity: schemas.IntegrityClean,
			p: 2.0 / 256, dbar: 1,
		},
		{
			name: "clear improvement with a new veto in B holds",
			m:    testManifest(ArmBaseline, ArmCandidate),
			runs: concat(passes(clean, ArmBaseline, 3, none...),
				withVeto(passes(clean, ArmCandidate, 3, all...), taskID(2), ArmCandidate)),
			decision: schemas.DecisionRegressed, action: schemas.ActionHold, integrity: schemas.IntegrityRegressed,
			p: -1,
		},
		{
			name:     "clear regression holds",
			m:        testManifest(ArmBaseline, ArmCandidate),
			runs:     concat(passes(clean, ArmBaseline, 3, all...), passes(clean, ArmCandidate, 3, none...)),
			decision: schemas.DecisionRegressed, action: schemas.ActionHold, integrity: schemas.IntegrityClean,
			p: 2.0 / 256, dbar: -1,
		},
		{
			name: "coverage below 0.90 in one arm is invalid",
			m:    testManifest(ArmBaseline, ArmCandidate),
			runs: concat(passes(clean, ArmBaseline, 3, none...),
				pattern(clean, ArmCandidate, "uuu", "111", "111", "111", "111", "111", "111", "111")),
			decision: schemas.DecisionInvalid, action: schemas.ActionFixHarness, integrity: schemas.IntegrityClean,
			p: -1, reason: "graded coverage 0.875 (21 of 24 runs) in arm B",
		},
		{
			name: "observed-only improvement that fails at the lower bound is not improved",
			m:    testManifest(ArmBaseline, ArmCandidate),
			runs: concat(
				pattern(clean, ArmBaseline, append(append(repeat("000", 5), "0uu"), repeat("111", 4)...)...),
				pattern(clean, ArmCandidate, append(append(repeat("111", 5), "1uu"), repeat("111", 4)...)...)),
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, integrity: schemas.IntegrityClean,
			p: 2.0 / 64, dbar: 0.6,
		},
		{
			name: "undetermined clean guard blocks an improvement",
			m:    stalePrimaryManifest(),
			runs: concat(
				passes(condStale, ArmBaseline, 3, none...), passes(condStale, ArmCandidate, 3, all...),
				pattern(clean, ArmBaseline, append(append(repeat("111", 5), "1uu"), repeat("111", 4)...)...),
				pattern(clean, ArmCandidate, append(append(repeat("000", 5), "0uu"), repeat("111", 4)...)...)),
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, integrity: schemas.IntegrityClean,
			p: 2.0 / 256, dbar: 1,
		},
		{
			name: "usage below 0.95 in one arm is invalid",
			m:    testManifest(ArmBaseline, ArmCandidate),
			runs: concat(passes(clean, ArmBaseline, 3, none...),
				withGateway(passes(clean, ArmCandidate, 3, all...), ArmCandidate, func(i int) *schemas.GatewayTally {
					t := cleanTally()
					if i < 3 {
						t.WithUsage = 1
					}
					return t
				})),
			decision: schemas.DecisionInvalid, action: schemas.ActionFixHarness, integrity: schemas.IntegrityClean,
			p: -1, reason: "usage completeness 0.938 (45 of 48 model requests carry usage, 0 in runs with a missing or incomplete log) in arm B is below 0.95",
		},
		{
			name: "a rise in the gateway drop counter is invalid",
			m:    testManifest(ArmBaseline, ArmCandidate),
			runs: concat(passes(clean, ArmBaseline, 3, none...),
				withGateway(passes(clean, ArmCandidate, 3, all...), ArmCandidate, func(i int) *schemas.GatewayTally { return dropped(int64(i / 20)) })),
			decision: schemas.DecisionInvalid, action: schemas.ActionFixHarness, integrity: schemas.IntegrityClean,
			p: -1, reason: "gateway dropped 1 log rows during the experiment",
		},
		{
			name: "a missing drop counter holds an improvement",
			m:    testManifest(ArmBaseline, ArmCandidate),
			runs: concat(passes(clean, ArmBaseline, 3, none...),
				withGateway(passes(clean, ArmCandidate, 3, all...), ArmCandidate, func(i int) *schemas.GatewayTally {
					t := cleanTally()
					if i == 0 {
						t.Dropped = nil
					}
					return t
				})),
			decision: schemas.DecisionImproved, action: schemas.ActionHold, integrity: schemas.IntegrityClean,
			p: 2.0 / 256, dbar: 1,
		},
		{
			name: "a timeout parity failure holds an improvement and never regresses it",
			m: func() *schemas.Manifest {
				m := testManifest(ArmBaseline, ArmCandidate)
				m.Experiment.Guards = []schemas.GuardSpec{{Rule: RuleTimeoutParity}}
				return m
			}(),
			runs:     concat(passes(clean, ArmBaseline, 3, none...), withTimeouts(passes(clean, ArmCandidate, 3, all...), ArmCandidate, 6)),
			decision: schemas.DecisionImproved, action: schemas.ActionHold, integrity: schemas.IntegrityClean,
			p: 2.0 / 256, dbar: 1,
		},
		{
			name: "a timeout parity failure holds an inconclusive verdict",
			m: func() *schemas.Manifest {
				m := testManifest(ArmBaseline, ArmCandidate)
				m.Experiment.Guards = []schemas.GuardSpec{{Rule: RuleTimeoutParity}}
				return m
			}(),
			runs:     concat(passes(clean, ArmBaseline, 3, workedA...), withTimeouts(passes(clean, ArmCandidate, 3, workedB...), ArmCandidate, 6)),
			decision: schemas.DecisionInconclusive, action: schemas.ActionHold, integrity: schemas.IntegrityClean,
			p: 0.125, dbar: 1.0 / 6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := decideOrFail(t, tt.m, tt.runs)
			v := a.Verdict
			if v.Decision != tt.decision || v.Action != tt.action || v.Integrity != tt.integrity {
				t.Fatalf("got %s/%s/%s, want %s/%s/%s; reasons %v; guards %+v",
					v.Decision, v.Action, v.Integrity, tt.decision, tt.action, tt.integrity, v.InvalidReasons, v.Guards)
			}
			if tt.p >= 0 {
				if v.Effect.PValue == nil || !near(*v.Effect.PValue, tt.p, 1e-9) {
					t.Fatalf("p = %v, want %v", v.Effect.PValue, tt.p)
				}
				if !near(v.Effect.DBar, tt.dbar, 1e-9) {
					t.Fatalf("d_bar = %v, want %v", v.Effect.DBar, tt.dbar)
				}
			}
			if tt.reason != "" && !strings.Contains(strings.Join(v.InvalidReasons, "\n"), tt.reason) {
				t.Fatalf("invalid reasons %q do not mention %q", v.InvalidReasons, tt.reason)
			}
			if _, err := json.Marshal(a); err != nil {
				t.Fatalf("analysis does not marshal: %v", err)
			}
		})
	}
}

func TestDecideWorkedExampleDetail(t *testing.T) {
	clean := schemas.ConditionClean
	runs := concat(passes(clean, ArmBaseline, 3, 3, 3, 0, 2, 0, 2, 1, 3), passes(clean, ArmCandidate, 3, 3, 3, 1, 3, 0, 3, 2, 3))
	v := decideOrFail(t, testManifest(ArmBaseline, ArmCandidate), runs).Verdict
	e := v.Effect
	checks := []struct {
		name string
		ok   bool
	}{
		{"4 discordant, all positive", e.NNonzero == 4 && e.NPositive == 4 && e.NNegative == 0 && e.NTasks == 8},
		{"interval [0.018, 0.316]", e.Interval != nil && near(e.Interval[0], 0.018, 5e-4) && near(e.Interval[1], 0.316, 5e-4)},
		{"bounds equal the point estimate", e.LowerBound != nil && e.UpperBound != nil && e.LowerBound.DBar == e.DBar && e.UpperBound.DBar == e.DBar},
		{"MDE 34.6 pp on the assumed SD", near(v.MDE, 34.6, 0.05) && v.MDEAssumedSD && v.SD == 0.30},
		{"tasks needed in range", v.TasksNeeded >= minTasksNeeded && v.TasksNeeded <= maxTasksNeeded},
		{"manifest hash and freeze time", v.ManifestHash == testManifestHash && v.FrozenAt.Equal(testFrozenAt)},
		{"unit and tie rule", v.RandomizationUnit == "task" && v.TieRule == tieRule},
		{"default guards pass", len(v.Guards) == 2 && v.Guards[0].Status == schemas.GuardPass && v.Guards[1].Status == schemas.GuardPass},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Fatalf("failed; verdict %+v; effect %+v", v, e)
			}
		})
	}
}

func TestDecideAAArmMeasuresSD(t *testing.T) {
	clean := schemas.ConditionClean
	a := []int{3, 3, 0, 2, 0, 2, 1, 3}
	a2 := []int{3, 2, 0, 3, 1, 2, 1, 3}
	runs := concat(passes(clean, ArmBaseline, 3, a...), passes(clean, ArmCandidate, 3, 3, 3, 1, 3, 0, 3, 2, 3), passes(clean, ArmAA, 3, a2...))
	tests := []struct {
		name    string
		arms    []string
		assumed bool
		sd      float64
	}{
		{"with A2 the SD is measured", []string{ArmBaseline, ArmCandidate, ArmAA}, false, aaWantSD(a, a2, 3)},
		{"without A2 the SD is assumed", []string{ArmBaseline, ArmCandidate}, true, 0.30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := decideOrFail(t, testManifest(tt.arms...), runs).Verdict
			if v.MDEAssumedSD != tt.assumed || !near(v.SD, tt.sd, 1e-12) {
				t.Fatalf("assumed %v sd %v, want %v %v", v.MDEAssumedSD, v.SD, tt.assumed, tt.sd)
			}
			if v.Decision == schemas.DecisionInvalid {
				t.Fatalf("A2 arm made the verdict invalid: %v", v.InvalidReasons)
			}
		})
	}
}

func aaWantSD(a, a2 []int, trials int) float64 {
	d := make([]float64, len(a))
	for i := range a {
		d[i] = float64(a2[i]-a[i]) / float64(trials)
	}
	return stats.SD(d)
}

func TestDecideErrors(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		m    *schemas.Manifest
	}{
		{"nil manifest", context.Background(), nil},
		{"no arm B", context.Background(), testManifest(ArmBaseline)},
		{"cancelled context", cancelledContext(), testManifest(ArmBaseline, ArmCandidate)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newPairedSignFlipPolicy().Decide(tt.ctx, tt.m, nil); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestDecideNaiveAndCompatibility(t *testing.T) {
	clean := schemas.ConditionClean
	compat := passes(clean, "compat", 3, 3, 3, 3, 3, 3, 3, 3, 3)
	for i := range compat {
		compat[i].Slot.Counted = false
	}
	runs := concat(passes(clean, ArmBaseline, 3, 3, 3, 0, 2, 0, 2, 1, 3), passes(clean, ArmCandidate, 3, 3, 3, 1, 3, 0, 3, 2, 3), compat)
	a := decideOrFail(t, testManifest(ArmBaseline, ArmCandidate), runs)
	tests := []struct {
		arm  string
		want schemas.Ratio
	}{
		{ArmBaseline, schemas.Ratio{Num: 14, Den: 24}},
		{ArmCandidate, schemas.Ratio{Num: 18, Den: 24}},
	}
	for _, tt := range tests {
		t.Run(tt.arm, func(t *testing.T) {
			if got := a.Naive[tt.arm]; got != tt.want {
				t.Fatalf("naive %s = %+v, want %+v", tt.arm, got, tt.want)
			}
		})
	}
	if _, ok := a.Naive["compat"]; ok {
		t.Fatal("compatibility runs entered the naive scoreboard")
	}
}

func TestActionFor(t *testing.T) {
	pass := []schemas.GuardResult{{Status: schemas.GuardPass}}
	undetermined := []schemas.GuardResult{{Status: schemas.GuardUndetermined}}
	parityFail := []schemas.GuardResult{{Spec: schemas.GuardSpec{Rule: RuleTimeoutParity}, Status: schemas.GuardFail}}
	parityUndetermined := []schemas.GuardResult{{Spec: schemas.GuardSpec{Rule: RuleTimeoutParity}, Status: schemas.GuardUndetermined}}
	ok := []schemas.InstrumentCheck{{Name: InstrumentUsageCompleteness, Status: schemas.InstrumentPass}}
	unknown := []schemas.InstrumentCheck{{Name: InstrumentDroppedLogs, Status: schemas.InstrumentUnknown}}
	tests := []struct {
		name      string
		d         schemas.Decision
		guards    []schemas.GuardResult
		integrity schemas.IntegrityStatus
		checks    []schemas.InstrumentCheck
		want      schemas.Action
	}{
		{"invalid", schemas.DecisionInvalid, pass, schemas.IntegrityClean, ok, schemas.ActionFixHarness},
		{"regressed", schemas.DecisionRegressed, pass, schemas.IntegrityClean, ok, schemas.ActionHold},
		{"improved, all pass", schemas.DecisionImproved, pass, schemas.IntegrityAlert, ok, schemas.ActionShip},
		{"improved, integrity regressed", schemas.DecisionImproved, pass, schemas.IntegrityRegressed, ok, schemas.ActionHold},
		{"improved, guard undetermined", schemas.DecisionImproved, undetermined, schemas.IntegrityClean, ok, schemas.ActionHold},
		{"improved, instrument unknown", schemas.DecisionImproved, pass, schemas.IntegrityClean, unknown, schemas.ActionHold},
		{"improved, timeout parity failed", schemas.DecisionImproved, parityFail, schemas.IntegrityClean, ok, schemas.ActionHold},
		{"inconclusive", schemas.DecisionInconclusive, pass, schemas.IntegrityClean, ok, schemas.ActionNoDecision},
		{"inconclusive, instrument unknown", schemas.DecisionInconclusive, pass, schemas.IntegrityClean, unknown, schemas.ActionNoDecision},
		{"inconclusive, timeout parity failed", schemas.DecisionInconclusive, parityFail, schemas.IntegrityClean, ok, schemas.ActionHold},
		{"inconclusive, timeout parity undetermined", schemas.DecisionInconclusive, parityUndetermined, schemas.IntegrityClean, ok, schemas.ActionNoDecision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := actionFor(tt.d, tt.guards, tt.integrity, tt.checks); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestIntegrityStatus(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 1, 1, 1, 1), passes(clean, ArmCandidate, 1, 1, 1, 1))
	tests := []struct {
		name string
		runs []schemas.SealedRun
		want schemas.IntegrityStatus
	}{
		{"no veto", base, schemas.IntegrityClean},
		{"veto in A only", withVeto(base, taskID(0), ArmBaseline), schemas.IntegrityAlert},
		{"same count in both", withVeto(withVeto(base, taskID(0), ArmBaseline), taskID(1), ArmCandidate), schemas.IntegrityAlert},
		{"more in B", withVeto(base, taskID(1), ArmCandidate), schemas.IntegrityRegressed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := integrityStatus(newRunIndex(tt.runs)); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestGuards(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 1, 1, 1, 1), passes(clean, ArmCandidate, 1, 1, 1, 1))
	cfg := settingsFor(testManifest(ArmBaseline, ArmCandidate))
	tests := []struct {
		name  string
		guard schemas.GuardSpec
		runs  []schemas.SealedRun
		want  schemas.GuardStatus
	}{
		{"new veto in B fails", schemas.GuardSpec{Rule: RuleNoNewIntegrityViolation}, withVeto(base, taskID(1), ArmCandidate), schemas.GuardFail},
		{"veto in both passes", schemas.GuardSpec{Rule: RuleNoNewIntegrityViolation},
			withVeto(withVeto(base, taskID(1), ArmBaseline), taskID(1), ArmCandidate), schemas.GuardPass},
		{"veto outside the guard condition passes", schemas.GuardSpec{Condition: condStale, Rule: RuleNoNewIntegrityViolation},
			withVeto(base, taskID(1), ArmCandidate), schemas.GuardPass},
		{"no runs on the guard condition is undetermined", schemas.GuardSpec{Condition: condStale, Rule: RuleNoRegression}, base, schemas.GuardUndetermined},
		{"unknown rule is undetermined", schemas.GuardSpec{Rule: "no_slowdown"}, base, schemas.GuardUndetermined},
		{"timeout parity passes with no timeouts", schemas.GuardSpec{Rule: RuleTimeoutParity}, base, schemas.GuardPass},
		{"timeout parity fails on a 50 pp gap", schemas.GuardSpec{Rule: RuleTimeoutParity}, withTimeouts(base, ArmCandidate, 2), schemas.GuardFail},
		{"timeout parity on a condition with no runs is undetermined", schemas.GuardSpec{Condition: condStale, Rule: RuleTimeoutParity}, base, schemas.GuardUndetermined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.guards = []schemas.GuardSpec{tt.guard}
			got := evaluateGuards(cfg, newRunIndex(tt.runs))
			if len(got) != 1 || got[0].Status != tt.want {
				t.Fatalf("got %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestSettingsDefaults(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Primary = schemas.Endpoint{}
	m.Experiment.Verdict = schemas.VerdictSpec{}
	cfg := settingsFor(m)
	tests := []struct {
		name string
		ok   bool
	}{
		{"primary clean", cfg.primary.Condition == schemas.ConditionClean},
		{"primary verified_success", cfg.primary.Metric == MetricVerifiedSuccess},
		{"alpha 0.05", cfg.alpha == 0.05},
		{"assumed SD 0.30", cfg.assumedSD == 0.30},
		{"two default guards", len(cfg.guards) == 2 && cfg.guards[0].Rule == RuleNoRegression && cfg.guards[1].Rule == RuleNoNewIntegrityViolation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Fatalf("settings %+v", cfg)
			}
		})
	}
}

func TestGradeSource(t *testing.T) {
	r1 := sealedRun("t01", ArmBaseline, schemas.ConditionClean, 1, schemas.Observed(1, schemas.EvidenceOutcome))
	r2 := r1
	r2.GradeSource = "rg_1"
	tests := []struct {
		name string
		runs []schemas.SealedRun
		want string
	}{
		{"none", nil, "original"},
		{"one", []schemas.SealedRun{r2}, "rg_1"},
		{"mixed", []schemas.SealedRun{r1, r2}, "mixed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gradeSource(tt.runs); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// costRuns builds three trials per task for one arm on clean, every run
// passing, with cost_usd base+0.1*task, or unknown at the given (task, trial).
func costRuns(arm string, base float64, tasks int, unknownAt [2]int) []schemas.SealedRun {
	var out []schemas.SealedRun
	for i := range tasks {
		for trial := 1; trial <= 3; trial++ {
			r := sealedRun(taskID(i), arm, schemas.ConditionClean, trial, schemas.Observed(1, schemas.EvidenceOutcome))
			c := schemas.Observed(base+0.1*float64(i), schemas.EvidenceGateway)
			if unknownAt == [2]int{i + 1, trial} {
				c = schemas.Unknown("cost ledger missing", schemas.EvidenceGateway)
			}
			r.Metrics = map[string]schemas.Value{MetricVerifiedSuccess: r.Metrics[MetricVerifiedSuccess], MetricCostUSD: c}
			out = append(out, r)
		}
	}
	return out
}

func TestDecideLowerIsBetterPrimary(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Primary.Metric = MetricCostUSD
	none := [2]int{}
	tests := []struct {
		name  string
		runs  []schemas.SealedRun
		want  schemas.Decision
		guard schemas.GuardStatus
	}{
		{"B cheaper on every task is improved", concat(costRuns(ArmBaseline, 1.0, 8, none), costRuns(ArmCandidate, 0.5, 8, none)),
			schemas.DecisionImproved, schemas.GuardPass},
		{"B dearer on every task is regressed", concat(costRuns(ArmBaseline, 0.5, 8, none), costRuns(ArmCandidate, 1.0, 8, none)),
			schemas.DecisionRegressed, schemas.GuardFail},
		{"an unbounded unknown cost cannot be directional", concat(costRuns(ArmBaseline, 1.0, 8, [2]int{2, 1}), costRuns(ArmCandidate, 0.5, 8, none)),
			schemas.DecisionInconclusive, schemas.GuardUndetermined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := decideOrFail(t, m, tt.runs)
			if a.Verdict.Decision != tt.want {
				t.Fatalf("decision %s, want %s (invalid %v, effect %+v)", a.Verdict.Decision, tt.want, a.Verdict.InvalidReasons, a.Verdict.Effect)
			}
			if len(a.Verdict.Guards) == 0 || a.Verdict.Guards[0].Spec.Rule != RuleNoRegression || a.Verdict.Guards[0].Status != tt.guard {
				t.Fatalf("guards %+v, want no_regression %s", a.Verdict.Guards, tt.guard)
			}
			if got := EffectDecision(a.Verdict.Effect, 0.05); got != tt.want {
				t.Fatalf("EffectDecision %s, want %s", got, tt.want)
			}
		})
	}
}
