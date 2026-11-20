package leaderboard

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// matrixFixture builds a manifest with the given arms and their sealed runs:
// pass[arm][task] is the number of passing trials out of trials on that task.
func matrixFixture(arms []string, tasks, trials int, pass func(arm string, task, trial int) bool) (*schemas.Manifest, []schemas.SealedRun) {
	m := &schemas.Manifest{ExpID: "e_mx", SuiteID: "s", SuiteVersion: "1",
		Experiment: schemas.Experiment{Primary: schemas.Endpoint{Condition: "clean", Metric: "verified_success"}},
		Arms:       map[string]schemas.ArmManifest{}}
	for _, a := range arms {
		m.Arms[a] = schemas.ArmManifest{Label: "label " + a, ConfigHash: "h" + a,
			Config: schemas.AgentConfig{ID: "codex", Model: "model-" + a, Adapter: "command", Gateway: schemas.GatewayPolicy{Provider: "openai"}}}
	}
	var runs []schemas.SealedRun
	for task := 1; task <= tasks; task++ {
		for trial := 1; trial <= trials; trial++ {
			for _, a := range arms {
				slot := schemas.Slot{SlotID: fmt.Sprintf("s_%s_%d_%d", a, task, trial), ExpID: m.ExpID, Task: fmt.Sprintf("t%02d", task),
					Arm: a, Condition: "clean", Trial: trial, Counted: true}
				m.Slots = append(m.Slots, slot)
				outcome, y := schemas.OutcomeFail, 0.0
				if pass(a, task, trial) {
					outcome, y = schemas.OutcomePass, 1
				}
				runs = append(runs, schemas.SealedRun{Slot: slot, Run: schemas.Run{RunID: "r_" + slot.SlotID, Outcome: outcome},
					Metrics: map[string]schemas.Value{
						"verified_success": schemas.Observed(y, schemas.EvidenceOutcome),
						MetricCostUSD:      schemas.Observed(0.02, schemas.EvidenceGateway),
						MetricTokens:       schemas.Observed(1000, schemas.EvidenceGateway),
						MetricWallTime:     schemas.Observed(30, schemas.EvidenceOutcome),
					}})
			}
		}
	}
	return m, runs
}

func TestMatrixSmallSuiteCannotSeparate(t *testing.T) {
	m, runs := matrixFixture([]string{"A", "B", "C"}, 3, 2, func(arm string, task, trial int) bool { return arm == "A" })
	mx, err := Matrix(m, runs, false, MatrixQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if mx.Separable || !strings.Contains(mx.Note, "Cannot separate any two arms") || !strings.Contains(mx.Note, "Holm across 3 pairs") {
		t.Fatalf("separable %v note %q", mx.Separable, mx.Note)
	}
	if len(mx.Rows) != 3 || mx.Rows[0].Arm != "A" || mx.Rows[0].Rank != 1 || mx.Rows[0].Tier != TierLeader || mx.Tasks != 3 {
		t.Fatalf("rows %+v", mx.Rows)
	}
	for _, r := range mx.Rows {
		if r.Tier != TierLeader || r.Provider != "openai" || r.Model != "model-"+r.Arm {
			t.Errorf("row %s: tier %d provider %q model %q", r.Arm, r.Tier, r.Provider, r.Model)
		}
	}
	if len(mx.Pairs) != 3 {
		t.Fatalf("%d pairs, want 3", len(mx.Pairs))
	}
	for _, p := range mx.Pairs {
		if p.Significant || !strings.HasPrefix(p.Reason, "cannot separate") {
			t.Errorf("pair %s-%s: %+v", p.ArmX, p.ArmY, p)
		}
	}
	// pass^2 over 2 trials per task: A always passes, B never.
	if v, ok := mx.Rows[0].Reliability.Float(); !ok || v != 1 || mx.Rows[0].Trials != 2 {
		t.Fatalf("leader reliability %+v trials %d", mx.Rows[0].Reliability, mx.Rows[0].Trials)
	}
}

func TestMatrixSeparatesWithEnoughTasks(t *testing.T) {
	// A and B pass everything, C fails everything, on 12 tasks: A and C differ
	// on all 12 (min p 2/4096, times 3 pairs, is below 0.05), A and B never.
	m, runs := matrixFixture([]string{"A", "B", "C"}, 12, 1, func(arm string, task, trial int) bool { return arm != "C" })
	mx, err := Matrix(m, runs, false, MatrixQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !mx.Separable || !strings.Contains(mx.Note, "Holm-adjusted across all 3 pairs") {
		t.Fatalf("separable %v note %q", mx.Separable, mx.Note)
	}
	byArm := map[string]schemas.MatrixRow{}
	for _, r := range mx.Rows {
		byArm[r.Arm] = r
	}
	if byArm["C"].Tier != TierBehind || byArm["C"].Rank != 3 || byArm["C"].PVsLeader == nil {
		t.Fatalf("row C %+v", byArm["C"])
	}
	if byArm["B"].Tier != TierLeader || !strings.Contains(byArm["B"].TierReason, "same success on all 12") {
		t.Fatalf("row B %+v", byArm["B"])
	}
	raw := stats.SignFlip(ones(12)).P
	var ac, bc *schemas.MatrixPair
	for i := range mx.Pairs {
		p := &mx.Pairs[i]
		switch {
		case p.ArmX == "A" && p.ArmY == "C":
			ac = p
		case p.ArmX == "B" && p.ArmY == "C":
			bc = p
		}
	}
	if ac == nil || bc == nil {
		t.Fatalf("pairs %+v", mx.Pairs)
	}
	// Holm over {1, raw, raw}: the two equal smallest get 3*raw, then
	// max(3*raw, 2*raw) keeps it monotone.
	if !ac.Significant || math.Abs(*ac.P-raw) > 1e-12 || math.Abs(*ac.PAdjusted-3*raw) > 1e-12 || ac.MeanDiff != 1 || ac.NNonzero != 12 {
		t.Fatalf("pair A-C %+v (p %v adj %v)", ac, *ac.P, *ac.PAdjusted)
	}
	if !bc.Significant || !strings.HasPrefix(bc.Reason, "B ahead") {
		t.Fatalf("pair B-C %+v", bc)
	}
	if v, ok := byArm["A"].Metrics[MetricCostPerSuccess].Float(); !ok || math.Abs(v-0.02) > 1e-12 {
		t.Fatalf("cost per success %+v", byArm["A"].Metrics[MetricCostPerSuccess])
	}
	if byArm["C"].Metrics[MetricCostPerSuccess].State != schemas.ValueNotApplicable {
		t.Fatalf("C cost per success %+v", byArm["C"].Metrics[MetricCostPerSuccess])
	}
}

func TestMatrixExclusionsAndConditions(t *testing.T) {
	m, runs := matrixFixture([]string{"A", "B"}, 4, 1, func(arm string, task, trial int) bool { return task%2 == 0 })
	runs[0].Problems = []string{"seal does not verify"}
	runs[1].Run.Outcome = schemas.OutcomeInfraError
	runs[2].Problems = []string{"slot s has 2 counted runs (r_1, r_2); exactly one may count"}
	probe := runs[3]
	probe.Slot.Condition = "model-outage"
	runs = append(runs, probe, schemas.SealedRun{Slot: schemas.Slot{Arm: "compat", Task: "t01", Condition: "clean"}, Run: schemas.Run{Outcome: schemas.OutcomePass}})
	mx, err := Matrix(m, runs, true, MatrixQuery{})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]int{}
	for _, x := range mx.Excluded {
		reasons[x.Reason] = x.Runs
	}
	if reasons[ReasonSealMismatch] != 1 || reasons["infra_error"] != 1 || reasons[ReasonDuplicateSlot] != 1 || len(reasons) != 3 {
		t.Fatalf("excluded %+v", mx.Excluded)
	}
	if strings.Join(mx.Conditions, ",") != "clean,model-outage" || !mx.Synthetic || !mx.Rows[0].Synthetic {
		t.Fatalf("conditions %v synthetic %v", mx.Conditions, mx.Synthetic)
	}
	other, err := Matrix(m, runs, false, MatrixQuery{Condition: "model-outage"})
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Rows) != 1 || other.Separable || !strings.Contains(other.Note, "1 arm with graded runs") {
		t.Fatalf("probe matrix rows %d note %q", len(other.Rows), other.Note)
	}
	if _, err := Matrix(m, runs, false, MatrixQuery{K: 1}); err == nil {
		t.Fatal("k 1 accepted")
	}
	if _, err := Matrix(m, runs, false, MatrixQuery{Condition: "a/b"}); err == nil {
		t.Fatal("bad condition accepted")
	}
}

func TestSyntheticArms(t *testing.T) {
	m, runs := matrixFixture([]string{"A", "B", "C"}, 1, 1, func(string, int, int) bool { return true })
	b := m.Arms["B"]
	b.Config.Adapter = adapterScripted
	m.Arms["B"] = b
	read := func(sr *schemas.SealedRun, name string) ([]byte, bool, error) {
		if name != fileLedger {
			return nil, false, fmt.Errorf("unexpected file %s", name)
		}
		switch sr.Slot.Arm {
		case "A":
			return []byte(realOpenAILine), true, nil
		case "C":
			return []byte(scriptedOpenAILine), true, nil
		}
		return nil, false, nil
	}
	got, err := SyntheticArms(context.Background(), m, runs, read)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] || !got["B"] || !got["C"] {
		t.Fatalf("synthetic %v", got)
	}
	m.Experiment.Smoke = true
	if got, _ := SyntheticArms(context.Background(), m, runs, nil); !got["A"] {
		t.Fatalf("smoke experiment not synthetic: %v", got)
	}
}

// ones returns n differences of 1.
func ones(n int) []float64 {
	d := make([]float64, n)
	for i := range d {
		d[i] = 1
	}
	return d
}

// The matrix applies the board's experiment eligibility: an INVALID verdict
// or a live external experiment ranks nothing and says why (finding 1-D-05).
func TestMatrixExperimentEligibility(t *testing.T) {
	pass := func(arm string, task, trial int) bool { return arm == "A" }
	m, runs := matrixFixture([]string{"A", "B"}, 8, 1, pass)
	mx, err := Matrix(m, runs, false, MatrixQuery{Invalid: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(mx.Rows) != 0 || !mx.Invalid || len(mx.Excluded) != 1 || mx.Excluded[0].Reason != ReasonVerdictInvalid || mx.Excluded[0].Runs != 16 {
		t.Fatalf("invalid experiment: rows %d invalid %v excluded %+v", len(mx.Rows), mx.Invalid, mx.Excluded)
	}
	m.Experiment.External = schemas.ExternalLive
	mx, err = Matrix(m, runs, false, MatrixQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mx.Rows) != 0 || len(mx.Excluded) != 1 || mx.Excluded[0].Reason != ReasonExternalLive {
		t.Fatalf("external live: rows %d excluded %+v", len(mx.Rows), mx.Excluded)
	}
}

// The matrix reads one frozen split: held-out tasks stay out of the default
// public matrix and only they enter the held-out one (finding 1-D-02).
func TestMatrixHeldOutSplit(t *testing.T) {
	m, runs := matrixFixture([]string{"A", "B"}, 8, 1, func(arm string, task, trial int) bool { return arm == "A" })
	m.HeldOutTasks = []string{"t07", "t08"}
	pub, err := Matrix(m, runs, false, MatrixQuery{})
	if err != nil {
		t.Fatal(err)
	}
	held, err := Matrix(m, runs, false, MatrixQuery{Split: schemas.SplitHeldOut})
	if err != nil {
		t.Fatal(err)
	}
	if pub.Tasks != 6 || pub.Split != "" || held.Tasks != 2 || held.Split != schemas.SplitHeldOut {
		t.Fatalf("public tasks %d split %q, held-out tasks %d split %q", pub.Tasks, pub.Split, held.Tasks, held.Split)
	}
	if _, err := Matrix(m, runs, false, MatrixQuery{Split: "both"}); err == nil {
		t.Fatal("unknown split accepted")
	}
}

// Three arms on eight tasks with one tie: seven same-direction differences
// separate a pair after Holm across 3 pairs (2/128 times 3 = 0.046875), as
// docs/model-matrix.md now says (finding 1-D-10).
func TestMatrixSevenDifferingTasksSeparate(t *testing.T) {
	m, runs := matrixFixture([]string{"A", "B", "C"}, 8, 1, func(arm string, task, trial int) bool { return arm != "B" || task == 1 })
	mx, err := Matrix(m, runs, false, MatrixQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !mx.Separable {
		t.Fatalf("not separable: %s", mx.Note)
	}
	sig := 0
	for _, p := range mx.Pairs {
		if p.PAdjusted != nil && math.Abs(*p.PAdjusted-0.046875) < 1e-9 && *p.PAdjusted < mx.Alpha {
			sig++
		}
	}
	if sig != 2 {
		t.Fatalf("pairs %+v, want A-B and C-B at adjusted p 0.046875", mx.Pairs)
	}
}

// A matrix pair with more than stats.ExactMaxNonzero differing tasks carries
// its draw count and the note does not call the test exact (audit 1-B-07).
func TestMatrixPairsMarkSampledP(t *testing.T) {
	x, y := &entryCalc{rates: map[string]float64{}}, &entryCalc{rates: map[string]float64{}}
	for i := range 23 {
		task := fmt.Sprintf("t%02d", i)
		x.rates[task], y.rates[task] = 1, 0
	}
	calcs := []*entryCalc{x, y}
	pairs := matrixPairs(calcs, map[*entryCalc]string{x: "A", y: "B"}, DefaultAlpha)
	if len(pairs) != 1 || pairs[0].PDraws != stats.MonteCarloDraws {
		t.Fatalf("pairs %+v", pairs)
	}
	if _, note := separability(calcs, pairs, DefaultAlpha); !strings.Contains(note, "sampled") || strings.Contains(note, "Exact sign-flip") {
		t.Fatalf("note %q", note)
	}
}
