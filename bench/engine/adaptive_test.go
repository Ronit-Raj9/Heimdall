package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// armGrader passes a run when pass says so for its slot.
type armGrader struct {
	part
	pass func(sl *schemas.Slot) bool
}

func (g armGrader) Grade(_ context.Context, rc *schemas.RunContext, _ *schemas.Diff) (*schemas.Grade, error) {
	return &schemas.Grade{Passed: g.pass(rc.Slot), TestsRan: 3}, nil
}

func fourTasks(trials int) []schemas.ConditionSpec {
	return []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02", "t03", "t04"}}, Trials: trials}}
}

func TestPlanAdaptiveTaskRule(t *testing.T) {
	b := newBench(t)
	e := testExperiment(fourTasks(3)...)
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveTask, Min: 1, Max: 3}
	m := b.plan(t, e)
	if m.Adaptive == nil || m.Adaptive.Required != 8 || m.Adaptive.Optional != 16 || m.Adaptive.Min != 1 || m.Adaptive.Max != 3 {
		t.Fatalf("plan %+v", m.Adaptive)
	}
	for _, sl := range m.Slots {
		if sl.Optional != (sl.Trial > 1) {
			t.Fatalf("slot %+v", sl)
		}
	}
	if h, err := ManifestHash(m); err != nil || h != m.Hash {
		t.Fatalf("hash %s vs %s (%v)", h, m.Hash, err)
	}
}

func TestPlanFixedUnchanged(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, testExperiment(fourTasks(2)...))
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"adaptive"`, `"tier"`, `"optional"`, `"trials":{`, `"extends"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("a fixed manifest carries %s: old manifests would hash differently", key)
		}
	}
}

func TestPlanTrialsRefused(t *testing.T) {
	tests := []struct {
		name   string
		trials int
		policy schemas.TrialsPolicy
		want   string
	}{
		{"min not below max", 3, schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveTask, Min: 3}, "must be below max"},
		{"trials differ from max", 3, schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveTask, Min: 1, Max: 5}, "trials is the maximum"},
		{"unknown rule", 3, schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: "bandit"}, "want task or sequential"},
		{"one look", 2, schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 1}, "looks must be between"},
		{"alphas over alpha", 2, schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 2, Alphas: []float64{0.04, 0.04}}, "above verdict alpha"},
		{"sequential with extension", 2, schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 2,
			Extension: &schemas.ExtensionRule{Rule: schemas.ExtensionInverseNormal}}, "cannot also declare an extension"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			e := testExperiment(fourTasks(tt.trials)...)
			p := tt.policy
			e.Trials = &p
			_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPlanSequentialLooks(t *testing.T) {
	b := newBench(t)
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 2}
	m := b.plan(t, e)
	p := m.Adaptive
	if p == nil || len(p.Looks) != 2 || len(p.Looks[0]) != 4 || len(p.LookAlphas) != 2 || p.Futility != schemas.FutilityCurtail || p.Optional != 8 {
		t.Fatalf("plan %+v", p)
	}
	again := b.plan(t, e)
	if strings.Join(again.Adaptive.Looks[0], ",") != strings.Join(p.Looks[0], ",") {
		t.Fatalf("looks are not drawn from the seed: %v vs %v", again.Adaptive.Looks, p.Looks)
	}
}

func TestPlanTier(t *testing.T) {
	b := newBench(t)
	b.suite.Tiers = map[string]schemas.TaskSelector{"pr": {IDs: []string{"t03", "t01"}}}
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
	e.Tier = "pr"
	m := b.plan(t, e)
	if m.Tier == nil || strings.Join(m.Tier.Tasks, ",") != "t01,t03" || len(m.Slots) != 4 {
		t.Fatalf("tier %+v, %d slots", m.Tier, len(m.Slots))
	}
	e.Conditions = []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 1}}
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err == nil || !strings.Contains(err.Error(), "not in tier pr") {
		t.Fatalf("explicit task outside the tier: %v", err)
	}
	e.Tier = "headroom"
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err == nil || !strings.Contains(err.Error(), "tiers suggest") {
		t.Fatalf("undefined tier: %v", err)
	}
	e.Tier = "nightly"
	e.Conditions = []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}}
	if m := b.plan(t, e); len(m.Tier.Tasks) != 8 {
		t.Fatalf("nightly without a definition: %d tasks", len(m.Tier.Tasks))
	}
}

// TestSchedulerReleasesOptionalSlots runs a task-rule experiment through the
// scheduler: t01 and t02 are discordant (B passes, A fails) and run all 3
// trials; t03 and t04 pass in both arms and stop at trial 1.
func TestSchedulerReleasesOptionalSlots(t *testing.T) {
	b := newBench(t)
	b.reg.Graders = []schemas.Grader{armGrader{part{"hidden_tests"}, func(sl *schemas.Slot) bool {
		if sl.Task == "t03" || sl.Task == "t04" {
			return true
		}
		return sl.Arm == "B"
	}}}
	e := testExperiment(fourTasks(3)...)
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveTask, Min: 1, Max: 3}
	m := b.plan(t, e)
	events := make(chan Event, 512)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Events: events})
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, sl := range m.Slots {
		n := b.w.execs[sl.SlotID]
		ran += n
		want := 1
		if (sl.Task == "t03" || sl.Task == "t04") && sl.Trial > 1 {
			want = 0
		}
		if n != want {
			t.Fatalf("slot %s %s trial %d ran %d times, want %d", sl.Task, sl.Arm, sl.Trial, n, want)
		}
	}
	if ran != 16 || sum.NotRequired != 8 || sum.StopReason != verdict.StopMaxReached {
		t.Fatalf("ran %d, summary %+v", ran, sum)
	}
	if drain(events)[EventAdaptiveRelease] != 2 {
		t.Fatal("want two releases: trial 2, then trial 3")
	}
	// A resume releases nothing new and runs nothing again.
	again, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Sealed != 0 || again.Skipped != 16 || again.NotRequired != 8 {
		t.Fatalf("resume %+v", again)
	}
}

func TestExtendExperiment(t *testing.T) {
	b := newBench(t)
	e := testExperiment(fourTasks(2)...)
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsFixed, Extension: &schemas.ExtensionRule{Rule: schemas.ExtensionInverseNormal, AlphaFirst: 0.02}}
	m := b.plan(t, e)
	child, _, err := ExtendExperiment(m, b.suite, ExtendTasks, 3)
	if err != nil {
		t.Fatal(err)
	}
	if child.Extends == nil || child.Extends.ExpID != m.ExpID || child.Extends.ManifestHash != m.Hash || child.Extends.Rule != schemas.ExtensionInverseNormal {
		t.Fatalf("extends %+v", child.Extends)
	}
	for _, task := range child.Conditions[0].Tasks.IDs {
		if task <= "t04" {
			t.Fatalf("follow-up reuses parent task %s", task)
		}
	}
	if len(child.Conditions[0].Tasks.IDs) != 3 || child.Trials != nil {
		t.Fatalf("follow-up %+v trials %+v", child.Conditions, child.Trials)
	}
	cm := b.plan(t, child)
	if cm.Experiment.Extends == nil {
		t.Fatal("the follow-up manifest lost its link")
	}
	more, _, err := ExtendExperiment(m, b.suite, ExtendTrials, 2)
	if err != nil || len(more.Conditions[0].Tasks.IDs) != 4 || more.Conditions[0].Trials != 2 {
		t.Fatalf("trials follow-up %+v %v", more.Conditions, err)
	}
	if _, _, err := ExtendExperiment(cm, b.suite, ExtendTasks, 1); err == nil {
		t.Fatal("a follow-up of a follow-up was planned")
	}
}

// TestPresetsPlan plans every preset under experiments/presets against the
// test suite, with a pr and a headroom tier defined.
func TestPresetsPlan(t *testing.T) {
	for _, name := range []string{"pr-smoke", "nightly", "headroom"} {
		t.Run(name, func(t *testing.T) {
			b := newBench(t)
			b.suite.Tiers = map[string]schemas.TaskSelector{"pr": {IDs: []string{"t01", "t02", "t03"}}, "headroom": {IDs: []string{"t04", "t05"}}}
			e := loadExperiment(t, "../experiments/presets/"+name+".yaml")
			m := b.plan(t, e)
			if m.Tier == nil || m.Tier.Name != e.Tier {
				t.Fatalf("tier %+v", m.Tier)
			}
			if (e.Trials != nil && e.Trials.Mode == schemas.TrialsAdaptive) != (m.Adaptive != nil) {
				t.Fatalf("adaptive plan %+v for trials %+v", m.Adaptive, e.Trials)
			}
		})
	}
}

// TestSchedulerSequentialStopsForFutility: every run passes in both arms, so
// look 1 (4 tasks, d = 0) can never be followed by a directional look 2 (4
// more discordant tasks give p 0.125 at best): look 2 is never released.
func TestSchedulerSequentialStopsForFutility(t *testing.T) {
	b := newBench(t)
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 2}
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	look1 := map[string]bool{}
	for _, task := range m.Adaptive.Looks[0] {
		look1[task] = true
	}
	for _, sl := range m.Slots {
		want := 0
		if look1[sl.Task] {
			want = 1
		}
		if b.w.execs[sl.SlotID] != want {
			t.Fatalf("slot %s %s ran %d times, want %d", sl.Task, sl.Arm, b.w.execs[sl.SlotID], want)
		}
	}
	if sum.NotRequired != 8 || sum.StopReason != verdict.StopFutility {
		t.Fatalf("summary %+v", sum)
	}
}

// TestRecordedManifestsKeepTheirHash re-hashes manifests recorded before W7
// (testdata/recorded, docs/results) with the W7 fields in the schema: every
// one still verifies, so no old experiment changes.
func TestRecordedManifestsKeepTheirHash(t *testing.T) {
	paths, _ := filepath.Glob("../testdata/recorded/*/manifest.json")
	more, _ := filepath.Glob("../docs/results/*/manifest.json")
	paths = append(paths, more...)
	if len(paths) == 0 {
		t.Skip("no recorded manifest")
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var m schemas.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if h, err := ManifestHash(&m); err != nil || h != m.Hash {
			t.Errorf("%s: hash %s, recorded %s (%v)", p, h, m.Hash, err)
		}
	}
}

// TestSchedulerHeldMinimumNotSettled is 2-C-04: a minimum slot another live
// owner holds has no result yet, so the rule must not treat it as settled and
// release that task's optional trials ahead of it.
func TestSchedulerHeldMinimumNotSettled(t *testing.T) {
	b := newBench(t)
	b.reg.Graders = []schemas.Grader{armGrader{part{"hidden_tests"}, func(sl *schemas.Slot) bool { return sl.Arm == "B" }}}
	e := testExperiment(fourTasks(3)...)
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveTask, Min: 1, Max: 3}
	m := b.plan(t, e)
	var held *schemas.Slot
	for i := range m.Slots {
		if !m.Slots[i].Optional {
			held = &m.Slots[i]
			break
		}
	}
	b.store.leases[held.SlotID] = "another-live-owner"
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.LeaseHeld != 1 {
		t.Fatalf("summary %+v", sum)
	}
	for _, sl := range m.Slots {
		if sl.Optional && sl.Task == held.Task && b.w.execs[sl.SlotID] > 0 {
			t.Fatalf("optional trial %d of task %s ran while its minimum slot %s is held elsewhere", sl.Trial, sl.Task, held.SlotID)
		}
	}
}

// TestExtendTaskRuleKeepsRetainedConditions is the regression test for audit
// qc2-03-02: a task rule naming a descriptive condition that the follow-up
// does not repeat still plans, with the rule narrowed to what remains.
func TestExtendTaskRuleKeepsRetainedConditions(t *testing.T) {
	b := newBench(t)
	e := testExperiment(
		schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02", "t03", "t04"}}, Trials: 3},
		schemas.ConditionSpec{ID: "model-outage", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 3},
	)
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveTask, Min: 1, Max: 3, Conditions: []string{"clean", "model-outage"}}
	m := b.plan(t, e)
	child, _, err := ExtendExperiment(m, b.suite, ExtendTasks, 2)
	if err != nil {
		t.Fatal(err)
	}
	if child.Trials == nil || strings.Join(child.Trials.Conditions, ",") != "clean" {
		t.Fatalf("follow-up trials %+v, want the rule on clean only", child.Trials)
	}
	if e.Trials.Conditions[1] != "model-outage" {
		t.Fatal("the parent's rule was changed")
	}
	if cm := b.plan(t, child); cm.Adaptive == nil {
		t.Fatal("follow-up lost its adaptive plan")
	}
}
