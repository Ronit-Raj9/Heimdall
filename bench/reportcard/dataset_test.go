package reportcard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Y07: a baseline that ran fewer tasks of a grown benchmark is compared on
// the shared tasks only. Pooled over the different sets the candidate looks
// 33 points worse; on the four tasks both ran it did not drop at all.
func TestCompareDatasetsUsesSharedTasks(t *testing.T) {
	src := newSource()
	var base, cand []fixRun
	for _, task := range []string{"t1", "t2", "t3", "t4"} {
		base = append(base, pass("A", task, 1, nil))
		cand = append(cand, pass("A", task, 1, nil))
	}
	cand = append(cand, fail("A", "t5", 1, nil), fail("A", "t6", 1, nil)) // two hard new tasks
	src.add("exp_base", "support", []string{"A"}, base)
	src.add("exp_cand", "support", []string{"A"}, cand)
	for _, exp := range []string{"exp_base", "exp_cand"} {
		for _, r := range src.runs[exp] {
			src.manifests[exp].Slots = append(src.manifests[exp].Slots, r.Slot)
		}
	}
	src.manifests["exp_base"].SuiteLockHash = "sha256:aaaaaaaaaaaa"
	src.manifests["exp_cand"].SuiteLockHash = "sha256:bbbbbbbbbbbb"
	score := func(id, exp string) *schemas.Evaluation {
		return &schemas.Evaluation{ID: id, Status: StatusDone, Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "m"}}},
			Children: []schemas.EvaluationChild{{Benchmark: "support", ExperimentID: exp, Status: "done"}}}
	}
	evBase, evCand := score("ev_base", "exp_base"), score("ev_cand", "exp_cand")
	// The candidate's child recorded its dataset at launch; the baseline's
	// is read from its manifest.
	ChildDatasetFromManifest(&evCand.Children[0], src.manifests["exp_cand"])
	if c := evCand.Children[0]; c.LockHash != "sha256:bbbbbbbbbbbb" || len(c.Tasks) != 6 || c.SuiteVersion != "1" {
		t.Fatalf("child dataset %+v", c)
	}

	cmp, shared := CompareDatasets(src, evCand, evBase)
	if !cmp.Changed || len(cmp.Benchmarks) != 1 || cmp.Benchmarks[0].Compared != 4 || cmp.Benchmarks[0].NewTasks != 2 {
		t.Fatalf("comparison %+v", cmp)
	}
	if !strings.Contains(cmp.Summary, "Benchmark changed since baseline") || !strings.Contains(cmp.Summary, "2 new or changed tasks not compared") {
		t.Fatalf("summary %q", cmp.Summary)
	}
	if got := strings.Join(shared["support"], ","); got != "t1,t2,t3,t4" {
		t.Fatalf("shared %q", got)
	}

	full, _ := Build(context.Background(), src, evCand, Options{})
	pinned, _ := Build(context.Background(), src, evCand, Options{Tasks: shared})
	was, _ := Build(context.Background(), src, evBase, Options{Tasks: shared})
	if full.Totals["A"].Runs != 6 || pinned.Totals["A"].Runs != 4 || pinned.Totals["A"].Score != 1 {
		t.Fatalf("full %+v pinned %+v", full.Totals["A"], pinned.Totals["A"])
	}
	gate := schemas.EvaluationGate{MaxRegression: 0.05}
	in := GateInput{Gate: gate, Agent: "codex", Candidate: "A", Report: full, Baseline: &schemas.GateBaseline{Agent: "codex", Version: 1},
		BaselineArm: "A", BaselineReport: was, Now: time.Now()}
	// The regression check pairs tasks (Y06), so even the full report is
	// compared on the four shared tasks only: the two new hard tasks never
	// count as a drop.
	if res := JudgeGate(in); res.State == schemas.GateFail {
		t.Fatalf("paired regression check compares shared tasks only: %+v", res)
	}
	in.CompareReport, in.Dataset = pinned, cmp
	res := JudgeGate(in)
	if res.State != schemas.GatePass || res.Dataset == nil || !strings.Contains(res.Summary, "Benchmark changed since baseline") {
		t.Fatalf("on shared tasks: %+v", res)
	}

	// The same dataset on both sides compares as is.
	same, s := CompareDatasets(src, evBase, evBase)
	if same.Changed || s != nil {
		t.Fatalf("same dataset: %+v %v", same, s)
	}
	// A task whose content changed is not shared.
	evCand.Children[0].TaskHashes = map[string]string{"t1": "new"}
	evB := score("ev_b2", "exp_base")
	evB.Children[0].Tasks = []string{"t1", "t2", "t3", "t4"}
	evB.Children[0].TaskHashes = map[string]string{"t1": "old"}
	if _, sh := CompareDatasets(src, evCand, evB); strings.Contains(strings.Join(sh["support"], ","), "t1") {
		t.Fatalf("changed task t1 was compared: %v", sh)
	}
}

// A4.gate: a re-run of the same benchmark version that sampled 5 of the 10
// tasks the baseline ran has not changed the benchmark. The five tasks the
// baseline also ran were not picked this time; they were not "no longer run".
func TestCompareDatasetsSameLockSampleIsNotDropped(t *testing.T) {
	src := newSource()
	var base, cand []fixRun
	for i := 1; i <= 10; i++ {
		task := fmt.Sprintf("t%02d", i)
		base = append(base, pass("A", task, 1, nil))
		if i <= 5 {
			cand = append(cand, pass("A", task, 1, nil))
		}
	}
	src.add("exp_base", "hd-harbor-bfcl", []string{"A"}, base)
	src.add("exp_cand", "hd-harbor-bfcl", []string{"A"}, cand)
	for _, exp := range []string{"exp_base", "exp_cand"} {
		for _, r := range src.runs[exp] {
			src.manifests[exp].Slots = append(src.manifests[exp].Slots, r.Slot)
		}
		src.manifests[exp].SuiteLockHash = "sha256:samesamesame"
	}
	score := func(id, exp string) *schemas.Evaluation {
		return &schemas.Evaluation{ID: id, Status: StatusDone, Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "m"}}},
			Children: []schemas.EvaluationChild{{Benchmark: "hd-harbor-bfcl", ExperimentID: exp, Status: "done"}}}
	}
	cmp, shared := CompareDatasets(src, score("ev_cand", "exp_cand"), score("ev_base", "exp_base"))
	if cmp.Changed || len(cmp.Benchmarks) != 1 {
		t.Fatalf("comparison %+v", cmp)
	}
	row := cmp.Benchmarks[0]
	if row.Changed || row.DroppedTasks != 0 || row.NewTasks != 0 || row.Compared != 5 || row.NotCompared {
		t.Fatalf("row %+v", row)
	}
	want := "hd-harbor-bfcl: compared on the 5 tasks both ran; the baseline also ran 5 tasks this evaluation did not pick"
	if !strings.Contains(cmp.Summary, want) || strings.Contains(cmp.Summary, "no longer run") || strings.Contains(cmp.Summary, "changed since") {
		t.Fatalf("summary %q", cmp.Summary)
	}
	if got := strings.Join(shared["hd-harbor-bfcl"], ","); got != "t01,t02,t03,t04,t05" {
		t.Fatalf("shared %q", got)
	}

	// A different lock is a changed benchmark and keeps the old wording.
	src.manifests["exp_cand"].SuiteLockHash = "sha256:otherotherot"
	cmp, _ = CompareDatasets(src, score("ev_cand", "exp_cand"), score("ev_base", "exp_base"))
	if !cmp.Changed || cmp.Benchmarks[0].DroppedTasks != 5 || !strings.Contains(cmp.Summary, "5 baseline tasks no longer run") {
		t.Fatalf("lock changed: %+v", cmp)
	}
}
