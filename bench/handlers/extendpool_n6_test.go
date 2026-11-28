package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// poolAnalyzer gives each stage its own per-task means.
type poolAnalyzer struct{ per map[string][]schemas.TaskDiff }

func (p poolAnalyzer) Analyze(_ context.Context, expID, _ string) (*schemas.Analysis, error) {
	return &schemas.Analysis{ExpID: expID, Verdict: schemas.Verdict{Decision: schemas.DecisionInconclusive,
		Effect: &schemas.Effect{Condition: "clean", Metric: "verified_success", ArmA: "baseline", ArmB: "candidate", PerTask: p.per[expID]}}}, nil
}

func poolManifest(id, hash, armB string, tasks []string, ext *schemas.ExtendsRef) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: id, Hash: hash, SuiteID: "s", SuiteVersion: "1", SuiteLockHash: "L",
		Experiment: schemas.Experiment{Name: id, Extends: ext},
		Arms:       map[string]schemas.ArmManifest{"A": {ConfigHash: "A"}, "B": {ConfigHash: armB}}}
	for _, t := range tasks {
		m.Slots = append(m.Slots, schemas.Slot{SlotID: id + t, ExpID: id, Task: t, Condition: "clean", Counted: true})
	}
	return m
}

func sealedRuns(m *schemas.Manifest) []RunView {
	var out []RunView
	for _, sl := range m.Slots {
		out = append(out, RunView{Slot: sl, Sealed: true}, RunView{Slot: sl, Sealed: true})
	}
	return out
}

func getPool(t *testing.T, h *ExtendPoolHandler, id string) ExtendPoolView {
	t.Helper()
	var ctx fasthttp.RequestCtx
	ctx.SetUserValue("id", id)
	h.get(&ctx)
	var v ExtendPoolView
	if ctx.Response.StatusCode() != 200 || json.Unmarshal(ctx.Response.Body(), &v) != nil {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	return v
}

func TestExtendPoolPoolsFollowUpAndRefusesMismatch(t *testing.T) {
	parent := poolManifest("e_p", "hp", "B", []string{"t1", "t2"}, nil)
	// The link the web's Extend job writes (engine.ExtendExperiment): parent id, its manifest hash, the kind.
	good := poolManifest("e_x1", "h1", "B", []string{"t3", "t4"}, &schemas.ExtendsRef{ExpID: "e_p", ManifestHash: "hp", Kind: "tasks", Rule: schemas.ExtensionExploratory})
	bad := poolManifest("e_x2", "h2", "OTHER", []string{"t5", "t6"}, &schemas.ExtendsRef{ExpID: "e_p", ManifestHash: "hp", Kind: "tasks"})
	td := func(task string, a, b float64) schemas.TaskDiff {
		return schemas.TaskDiff{Task: task, MeanA: a, MeanB: b, D: b - a, NA: 1, NB: 1}
	}
	an := poolAnalyzer{per: map[string][]schemas.TaskDiff{
		"e_p":  {td("t1", 0, 1), td("t2", 1, 1)},
		"e_x1": {td("t3", 0, 1), td("t4", 0, 0)},
		"e_x2": {td("t5", 0, 1), td("t6", 0, 1)},
	}}
	var runs []RunView
	for _, m := range []*schemas.Manifest{parent, good, bad} {
		runs = append(runs, sealedRuns(m)...)
	}
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_p": parent, "e_x1": good, "e_x2": bad}, runs: runs}
	h := NewExtendPoolHandler(testMode, idx, an)

	v := getPool(t, h, "e_p")
	if v.Pooled == nil || v.Pooled.Tasks != 4 || v.Pooled.Runs != 8 || v.Pooled.AddedRuns != 4 {
		t.Fatalf("pooled %+v", v.Pooled)
	}
	if v.Pooled.DBar != 0.5 || v.Pooled.Interval == nil || v.Pooled.Interval[0] >= 0.5 || v.Pooled.Interval[1] <= 0.5 {
		t.Fatalf("pooled effect %+v interval %v", v.Pooled, v.Pooled.Interval)
	}
	if len(v.Extensions) != 2 || !v.Extensions[0].Pooled || v.Extensions[1].Pooled || !strings.Contains(v.Extensions[1].Reason, "arm B config hash differs") {
		t.Fatalf("extensions %+v", v.Extensions)
	}

	c := getPool(t, h, "e_x1")
	if c.Parent != "e_p" || !c.ParentPooled || c.Pooled != nil {
		t.Fatalf("child view %+v", c)
	}
	if c2 := getPool(t, h, "e_x2"); c2.ParentPooled || c2.ParentReason == "" {
		t.Fatalf("mismatched child view %+v", c2)
	}
}

func TestExtendPoolTrialsCountTaskOnce(t *testing.T) {
	parent := poolManifest("e_p", "hp", "B", []string{"t1", "t2"}, nil)
	more := poolManifest("e_x", "hx", "B", []string{"t1", "t2"}, &schemas.ExtendsRef{ExpID: "e_p", ManifestHash: "hp", Kind: "trials"})
	an := poolAnalyzer{per: map[string][]schemas.TaskDiff{
		"e_p": {{Task: "t1", MeanA: 0, MeanB: 1, NA: 1, NB: 1}, {Task: "t2", MeanA: 0, MeanB: 0, NA: 1, NB: 1}},
		"e_x": {{Task: "t1", MeanA: 0, MeanB: 0, NA: 3, NB: 3}, {Task: "t2", MeanA: 0, MeanB: 1, NA: 1, NB: 1}},
	}}
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_p": parent, "e_x": more}}
	v := getPool(t, NewExtendPoolHandler(testMode, idx, an), "e_p")
	if v.Pooled == nil || v.Pooled.Tasks != 2 {
		t.Fatalf("pooled %+v", v.Pooled)
	}
	// t1: candidate 1 of 4 tries = 0.25; t2: 1 of 2 = 0.5; baseline 0.
	if got := v.Pooled.DBar; got != 0.375 {
		t.Fatalf("d_bar %v", got)
	}
}
