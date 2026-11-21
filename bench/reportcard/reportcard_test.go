package reportcard

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeSource is a leaderboard.Source over in-memory fixture runs.
type fakeSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]leaderboard.Run
	files     map[string][]byte // run id -> metrics.json
	decisions map[string]schemas.Decision
}

func (f *fakeSource) Experiments() []string {
	var out []string
	for id := range f.manifests {
		out = append(out, id)
	}
	return out
}
func (f *fakeSource) Manifest(id string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[id]
	return m, ok
}
func (f *fakeSource) Runs(id string) []leaderboard.Run { return f.runs[id] }
func (f *fakeSource) RunFile(runID, name string) ([]byte, error) {
	if b, ok := f.files[runID]; ok && name == fileMetrics {
		return b, nil
	}
	return nil, fs.ErrNotExist
}
func (f *fakeSource) LatestDecision(id string) (schemas.Decision, bool) {
	d, ok := f.decisions[id]
	return d, ok
}

type fixRun struct {
	arm, task, cond string
	trial           int
	outcome         schemas.Outcome
	metrics         map[string]float64
	attempt         int
}

func newSource() *fakeSource {
	return &fakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]leaderboard.Run{}, files: map[string][]byte{}, decisions: map[string]schemas.Decision{}}
}

func (f *fakeSource) add(exp, suite string, arms []string, runs []fixRun) {
	m := &schemas.Manifest{ExpID: exp, SuiteID: suite, SuiteVersion: "1", Arms: map[string]schemas.ArmManifest{}}
	for _, a := range arms {
		m.Arms[a] = schemas.ArmManifest{Label: a, ConfigHash: "h-" + a}
	}
	f.manifests[exp] = m
	for i, r := range runs {
		cond := r.cond
		if cond == "" {
			cond = schemas.ConditionClean
		}
		trial := max(r.trial, 1)
		slot := fmt.Sprintf("%s-%s-%s-%s-%d", exp, r.arm, r.task, cond, trial)
		id := fmt.Sprintf("r_%s_%d", exp, i)
		vals := map[string]schemas.Value{}
		for k, v := range r.metrics {
			vals[k] = schemas.Observed(v, schemas.EvidenceOutcome)
		}
		b, _ := json.Marshal(vals)
		f.files[id] = b
		f.runs[exp] = append(f.runs[exp], leaderboard.Run{
			Slot:   schemas.Slot{SlotID: slot, ExpID: exp, Suite: suite, Task: r.task, Arm: r.arm, Condition: cond, Trial: trial, Counted: true},
			Run:    schemas.Run{RunID: id, SlotID: slot, Outcome: r.outcome, Attempt: max(r.attempt, 1)},
			Sealed: true,
		})
	}
}

func pass(arm, task string, trial int, m map[string]float64) fixRun {
	return fixRun{arm: arm, task: task, trial: trial, outcome: schemas.OutcomePass, metrics: m}
}
func fail(arm, task string, trial int, m map[string]float64) fixRun {
	return fixRun{arm: arm, task: task, trial: trial, outcome: schemas.OutcomeFail, metrics: m}
}

func abEval(children ...schemas.EvaluationChild) *schemas.Evaluation {
	return &schemas.Evaluation{
		ID: "ev_1", Status: StatusDone,
		Spec: schemas.EvaluationSpec{Mode: "versions", Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "agents/codex.yaml@v1", Model: "m1"},
			{Label: "B", Agent: "codex", Version: 2, Model: "m1"},
		}, Faults: []string{"clean", FaultModelDown}},
		Children: children,
	}
}

func TestBuildABReport(t *testing.T) {
	src := newSource()
	cost := func(c, tok, steps, tools, wall float64) map[string]float64 {
		return map[string]float64{metricCostUSD: c, metricTokens: tok, metricSteps: steps, metricToolCalls: tools, metricWallTime: wall}
	}
	var tb2 []fixRun
	tasks := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t10", "t11"} // 8: enough to stay significant after Holm over 3 benchmarks
	for _, task := range tasks {
		for trial := 1; trial <= 2; trial++ {
			tb2 = append(tb2, fail("a", task, trial, cost(0.1, 100, 10, 5, 60)))
			tb2 = append(tb2, pass("b", task, trial, cost(0.2, 200, 20, 8, 90)))
		}
	}
	// model-down fault: A solves none, B solves half.
	tb2 = append(tb2,
		fixRun{arm: "a", task: "t1", cond: "outage-model-down", outcome: schemas.OutcomeFail},
		fixRun{arm: "b", task: "t1", cond: "outage-model-down", outcome: schemas.OutcomePass},
		fixRun{arm: "b", task: "t2", cond: "outage-model-down", outcome: schemas.OutcomeFail},
	)
	// an infra error is not graded; a retry after aborted_infra counts.
	tb2 = append(tb2,
		fixRun{arm: "a", task: "t9", outcome: schemas.OutcomeInfraError},
		fixRun{arm: "a", task: "t8", outcome: schemas.OutcomeAbortedInfra, attempt: 1},
		fixRun{arm: "a", task: "t8", outcome: schemas.OutcomePass, attempt: 2},
	)
	src.add("exp_tb2", "hd-harbor-tb2", []string{"a", "b"}, tb2)

	// ImpossibleBench: cheating_rate observed.
	src.add("exp_ib", "hd-swe-ib-lcb", []string{"A", "B"}, []fixRun{
		fail("A", "i1", 1, map[string]float64{metricCheatingRate: 1}),
		fail("A", "i2", 1, map[string]float64{metricCheatingRate: 0}),
		fail("B", "i1", 1, map[string]float64{metricCheatingRate: 0}),
		fail("B", "i2", 1, map[string]float64{metricCheatingRate: 0}),
	})
	// AgentDojo attack condition.
	src.add("exp_adj", "agentdojo-banking", []string{"A", "B"}, []fixRun{
		pass("A", "u1", 1, nil), pass("B", "u1", 1, nil),
		{arm: "A", task: "u1", cond: "important_instructions-it1", outcome: schemas.OutcomeFail, metrics: map[string]float64{metricAttackSucceded: 1}},
		{arm: "A", task: "u2", cond: "important_instructions-it1", outcome: schemas.OutcomePass, metrics: map[string]float64{metricAttackSucceded: 0}},
		{arm: "B", task: "u1", cond: "important_instructions-it1", outcome: schemas.OutcomePass, metrics: map[string]float64{metricAttackSucceded: 0}},
	})

	ev := abEval(
		schemas.EvaluationChild{Benchmark: "hd-harbor-tb2", ExperimentID: "exp_tb2", Status: "done"},
		schemas.EvaluationChild{Benchmark: "hd-swe-ib-lcb", ExperimentID: "exp_ib", Status: "done"},
		schemas.EvaluationChild{Benchmark: "agentdojo-banking", ExperimentID: "exp_adj", Status: "done"},
		schemas.EvaluationChild{Benchmark: "voicebench", Skipped: "agent codex cannot run benchmark voicebench: needs voice"},
	)
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Arms) != 2 || len(rep.Benchmarks) != 3 {
		t.Fatalf("arms %d benchmarks %d", len(rep.Arms), len(rep.Benchmarks))
	}
	b := rep.Benchmarks[0]
	if b.Capability != "terminal" || b.ExperimentID != "exp_tb2" {
		t.Fatalf("row %+v", b)
	}
	a, bb := b.Arms["A"], b.Arms["B"]
	// A: 16 fails + t8 retry pass = 17 runs, 1 success.
	if a.Runs != 17 || a.Score != round(1.0/17) || bb.Runs != 16 || bb.Score != 1 {
		t.Fatalf("scores A %+v B %+v", a, bb)
	}
	if !(a.CILow <= a.Score && a.Score <= a.CIHigh) || bb.CIHigh != 1 || bb.CILow <= 0.5 {
		t.Fatalf("intervals A %+v B %+v", a, bb)
	}
	if deref(bb.CostUSD) != 3.2 || deref(bb.Tokens) != 3200 || bb.CostRuns != 16 || bb.Tasks != 8 || bb.Steps != 20 || bb.ToolCalls != 8 || bb.WallS != 90 {
		t.Fatalf("B columns %+v", bb)
	}
	if b.Verdict == nil || b.Verdict.Winner != "B" || !b.Verdict.Significant || b.Verdict.Delta <= 0 {
		t.Fatalf("verdict %+v", b.Verdict)
	}
	if rep.Overall == nil || rep.Overall.Wins["B"] != 1 {
		t.Fatalf("overall %+v", rep.Overall)
	}

	// capabilities: safety pools ImpossibleBench and AgentDojo clean runs.
	var safetyCap *Capability
	for i := range rep.Capabilities {
		if rep.Capabilities[i].Capability == "safety" {
			safetyCap = &rep.Capabilities[i]
		}
	}
	if safetyCap == nil || len(safetyCap.Benchmarks) != 2 || safetyCap.Arms["A"].Runs != 3 || safetyCap.Arms["A"].Score != round(1.0/3) {
		t.Fatalf("safety capability %+v", safetyCap)
	}

	// reliability: model-down on tb2 only.
	if len(rep.Reliability) != 1 || rep.Reliability[0].Fault != FaultModelDown {
		t.Fatalf("reliability %+v", rep.Reliability)
	}
	r := rep.Reliability[0]
	if r.Arms["A"].Score != 0 || r.Arms["A"].Drop != round(-1.0/17) || r.Arms["B"].Score != 0.5 || r.Arms["B"].Drop != -0.5 {
		t.Fatalf("reliability arms %+v", r.Arms)
	}

	if rep.Safety.CheatRate["A"] != 0.5 || rep.Safety.CheatRate["B"] != 0 {
		t.Fatalf("cheat rate %+v", rep.Safety.CheatRate)
	}
	if rep.Safety.AttackResisted["A"] != 0.5 || rep.Safety.AttackResisted["B"] != 1 {
		t.Fatalf("attack resisted %+v", rep.Safety.AttackResisted)
	}

	// The JSON carries the contract's keys.
	data, _ := json.Marshal(rep)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	for _, k := range []string{"evaluation", "arms", "capabilities", "benchmarks", "reliability", "safety", "overall"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("report JSON lacks %q: %s", k, data)
		}
	}
}

func TestBuildSingleArmAndFilters(t *testing.T) {
	src := newSource()
	src.add("exp_1", "longmemeval", []string{"main"}, []fixRun{pass("main", "q1", 1, nil), fail("main", "q2", 1, nil)})
	src.add("exp_bad", "tau2", []string{"main"}, []fixRun{pass("main", "q1", 1, nil)})
	src.decisions["exp_bad"] = schemas.DecisionInvalid
	ev := &schemas.Evaluation{ID: "ev_2", Status: StatusDone, Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "m"}}},
		Children: []schemas.EvaluationChild{
			{Benchmark: "longmemeval", ExperimentID: "exp_1"},
			{Benchmark: "tau2", ExperimentID: "exp_bad"},
			{Benchmark: "locomo", ExperimentID: ""},
		}}
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Overall != nil || rep.Benchmarks[0].Verdict != nil {
		t.Fatal("single arm has no verdict")
	}
	if got := rep.Benchmarks[0].Arms["A"]; got.Runs != 2 || got.Score != 0.5 || rep.Benchmarks[0].Capability != "memory" {
		t.Fatalf("row %+v", rep.Benchmarks[0])
	}
	if rep.Benchmarks[1].Arms["A"].Runs != 0 || rep.Benchmarks[2].Arms["A"].Runs != 0 {
		t.Fatalf("invalid verdict and missing experiment add no runs: %+v", rep.Benchmarks)
	}
	// memory capability pools longmemeval and locomo (runs 2 + 0).
	for _, c := range rep.Capabilities {
		if c.Capability == "memory" && (c.Arms["A"].Runs != 2 || len(c.Benchmarks) != 2) {
			t.Fatalf("memory %+v", c)
		}
	}
}

func TestSelectForAgent(t *testing.T) {
	now := time.Now()
	arm := func(label, agent string, v int, model string) schemas.EvaluationArm {
		return schemas.EvaluationArm{Label: label, Agent: agent, Version: v, Model: model}
	}
	evals := []schemas.Evaluation{
		{ID: "old", Status: StatusDone, CreatedAt: now.Add(-2 * time.Hour), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "codex", 1, "m1")}}},
		{ID: "new", Status: StatusDone, CreatedAt: now.Add(-time.Hour), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "agents/codex.yaml@v2", 0, "m1"), arm("B", "codex", 1, "m2")}}},
		{ID: "live", Status: StatusRunning, CreatedAt: now, Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "codex", 3, "m1")}},
			Children: []schemas.EvaluationChild{{Benchmark: "x", ExperimentID: "exp"}}},
	}
	cases := []struct {
		version int
		model   string
		want    string
		labels  int
	}{
		{0, "", "new", 2},
		{1, "m1", "old", 1},
		{2, "", "new", 1},
		{3, "", "live", 1},
		{4, "", "", 0},
	}
	for _, c := range cases {
		ev, labels := SelectForAgent(evals, "codex", c.version, c.model)
		got := ""
		if ev != nil {
			got = ev.ID
		}
		if got != c.want || len(labels) != c.labels {
			t.Errorf("v%d %q: got %q %v, want %q", c.version, c.model, got, labels, c.want)
		}
	}
	if id, v := AgentRef("agents/my-bot.yaml@v12"); id != "my-bot" || v != 12 {
		t.Fatalf("AgentRef %q %d", id, v)
	}
}

func TestDefaults(t *testing.T) {
	for cond, want := range map[string]string{"clean": "", "model-down": FaultModelDown, "outage-full": FaultModelDown, "broken-tool": FaultBrokenTool, "misleading-instruction": FaultMisleading, "stale-notes": FaultMisleading, "important_instructions": ""} {
		if got := DefaultFault(cond); got != want {
			t.Errorf("fault %q = %q, want %q", cond, got, want)
		}
	}
	for b, want := range map[string]string{"hd-harbor-tb2": "terminal", "hd-swe-verified": "coding", "agentdojo-slack": "safety", "browsecomp-plus-small": "research", "marble-coding": "team", "unknown-x": "coding"} {
		if got := DefaultCapability(b); got != want {
			t.Errorf("capability %q = %q, want %q", b, got, want)
		}
	}
	if CapabilityFor("my-pack", "tool_use") != "support" {
		t.Error("track tool_use is support")
	}
}

// TestBuildScoreModeHidesRepeatArm: score mode runs the one arm plus a hidden
// repeat B; the report shows only A, with no A/B verdict, even when B wins.
func TestBuildScoreModeHidesRepeatArm(t *testing.T) {
	src := newSource()
	var runs []fixRun
	for i := 1; i <= 8; i++ {
		task := fmt.Sprintf("t%d", i)
		runs = append(runs, fail("A", task, 1, nil), pass("B", task, 1, nil))
	}
	src.add("exp_s", "hd-harbor-tb2", []string{"A", "B"}, runs)
	ev := &schemas.Evaluation{ID: "ev_s", Status: StatusDone,
		Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "codex", Model: "m"},
			{Label: "B", Agent: "codex", Model: "m"}, // even if a stored spec lists the repeat
		}},
		Children: []schemas.EvaluationChild{{Benchmark: "hd-harbor-tb2", ExperimentID: "exp_s"}}}
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Arms) != 1 || rep.Arms[0].Label != "A" || rep.Mode != "score" {
		t.Fatalf("arms %+v mode %q", rep.Arms, rep.Mode)
	}
	if rep.Overall != nil || rep.Benchmarks[0].Verdict != nil {
		t.Fatalf("score mode has no verdict: %+v %+v", rep.Overall, rep.Benchmarks[0].Verdict)
	}
	b := rep.Benchmarks[0]
	if _, ok := b.Arms["B"]; ok || b.Arms["A"].Runs != 8 || b.Arms["A"].Score != 0 {
		t.Fatalf("row %+v", b.Arms)
	}
	for _, c := range rep.Capabilities {
		if _, ok := c.Arms["B"]; ok {
			t.Fatalf("capability shows B: %+v", c)
		}
	}
}

func deref(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

// TestCostNeverZeroWhenUnpriced (N2): a run whose cost is unknown (an
// unpriced model) makes the benchmark's and the capability's cost not
// known, with the count of priced runs and the reason, never a partial sum
// or $0.00.
func TestCostNeverZeroWhenUnpriced(t *testing.T) {
	src := newSource()
	src.add("exp_1", "hd-harbor-tb2", []string{"A", "B"}, []fixRun{
		pass("A", "t1", 1, map[string]float64{metricCostUSD: 0.5, metricTokens: 10}),
		pass("A", "t2", 1, nil),
		fail("A", "t3", 1, nil),
		pass("B", "t1", 1, map[string]float64{metricCostUSD: 0.5, metricTokens: 10}),
	})
	unpriced, _ := json.Marshal(map[string]schemas.Value{
		metricCostUSD: schemas.Unknown("1 of 1 cost rows unpriced", schemas.EvidenceGateway),
		metricTokens:  schemas.Observed(20, schemas.EvidenceGateway),
	})
	src.files["r_exp_1_1"], src.files["r_exp_1_2"] = unpriced, unpriced
	ev := abEval(schemas.EvaluationChild{Benchmark: "hd-harbor-tb2", ExperimentID: "exp_1", Status: "done"})
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	a := rep.Benchmarks[0].Arms["A"]
	if a.CostUSD != nil || a.CostRuns != 1 || a.Runs != 3 || a.CostNote != "1 of 1 cost rows unpriced" {
		t.Fatalf("A cost = %v runs %d/%d note %q, want not known, 1 of 3 priced", a.CostUSD, a.CostRuns, a.Runs, a.CostNote)
	}
	if deref(a.Tokens) != 50 || a.TokenRuns != 3 {
		t.Fatalf("A tokens = %v (%d runs)", a.Tokens, a.TokenRuns)
	}
	if b := rep.Benchmarks[0].Arms["B"]; deref(b.CostUSD) != 0.5 || b.CostNote != "" {
		t.Fatalf("B cost = %v note %q", b.CostUSD, b.CostNote)
	}
	c := rep.Capabilities[0].Arms["A"]
	if c.CostUSD != nil || c.CostRuns != 1 || c.CostNote == "" {
		t.Fatalf("capability cost = %v runs %d note %q", c.CostUSD, c.CostRuns, c.CostNote)
	}
	raw, _ := json.Marshal(a)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	if v, ok := back["cost_usd"]; !ok || v != nil {
		t.Fatalf("cost_usd JSON = %v, want null", v)
	}
}
