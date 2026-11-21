package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// singleManifest is a one-arm experiment: arm A only, declared and resolved.
func singleManifest() *schemas.Manifest {
	m := testManifest(ArmBaseline)
	m.Experiment.Arms = map[string]schemas.ArmSpec{ArmBaseline: {Label: "codex on gpt-x"}}
	return m
}

func TestSingleArm(t *testing.T) {
	if got := SingleArm(singleManifest()); got != ArmBaseline {
		t.Fatalf("SingleArm = %q", got)
	}
	two := testManifest(ArmBaseline, ArmCandidate)
	two.Experiment.Arms = map[string]schemas.ArmSpec{ArmBaseline: {}, ArmCandidate: {}}
	if SingleArm(two) != "" || SingleArm(nil) != "" {
		t.Fatal("two arms or no manifest is not a one-arm experiment")
	}
}

// A one-arm experiment gets a score with an interval, never a verdict.
func TestDecideSingleArmScore(t *testing.T) {
	m := singleManifest()
	runs := passes(schemas.ConditionClean, ArmBaseline, 2, 2, 1, 0, 2, 1, 2, 2, 0)
	a := decideOrFail(t, m, runs)
	v := a.Verdict
	if v.Decision != schemas.DecisionScore || v.Action != schemas.ActionNoDecision || v.Effect != nil || len(v.Guards) != 0 {
		t.Fatalf("verdict %+v", v)
	}
	s := v.Score
	if s == nil || s.Arm != ArmBaseline || s.Label != "codex on gpt-x" || s.Tasks != 8 || s.Runs != 16 || s.Observed != 16 {
		t.Fatalf("score %+v", s)
	}
	if s.Mean == nil || !near(*s.Mean, 10.0/16, 1e-12) {
		t.Fatalf("mean %v", s.Mean)
	}
	if s.Interval == nil || s.Interval[0] > *s.Mean || s.Interval[1] < *s.Mean || s.Interval[0] < 0 || s.Interval[1] > 1 || !strings.Contains(s.Method, "bootstrap") {
		t.Fatalf("interval %v method %q", s.Interval, s.Method)
	}
	if a.Naive[ArmBaseline].Num != 10 || a.Naive[ArmBaseline].Den != 16 {
		t.Fatalf("naive %+v", a.Naive)
	}
	// The same runs give the same interval.
	b := decideOrFail(t, m, runs)
	if *b.Verdict.Score.Interval != *s.Interval {
		t.Fatal("interval is not reproducible")
	}
}

// Unknown runs widen the score's bounds; too many of them make it INVALID.
func TestDecideSingleArmUnknowns(t *testing.T) {
	m := singleManifest()
	runs := pattern(schemas.ConditionClean, ArmBaseline, repeat("11", 9)...)
	runs = append(runs, pattern(schemas.ConditionClean, ArmBaseline, "1u")[1:]...)
	runs[len(runs)-1].Slot.Task, runs[len(runs)-1].Slot.SlotID = taskID(9), "s_unknown"
	a := decideOrFail(t, m, runs)
	s := a.Verdict.Score
	if a.Verdict.Decision != schemas.DecisionScore || s.Runs != 19 || s.Observed != 18 || s.Low == nil || s.High == nil || !near(*s.Low, 18.0/19, 1e-12) || !near(*s.High, 1, 1e-12) {
		t.Fatalf("verdict %+v score %+v", a.Verdict, s)
	}
	half := pattern(schemas.ConditionClean, ArmBaseline, "1u", "0u", "1u")
	a = decideOrFail(t, m, half)
	if a.Verdict.Decision != schemas.DecisionInvalid || a.Verdict.Score == nil || len(a.Verdict.InvalidReasons) == 0 {
		t.Fatalf("low coverage %+v", a.Verdict)
	}
}

// One task: a rate gets the exact interval over its runs.
func TestDecideSingleArmOneTask(t *testing.T) {
	a := decideOrFail(t, singleManifest(), passes(schemas.ConditionClean, ArmBaseline, 4, 3))
	s := a.Verdict.Score
	if s.Interval == nil || !strings.Contains(s.Method, "Clopper-Pearson") || s.Tasks != 1 {
		t.Fatalf("score %+v", s)
	}
}
