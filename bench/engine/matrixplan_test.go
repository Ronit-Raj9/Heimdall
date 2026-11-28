package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
	"gopkg.in/yaml.v3"
)

func TestExpandMatrix(t *testing.T) {
	base := func(mx *schemas.MatrixSpec) *schemas.Experiment {
		e := testExperiment()
		e.Arms = nil
		e.Matrix = mx
		return e
	}
	tests := []struct {
		name   string
		e      *schemas.Experiment
		labels map[string]string
		agents map[string]string
		sets   map[string]map[string]any
		err    string
	}{
		{
			name:   "models",
			e:      base(&schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2", Provider: "openai"}, {Model: "m3"}}}),
			labels: map[string]string{"A": "m1", "B": "m2 via openai", "C": "m3"},
			agents: map[string]string{"A": "", "B": "", "C": ""},
			sets: map[string]map[string]any{"A": {"model": "m1"}, "B": {"model": "m2", "gateway.provider": "openai"},
				"C": {"model": "m3"}},
		},
		{
			name:   "agents and models",
			e:      base(&schemas.MatrixSpec{Agents: []string{"agents/a.yaml", "agents/b.yaml"}, Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2"}}}),
			labels: map[string]string{"A": "a / m1", "B": "a / m2", "C": "b / m1", "D": "b / m2"},
			agents: map[string]string{"A": "agents/a.yaml", "B": "agents/a.yaml", "C": "agents/b.yaml", "D": "agents/b.yaml"},
		},
		{
			name:   "agents only",
			e:      base(&schemas.MatrixSpec{Agents: []string{"agents/a.yaml", "agents/b.yaml"}}),
			labels: map[string]string{"A": "a", "B": "b"},
			sets:   map[string]map[string]any{"A": nil, "B": nil},
		},
		{name: "arms and matrix", e: func() *schemas.Experiment {
			e := testExperiment()
			e.Matrix = &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m"}}}
			return e
		}(), err: "not both"},
		{name: "empty", e: base(&schemas.MatrixSpec{}), err: "list models, agents or both"},
		{name: "one combination", e: base(&schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}}}), err: "at least 2"},
		{name: "duplicate model", e: base(&schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m1"}}}), err: "listed twice"},
		{name: "empty model", e: base(&schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: " "}}}), err: "empty"},
		{name: "no base", e: func() *schemas.Experiment {
			e := base(&schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2"}}})
			e.Base = ""
			return e
		}(), err: "no base config"},
		{name: "too many", e: base(&schemas.MatrixSpec{Agents: []string{"a", "b", "c", "d", "e", "f"}, Models: []schemas.MatrixModel{
			{Model: "1"}, {Model: "2"}, {Model: "3"}, {Model: "4"}, {Model: "5"}}}), err: "at most 26"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandMatrix(tt.e)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Arms) != len(tt.labels) || got.Matrix != tt.e.Matrix || len(tt.e.Arms) != 0 {
				t.Fatalf("arms %+v (input changed: %v)", got.Arms, len(tt.e.Arms) != 0)
			}
			for id, label := range tt.labels {
				if got.Arms[id].Label != label {
					t.Errorf("arm %s label %q, want %q", id, got.Arms[id].Label, label)
				}
			}
			for id, agent := range tt.agents {
				if got.Arms[id].Agent != agent {
					t.Errorf("arm %s agent %q, want %q", id, got.Arms[id].Agent, agent)
				}
			}
			for id, set := range tt.sets {
				if len(got.Arms[id].Set) != len(set) {
					t.Errorf("arm %s set %v, want %v", id, got.Arms[id].Set, set)
				}
				for k, v := range set {
					if got.Arms[id].Set[k] != v {
						t.Errorf("arm %s set %s = %v, want %v", id, k, got.Arms[id].Set[k], v)
					}
				}
			}
		})
	}
	if e := testExperiment(); func() bool { got, _ := ExpandMatrix(e); return got != e }() {
		t.Fatal("an experiment without a matrix was copied")
	}
}

func TestPlanMatrix(t *testing.T) {
	matrix := func(allow bool) *schemas.Experiment {
		e := testExperiment()
		e.Arms = nil
		e.Matrix = &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2", Provider: "openai"}, {Model: "m3"}}, AllowUnpriced: allow}
		return e
	}
	priced := func(model string) bool { return model != "m3" }
	t.Run("expands into arms", func(t *testing.T) {
		b := newBench(t)
		m, err := NewPlanner(b.reg, b.root, nil, WithModelPrices(func(string) bool { return true })).Plan(context.Background(), matrix(false))
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Arms) != 3 || m.Arms["B"].Config.Model != "m2" || m.Arms["B"].Config.Gateway.Provider != "openai" || m.Arms["C"].Label != "m3" {
			t.Fatalf("arms %+v", m.Arms)
		}
		if m.Experiment.Matrix == nil || len(m.Experiment.Arms) != 3 {
			t.Fatalf("manifest experiment does not record the matrix and its arms: %+v", m.Experiment)
		}
		// 2 tasks x 1 trial x 3 arms, every pair holds all three arms.
		if len(m.Slots) != 6 {
			t.Fatalf("%d slots, want 6", len(m.Slots))
		}
		checkArmOrder(t, m)
		if h, err := ManifestHash(m); err != nil || h != m.Hash {
			t.Fatalf("hash %q, recorded %q, err %v", h, m.Hash, err)
		}
	})
	t.Run("refuses an unpriced model", func(t *testing.T) {
		b := newBench(t)
		_, err := NewPlanner(b.reg, b.root, nil, WithModelPrices(priced)).Plan(context.Background(), matrix(false))
		if err == nil || !strings.Contains(err.Error(), "no price in the price snapshot for m3") || strings.Contains(err.Error(), "m1") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("no snapshot wired", func(t *testing.T) {
		b := newBench(t)
		if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), matrix(false)); err == nil || !strings.Contains(err.Error(), "allow_unpriced") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("allow unpriced", func(t *testing.T) {
		b := newBench(t)
		if _, err := NewPlanner(b.reg, b.root, nil, WithModelPrices(priced)).Plan(context.Background(), matrix(true)); err != nil {
			t.Fatal(err)
		}
	})
}

// TestMatrixHashStability proves the matrix field leaves every experiment
// without one encoding exactly as before: the experiment's JSON has no
// "matrix" key, and every manifest recorded under docs/results re-hashes to
// the hash written in it.
func TestMatrixHashStability(t *testing.T) {
	raw, err := json.Marshal(testExperiment())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"matrix"`)) {
		t.Fatalf("an experiment without a matrix encodes one: %s", raw)
	}
	out, err := yaml.Marshal(testExperiment())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("matrix")) {
		t.Fatalf("an experiment without a matrix encodes one in YAML: %s", out)
	}
	paths, err := filepath.Glob(filepath.Join("..", "docs", "results", "*", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no recorded manifests under docs/results")
	}
	for _, p := range paths {
		t.Run(filepath.Base(filepath.Dir(p)), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var m schemas.Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if m.Experiment.Matrix != nil {
				t.Fatal("a recorded manifest decoded a matrix it does not have")
			}
			if h, err := ManifestHash(&m); err != nil || h != m.Hash {
				t.Fatalf("re-hash %q, recorded %q (err %v)", h, m.Hash, err)
			}
		})
	}
}

func TestMatrixModelDecode(t *testing.T) {
	var e schemas.Experiment
	doc := "name: x\nmatrix:\n  models: [gpt-5-mini, {model: claude-sonnet-5, provider: anthropic}]\n  agents: [agents/codex.yaml]\n"
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(&e); err != nil {
		t.Fatal(err)
	}
	want := []schemas.MatrixModel{{Model: "gpt-5-mini"}, {Model: "claude-sonnet-5", Provider: "anthropic"}}
	if e.Matrix == nil || len(e.Matrix.Models) != 2 || e.Matrix.Models[0] != want[0] || e.Matrix.Models[1] != want[1] || e.Matrix.Agents[0] != "agents/codex.yaml" {
		t.Fatalf("yaml matrix %+v", e.Matrix)
	}
	var j schemas.Experiment
	if err := json.Unmarshal([]byte(`{"matrix":{"models":["gpt-5-mini",{"model":"claude-sonnet-5","provider":"anthropic"}]}}`), &j); err != nil {
		t.Fatal(err)
	}
	if j.Matrix == nil || j.Matrix.Models[0] != want[0] || j.Matrix.Models[1] != want[1] {
		t.Fatalf("json matrix %+v", j.Matrix)
	}
}

func TestMatrixExampleExperiment(t *testing.T) {
	e := loadExperiment(t, filepath.Join("..", "experiments", "matrix-models.yaml"))
	got, err := ExpandMatrix(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Arms) != 3 || got.Arms["A"].Agent != "" || e.Base != "agents/codex.yaml" {
		t.Fatalf("arms %+v", got.Arms)
	}
	snap, err := os.ReadFile(filepath.Join("..", "deploy", "pricing-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prices struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(snap, &prices); err != nil {
		t.Fatal(err)
	}
	if err := checkMatrixPrices(e.Matrix, func(m string) bool { _, ok := prices.Models[m]; return ok }); err != nil {
		t.Fatalf("example names a model outside the pinned snapshot: %v", err)
	}
}

// TestMatrixRunsEndToEnd runs a three-model matrix through the scheduler on
// the fakes and reads it back: every slot seals, the unchanged paired verdict
// still decides B against A, and the matrix compares all three arms.
func TestMatrixRunsEndToEnd(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Arms = nil
	e.Matrix = &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2"}, {Model: "m3"}}, AllowUnpriced: true}
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(m.Slots) || len(m.Slots) != 6 {
		t.Fatalf("summary %+v for %d slots", sum, len(m.Slots))
	}
	// The fake store keeps runs but not the sealed-run index; read the
	// counted run of every slot from it as the store would.
	var runs []schemas.SealedRun
	for _, s := range m.Slots {
		for _, r := range b.store.slotRuns(s.SlotID) {
			if r.sealed {
				y, _ := r.run.Outcome.Score()
				runs = append(runs, schemas.SealedRun{Slot: s, Run: r.run, Dir: r.dir,
					Metrics: map[string]schemas.Value{"verified_success": schemas.Observed(y, schemas.EvidenceOutcome)}})
				break
			}
		}
	}
	if len(runs) != 6 {
		t.Fatalf("%d sealed runs, want 6", len(runs))
	}
	var policy schemas.VerdictPolicy
	for _, p := range verdict.Builtin() {
		if p.GetName() == "paired_signflip" {
			policy = p
		}
	}
	an, err := policy.Decide(context.Background(), m, runs)
	if err != nil || an.Verdict.Effect == nil || an.Verdict.Effect.ArmA != "A" || an.Verdict.Effect.ArmB != "B" {
		t.Fatalf("verdict %+v err %v", an, err)
	}
	mx, err := leaderboard.Matrix(m, runs, false, leaderboard.MatrixQuery{})
	if err != nil || len(mx.Rows) != 3 || len(mx.Pairs) != 3 || mx.Separable {
		t.Fatalf("matrix %+v err %v", mx, err)
	}
}

// TestReplanFrozenMatrix is QC2-11-01: a worker replans the manifest's
// frozen experiment, which holds the matrix and its expanded arms, with no
// price snapshot; the replan must succeed and hash like the coordinator's.
// Arms that are not the matrix's expansion are still refused.
func TestReplanFrozenMatrix(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Arms = nil
	e.Matrix = &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2", Provider: "openai"}}}
	m, err := NewPlanner(b.reg, b.root, nil, WithModelPrices(func(string) bool { return true })).Plan(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m.Experiment)
	if err != nil {
		t.Fatal(err)
	}
	var frozen schemas.Experiment
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	local, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), &frozen)
	if err != nil {
		t.Fatalf("replan of the frozen matrix experiment: %v", err)
	}
	adoptUnrepeatable(local, m)
	if h, err := ManifestHash(local); err != nil || h != m.Hash {
		t.Fatalf("replan hashes %s, want %s (%v): %s", h, m.Hash, err, manifestDiff(local, m))
	}
	frozen.Arms["B"] = schemas.ArmSpec{Label: "edited"}
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), &frozen); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("edited arms beside a matrix: %v", err)
	}
}

// TestPlanMatrixAcceptsTaskImageArms (round 6 N1): the comparison handler
// expands a two-agent matrix and adds the task-image build overrides to the
// arms; the planner takes those arms beside the matrix, still checks prices,
// and refuses any other edit.
func TestPlanMatrixAcceptsTaskImageArms(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Arms = nil
	e.Matrix = &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2", Provider: "openai"}}}
	expanded, err := ExpandMatrix(e)
	if err != nil {
		t.Fatal(err)
	}
	derived := *e
	derived.Arms = map[string]schemas.ArmSpec{}
	for id, arm := range expanded.Arms {
		set := map[string]any{"image": "bench/agent-x-task:1"} // the planner's own task-image checks are covered by its suites; here only the matrix rule
		for k, v := range arm.Set {
			set[k] = v
		}
		arm.Set = set
		derived.Arms[id] = arm
	}
	m, err := NewPlanner(b.reg, b.root, nil, WithModelPrices(func(string) bool { return true })).Plan(context.Background(), &derived)
	if err != nil {
		t.Fatalf("derived matrix arms: %v", err)
	}
	if m.Experiment.Arms["A"].Set["image"] != "bench/agent-x-task:1" {
		t.Fatalf("arm A lost the task-image build: %+v", m.Experiment.Arms["A"])
	}
	if _, err := NewPlanner(b.reg, b.root, nil, WithModelPrices(func(string) bool { return false })).Plan(context.Background(), &derived); err == nil {
		t.Fatal("unpriced matrix models with task-image arms were accepted")
	}
	derived.Arms["B"] = schemas.ArmSpec{Label: "edited", Set: map[string]any{"image": "x"}}
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), &derived); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("edited arms beside a matrix: %v", err)
	}
}
