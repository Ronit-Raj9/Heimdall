package reportcard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// gateReport is a one-arm report on one benchmark with per-task results:
// pass[i] is task t<i>'s success (one run each).
func gateReport(label, bench, version string, pass []bool, judge *JudgeState) *Report {
	b := Benchmark{Benchmark: bench, Capability: "support", SuiteVersion: version, Arms: map[string]Metrics{}, Judge: judge, tasks: map[string][]TaskTally{}}
	succ := 0
	for i, p := range pass {
		s := 0
		if p {
			s, succ = 1, succ+1
		}
		b.tasks[label] = append(b.tasks[label], TaskTally{Key: fmt.Sprintf("t%02d", i), Succ: s, Runs: 1})
	}
	m := Metrics{Score: float64(succ) / float64(len(pass)), Runs: len(pass), Tasks: len(pass)}
	b.Arms[label] = m
	return &Report{Benchmarks: []Benchmark{b}, Capabilities: []Capability{{Capability: "support", Benchmarks: []string{bench}, Arms: map[string]Metrics{label: m}}},
		Totals: map[string]Totals{label: {Score: m.Score, Runs: m.Runs}}}
}

func bools(n, passes int) []bool {
	out := make([]bool, n)
	for i := range passes {
		out[i] = true
	}
	return out
}

func gate(rep, base *Report, g schemas.EvaluationGate) *schemas.EvaluationGateResult {
	in := GateInput{Gate: g, Agent: "codex", Candidate: "B", Report: rep, Now: time.Unix(0, 0)}
	if base != nil {
		in.Baseline, in.BaselineArm, in.BaselineReport = &schemas.GateBaseline{Agent: "codex", Version: 1}, "A", base
	}
	return JudgeGate(in)
}

func check(t *testing.T, res *schemas.EvaluationGateResult, capability, rule string) schemas.GateCheck {
	t.Helper()
	for _, c := range res.Checks {
		if c.Capability == capability && c.Rule == rule {
			return c
		}
	}
	t.Fatalf("no %s %s check in %+v", capability, rule, res.Checks)
	return schemas.GateCheck{}
}

func TestRegressionGateNeedsARealDrop(t *testing.T) {
	limit := schemas.EvaluationGate{MaxRegression: 0.05}
	// 8 tasks, 6 passed before and 5 now: a 12 point drop, over the limit,
	// but one task is noise at this size.
	res := gate(gateReport("B", "support@1", "1", bools(8, 5), nil), gateReport("A", "support@1", "1", bools(8, 6), nil), limit)
	c := check(t, res, "support", RuleMaxRegression)
	if res.State != schemas.GateInconclusive || !c.Inconclusive || c.Pass || c.MDE == nil || c.Tasks != 8 {
		t.Fatalf("noise drop: state %s check %+v", res.State, c)
	}
	if !strings.Contains(c.Summary, "could be noise") || !strings.Contains(c.Summary, "can detect a change of about 35%") || !strings.Contains(c.Summary, "needs about") {
		t.Fatalf("summary %q", c.Summary)
	}
	// 40 tasks all passed before, 10 now: a real 75 point drop.
	res = gate(gateReport("B", "support@1", "1", bools(40, 10), nil), gateReport("A", "support@1", "1", bools(40, 40), nil), limit)
	c = check(t, res, "support", RuleMaxRegression)
	if res.State != schemas.GateFail || c.Pass || c.Inconclusive || c.ChangeHigh == nil || *c.ChangeHigh >= 0 {
		t.Fatalf("real drop: state %s check %+v", res.State, c)
	}
	// A real drop smaller than the limit passes.
	res = gate(gateReport("B", "support@1", "1", bools(40, 10), nil), gateReport("A", "support@1", "1", bools(40, 40), nil), schemas.EvaluationGate{MaxRegression: 0.9})
	if res.State != schemas.GatePass {
		t.Fatalf("drop under the limit: %+v", res)
	}
}

func TestRegressionGatePairsSameVersionAndTasks(t *testing.T) {
	limit := schemas.EvaluationGate{MaxRegression: 0.05}
	// The baseline ran 10 tasks, the candidate 15 (5 new, all failing):
	// only the 10 shared tasks are compared, and they are the same.
	cand := gateReport("B", "support@1", "1", append(bools(10, 10), bools(5, 0)...), nil)
	res := gate(cand, gateReport("A", "support@1", "1", bools(10, 10), nil), limit)
	c := check(t, res, "support", RuleMaxRegression)
	if res.State != schemas.GatePass || c.Tasks != 10 || len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "ran 10 of these 15 tasks; 5 new tasks not compared") {
		t.Fatalf("intersection: state %s check %+v", res.State, c)
	}
	// Another version of the benchmark is never compared.
	res = gate(gateReport("B", "support@2", "2", bools(10, 0), nil), gateReport("A", "support@1", "1", bools(10, 10), nil), limit)
	all := check(t, res, CapabilityAll, RuleMaxRegression)
	if res.State != schemas.GateInconclusive || !all.Inconclusive || len(all.Notes) != 1 || !strings.Contains(all.Notes[0], "version 1, this evaluation 2") {
		t.Fatalf("version: state %s check %+v", res.State, all)
	}
}

func TestMinScoreGateRefusesUncalibratedJudge(t *testing.T) {
	g := schemas.EvaluationGate{MinScore: map[string]float64{"support": 0.5}}
	uncal := &JudgeState{State: schemas.CalibrationStateUncalibrated, Label: "judge not calibrated"}
	res := gate(gateReport("B", "support@1", "1", bools(10, 9), uncal), nil, g)
	c := check(t, res, "support", RuleMinScore)
	if res.State != schemas.GateInconclusive || !c.Inconclusive || c.Pass || !strings.Contains(c.Summary, "not calibrated") {
		t.Fatalf("uncalibrated: %s %+v", res.State, c)
	}
	g.AllowUncalibratedJudge = true
	if res = gate(gateReport("B", "support@1", "1", bools(10, 9), uncal), nil, g); res.State != schemas.GatePass || len(res.Checks[0].Notes) != 1 {
		t.Fatalf("allowed: %+v", res)
	}
	g.AllowUncalibratedJudge = false
	controls := &JudgeState{State: schemas.CalibrationStateControls, Label: "calibrated on controls", Trusted: true}
	if res = gate(gateReport("B", "support@1", "1", bools(10, 9), controls), nil, g); res.State != schemas.GatePass {
		t.Fatalf("calibrated on controls: %+v", res)
	}
}

func TestJudgeStateFromStatus(t *testing.T) {
	th := schemas.JudgeCalibrationThresholds{MinLabels: 30}
	cases := []struct {
		st      schemas.CalibrationStatus
		label   string
		trusted bool
	}{
		{schemas.CalibrationStatus{Judged: true, State: schemas.CalibrationStateCalibrated}, "human-calibrated", true},
		{schemas.CalibrationStatus{Judged: true, State: schemas.CalibrationStateControls, SeedItems: 24}, "calibrated on controls", true},
		{schemas.CalibrationStatus{Judged: true, State: schemas.CalibrationStateFewLabels, HumanLabels: 12, Thresholds: th}, "few human labels (12 of 30)", true},
		{schemas.CalibrationStatus{Judged: true, State: schemas.CalibrationStateFewLabels, HumanLabels: 4, Caps: true, Thresholds: th}, "few human labels (4 of 30)", false},
		{schemas.CalibrationStatus{Judged: true, State: schemas.CalibrationStateUncalibrated}, "judge not calibrated", false},
		{schemas.CalibrationStatus{Judged: true, State: schemas.CalibrationStateUnreliable}, "judge unreliable", false},
	}
	for _, c := range cases {
		js := JudgeStateFrom(&c.st)
		if js == nil || js.Label != c.label || js.Trusted != c.trusted {
			t.Fatalf("%s: %+v", c.st.State, js)
		}
	}
	if JudgeStateFrom(&schemas.CalibrationStatus{State: "not_judged"}) != nil {
		t.Fatal("not judged")
	}
}

// A judged benchmark's A/B verdict is capped at unclear unless its judge is
// trusted, and a capped benchmark never counts in the overall verdict.
func TestBuildCapsUncalibratedJudgeVerdict(t *testing.T) {
	build := func(of func(string) *JudgeState) *Report {
		src := newSource()
		var runs []fixRun
		for i := range 10 {
			task := fmt.Sprintf("t%d", i)
			runs = append(runs, fail("a", task, 1, nil), pass("b", task, 1, nil))
		}
		src.add("e1", "support", []string{"a", "b"}, runs)
		src.manifests["e1"].Scenario = map[string]string{"suite.judge": "h"}
		src.manifests["e1"].Arms = map[string]schemas.ArmManifest{"a": {Label: "A", ConfigHash: "h-a"}, "b": {Label: "B", ConfigHash: "h-b"}}
		rep, err := Build(t.Context(), src, abEval(schemas.EvaluationChild{Benchmark: "support@1", ExperimentID: "e1"}), Options{JudgeOf: of})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	rep := build(nil)
	b := rep.Benchmarks[0]
	if b.Judge == nil || b.Judge.State != JudgeStateUnchecked || b.Verdict == nil || !b.Verdict.JudgeCapped || b.Verdict.Significant || b.Verdict.CappedFrom != "B" {
		t.Fatalf("unchecked judge: judge %+v verdict %+v", b.Judge, b.Verdict)
	}
	if !strings.Contains(b.Verdict.Summary, "not a decision") || rep.Overall.Significant || rep.Overall.Tasks != 0 {
		t.Fatalf("summary %q overall %+v", b.Verdict.Summary, rep.Overall)
	}
	rep = build(func(id string) *JudgeState {
		if id != "support" {
			t.Fatalf("judge of %q", id)
		}
		return &JudgeState{State: schemas.CalibrationStateControls, Label: "calibrated on controls", Trusted: true}
	})
	if v := rep.Benchmarks[0].Verdict; v.JudgeCapped || !v.Significant || v.BootLow == nil || v.MDE == nil || !rep.Overall.Significant {
		t.Fatalf("trusted judge: verdict %+v overall %+v", v, rep.Overall)
	}
	if rep.Benchmarks[0].SuiteVersion != "1" {
		t.Fatalf("suite version %q", rep.Benchmarks[0].SuiteVersion)
	}
}
