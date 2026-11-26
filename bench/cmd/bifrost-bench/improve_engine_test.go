//go:build improveengine && e2eagentfirst

// Engine test of improvement runs (design 5, A1.11): the improver and a
// scoring experiment go through the real planner (PlanCanary), the run
// store (CreateExperiment) and the scheduler (scheduleRunner.execute).
//
// It needs Docker, the image bench/coding-core-base:1, a running gateway
// and the shipped improver suite, and it starts containers, so it is behind
// the build tags improveengine and e2eagentfirst (it reuses that test's
// project) and skips unless BENCH_IMPROVE_ENGINE=1. The
// agents are scripted shell commands; no model is called.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// engineEdits is the fixed edits.json the scripted reflector writes: one
// skill whose text holds a placeholder, and one playbook item.
const engineEdits = `{"rationale":"fixed","edits":[` +
	`{"op":"add_skill","path":"/home/agent/skills/run-tests/SKILL.md","text":"Run the tests with {{model}} in mind.","reason":"tests were skipped"},` +
	`{"op":"add_item","file":"@instructions","section":"Checks","text":"Run the tests before you finish.","reason":"tests were skipped"}]}`

func engineProject(t *testing.T) (string, *app) {
	t.Helper()
	if os.Getenv("BENCH_IMPROVE_ENGINE") != "1" {
		t.Skip("set BENCH_IMPROVE_ENGINE=1 to run the improvement engine test (it starts containers)")
	}
	if err := exec.Command("docker", "image", "inspect", "bench/coding-core-base:1").Run(); err != nil {
		t.Skip("docker or image bench/coding-core-base:1 not available")
	}
	if improverSuiteFS == nil {
		t.Skip("this build ships no improver suite")
	}
	src, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := e2eProject(t, src)
	script := "mkdir -p /workspace && cat > /workspace/edits.json <<'EOF'\n" + engineEdits + "\nEOF\n"
	reflector := "id: heimdall-reflector\nadapter: command\nimage: bench/coding-core-base:1\nargv: [sh, -c, " + strconvQuote(script) + "]\n" +
		"model: claude-sonnet-4-5\ncapabilities: [shell]\nbudgets:\n  wall_s: 300\ngateway:\n  provider: anthropic\n"
	writeE2E(t, filepath.Join(root, "agents", reflectorAgentID+".yaml"), []byte(reflector))
	// A library-style agent with no files map: only its standing
	// instructions can change.
	writeE2E(t, filepath.Join(root, "agents", "plain.yaml"), []byte("id: plain\nadapter: command\nimage: bench/coding-core-base:1\nargv: [sh, -c, 'cat /tmp/bench/instruction.md > /workspace/seen.md']\nmodel: claude-sonnet-4-5\ncapabilities: [shell]\nbudgets:\n  wall_s: 300\ngateway:\n  provider: anthropic\n"))
	a, err := newApp(context.Background(), filepath.Join(root, "deploy", "bench.yaml"), appOptions{gateway: true})
	if err != nil {
		t.Fatal(err)
	}
	return root, a
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestImproveEngineRound(t *testing.T) {
	_, a := engineProject(t)
	ctx := context.Background()
	x := improveExecutor{a: a}
	ed := improve.Editable{Dirs: []string{"/home/agent/skills/"}, Instructions: true}
	spec := &improve.Spec{Name: "engine", Agent: "agents/plain.yaml", Suite: e2eCoding + "@1.0.0", Editable: ed, Seed: 1}
	spec.Defaults()

	// (5) a missing improver task is refused before any experiment exists.
	bad := *spec
	bad.Improver.Task = "nope@1.0.0:x"
	if err := x.CheckImprover(ctx, &bad); err == nil || err.Error() != "The improver task nope@1.0.0:x does not exist." {
		t.Fatalf("missing task: %v", err)
	}
	if err := x.CheckImprover(ctx, spec); err != nil {
		t.Fatal(err)
	}

	// (4) the agent with no files map gets an instructions bundle.
	v0, err := x.Load(ctx, spec.Agent, ed)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v0.Bundle[improve.InstructionsKey]; !ok {
		t.Fatalf("version 0 bundle %v", v0.Bundle)
	}

	// (1) the improver experiment is equal_budget, its arm capped at the spec.
	res, err := x.Improve(ctx, improve.ImproveRequest{ImprovementID: "imp_engine", Spec: spec, Round: 1, Incumbent: v0,
		BudgetUSD: spec.Improver.BudgetUSD, WallS: spec.Improver.WallS})
	if err != nil {
		t.Fatal(err)
	}
	m, err := a.store.LoadManifest(ctx, res.ExpID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Experiment.Fairness != schemas.FairnessEqualBudget || m.Arms["A"].Config.Budgets.USD != spec.Improver.BudgetUSD {
		t.Errorf("improver manifest fairness %q usd %v", m.Experiment.Fairness, m.Arms["A"].Config.Budgets.USD)
	}
	if res.Edits == nil || len(res.Edits.Edits) != 2 {
		t.Fatalf("edits %+v", res.Edits)
	}

	// The candidate: the skill and the standing instructions.
	b := v0.Bundle.Clone()
	b["/home/agent/skills/run-tests/SKILL.md"] = "Run the tests with {{model}} in mind."
	b[improve.InstructionsKey] = "Run the tests before you finish."
	doc, err := improve.ApplyBundle(v0.Doc, ed, b)
	if err != nil {
		t.Fatal(err)
	}
	cand, err := x.Freeze(ctx, v0, doc, improve.Lineage{ImprovementID: "imp_engine", Round: 1, BundleHash: b.Hash(), Editable: ed})
	if err != nil {
		t.Fatal(err)
	}

	// (2) round 2's improver input bundle holds the skill added in round 1.
	doc2, err := improverDoc(improve.ImproveRequest{Spec: spec, Round: 2, Incumbent: cand}, cand.Doc, improve.PlaybookObject{})
	if err != nil || !strings.Contains(string(doc2), "run-tests/SKILL.md") {
		t.Errorf("round 2 improver input: %v", err)
	}

	// (3) and (4): the skill reaches the container verbatim, and the
	// instruction file starts with the standing instructions.
	sc, err := x.Score(ctx, improve.ScoreRequest{ImprovementID: "imp_engine", Spec: spec, Round: 1, Phase: improve.PhaseScreen, Arms: []improve.Version{v0, cand}, Tasks: improveEngineTask(t, x, spec), Trials: 1})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := a.store.SealedRuns(ctx, sc.ExpID, store.GradeSourceOriginal)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sr := range runs {
		if sr.Slot.Arm != "B" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sr.Dir, "plan.json"))
		if err != nil {
			t.Fatal(err)
		}
		var plan schemas.RunPlan
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if plan.Files["/home/agent/skills/run-tests/SKILL.md"] != "Run the tests with {{model}} in mind." {
			t.Errorf("skill in container %q", plan.Files["/home/agent/skills/run-tests/SKILL.md"])
		}
		if body := plan.Files[adapters.InstructionFile]; !strings.HasPrefix(body, "Standing instructions from the agent's owner:") {
			t.Errorf("instruction file %q", body)
		}
		found = true
	}
	if !found {
		t.Error("no candidate run")
	}
}

func improveEngineTask(t *testing.T, x improveExecutor, spec *improve.Spec) []string {
	t.Helper()
	tasks, err := x.Tasks(context.Background(), spec)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("tasks %v %v", tasks, err)
	}
	return []string{tasks[0].ID}
}
