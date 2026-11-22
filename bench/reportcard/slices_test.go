package reportcard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// sliceFixture: 8 tasks, tier enterprise (e1..e4) and free (f1..f3) plus
// one small tier (p1); A passes free and fails enterprise, B passes all.
func sliceFixture(t *testing.T) (*fakeSource, *schemas.Evaluation) {
	t.Helper()
	src := newSource()
	var runs []fixRun
	meta := map[string]map[string]string{}
	for _, task := range []string{"e1", "e2", "e3", "e4"} {
		runs = append(runs, fail("a", task, 1, nil), pass("b", task, 1, nil))
		meta[task] = map[string]string{"tier": "enterprise", "lang": "en"}
	}
	for _, task := range []string{"f1", "f2", "f3"} {
		runs = append(runs, pass("a", task, 1, nil), pass("b", task, 1, nil))
		meta[task] = map[string]string{"tier": "free", "lang": "en"}
	}
	runs = append(runs, fail("a", "p1", 1, nil), fail("b", "p1", 1, nil))
	meta["p1"] = map[string]string{"tier": "pro", "lang": "en"}
	src.add("exp_own", "support-faq", []string{"a", "b"}, runs)
	src.manifests["exp_own"].TaskMetadata = meta
	return src, abEval(schemas.EvaluationChild{Benchmark: "support-faq", ExperimentID: "exp_own", Status: "done"})
}

func TestReportSlices(t *testing.T) {
	src, ev := sliceFixture(t)
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Slices) != 1 || rep.Slices[0].Key != "tier" {
		t.Fatalf("slices = %+v (lang has one value and is not a slice)", rep.Slices)
	}
	s := rep.Slices[0]
	if len(s.Values) != 3 || s.Values[0].Value != "enterprise" || s.Values[0].Tasks != 4 || s.Values[2].Value != "pro" || !s.Values[2].Small {
		t.Fatalf("values = %+v", s.Values)
	}
	ent := s.Values[0]
	if ent.Arms["A"].Score != 0 || ent.Arms["B"].Score != 1 || ent.Delta == nil || *ent.Delta != 1 {
		t.Fatalf("enterprise = %+v", ent)
	}
	if ent.Arms["B"].CILow != 1 || ent.Arms["A"].CIHigh != 0 || s.Values[1].Arms["A"].CILow != 1 {
		t.Fatalf("intervals = %+v", ent.Arms)
	}
	// The small pro slice (0 for both) is never named worst.
	if s.Worst["A"] != "enterprise" || s.Worst["B"] == "pro" {
		t.Fatalf("worst = %v", s.Worst)
	}

	rep, err = Build(context.Background(), src, ev, Options{Where: map[string]string{"tier": "free"}})
	if err != nil {
		t.Fatal(err)
	}
	if m := rep.Benchmarks[0].Arms["A"]; m.Runs != 3 || m.Score != 1 || rep.Where["tier"] != "free" {
		t.Fatalf("filtered = %+v where %v", m, rep.Where)
	}
}

func TestGateSliceRules(t *testing.T) {
	src, ev := sliceFixture(t)
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	min := 0.5
	in := GateInput{Gate: schemas.EvaluationGate{Slices: []schemas.GateSliceRule{
		{Key: "tier", Value: "enterprise", MinScore: &min},
		{Key: "tier", Value: "nope", MinScore: &min},
	}}, Candidate: "A", Report: rep, Now: time.Now()}
	res := JudgeGate(in)
	if res.State != schemas.GateFail || len(res.Checks) != 2 || res.Checks[0].Capability != "tier=enterprise" || res.Checks[0].Pass || res.Checks[1].Pass {
		t.Fatalf("gate = %+v", res)
	}
	in.Candidate = "B"
	in.Gate.Slices = []schemas.GateSliceRule{{Key: "tier", Value: "enterprise", MinScore: &min, MaxRegression: 0.1}}
	if res := JudgeGate(in); res.State != schemas.GateNoBaseline {
		t.Fatalf("no baseline: %+v", res)
	}
	in.Baseline, in.BaselineReport, in.BaselineArm = &schemas.GateBaseline{}, rep, "A"
	in.Candidate = "A"
	in.Gate.Slices[0].MinScore = nil
	in.BaselineArm = "B"
	res = JudgeGate(in)
	if res.State != schemas.GateFail || !strings.Contains(res.Summary, "tier=enterprise fell from 100% to 0%") {
		t.Fatalf("regression: %+v", res)
	}
}

func TestParseWhere(t *testing.T) {
	w, err := ParseWhere([]string{"tier:enterprise", "lang: en "})
	if err != nil || w["tier"] != "enterprise" || w["lang"] != "en" {
		t.Fatalf("%v %v", w, err)
	}
	for _, bad := range []string{"tier", ":x", "ti er:x", "k:"} {
		if _, err := ParseWhere([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
