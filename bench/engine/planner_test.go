package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestPlanExperimentFiles(t *testing.T) {
	tests := []struct {
		file             string
		counted, compat  int
		arms             int
		conditionSlots   map[string]int
		compatConditions int
	}{
		{"codewins.yaml", 120, 8, 2, map[string]int{"clean": 48, "stale-notes": 48, "model-outage": 12, "test-crash": 6, "empty-test-run": 6}, 4},
		{"codewins.reduced.yaml", 56, 4, 2, map[string]int{"clean": 24, "stale-notes": 24, "model-outage": 4, "empty-test-run": 4}, 4},
		{"codewins.minimum.yaml", 52, 1, 2, map[string]int{"clean": 24, "stale-notes": 24, "model-outage": 2, "empty-test-run": 2}, 1},
		{"smoke.yaml", 8, 0, 2, map[string]int{"clean": 4, "model-outage": 4}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			b := newBench(t)
			m := b.plan(t, loadExperiment(t, filepath.Join("..", "experiments", tt.file)))
			counted := slotsWhere(m, func(s schemas.Slot) bool { return s.Counted })
			compat := slotsWhere(m, func(s schemas.Slot) bool { return !s.Counted })
			if len(counted) != tt.counted || len(compat) != tt.compat {
				t.Fatalf("slots: counted %d compat %d, want %d and %d", len(counted), len(compat), tt.counted, tt.compat)
			}
			t.Logf("%s: %d counted + %d compat = %d slots", tt.file, len(counted), len(compat), len(m.Slots))
			perCond := map[string]int{}
			for _, s := range counted {
				perCond[s.Condition]++
			}
			for c, n := range tt.conditionSlots {
				if perCond[c] != n {
					t.Errorf("condition %s: %d slots, want %d", c, perCond[c], n)
				}
			}
			for i, s := range m.Slots {
				if s.OrderIndex != i || s.ExpID != m.ExpID || s.TaskVersion != "1.0.0" {
					t.Fatalf("slot %d: %+v", i, s)
				}
				if !storeID.MatchString(s.SlotID) || !storeID.MatchString(s.PairID) || !storeID.MatchString(s.ExpID) {
					t.Fatalf("slot %d ids do not match the store pattern: %+v", i, s)
				}
				if !s.Counted && s.Arm != CompatArm {
					t.Fatalf("uncounted slot on arm %s", s.Arm)
				}
			}
			if tt.compat > 0 && (m.Compatibility == nil || len(m.Compatibility.Files) != tt.compatConditions) {
				t.Fatalf("compatibility manifest: %+v", m.Compatibility)
			}
			checkArmOrder(t, m)
			if m.RandomizationU != "task" || m.BenchVersion != BenchVersion || m.SuiteLockHash != "lock-abc" {
				t.Fatalf("manifest header: %q %q %q", m.RandomizationU, m.BenchVersion, m.SuiteLockHash)
			}
			if m.Gateway == nil || m.Gateway.Name != "fake" || m.Parts["gate/g1"] == "" {
				t.Fatal("gateway manifest or part versions missing")
			}
		})
	}
}

// checkArmOrder asserts that both arms of every pair sit next to each other
// in the queue, in the task's recorded arm order.
func checkArmOrder(t *testing.T, m *schemas.Manifest) {
	t.Helper()
	pairs := map[string][]schemas.Slot{}
	for _, s := range m.Slots {
		if s.Counted {
			pairs[s.PairID] = append(pairs[s.PairID], s)
		}
	}
	for pid, ss := range pairs {
		order := m.ArmOrder[ss[0].Task]
		if len(ss) != len(order) {
			t.Fatalf("pair %s has %d slots, want %d", pid, len(ss), len(order))
		}
		for i, s := range ss {
			if s.Arm != order[i] || s.OrderIndex != ss[0].OrderIndex+i {
				t.Fatalf("pair %s: slot %d arm %s at %d, order %v", pid, i, s.Arm, s.OrderIndex, order)
			}
			if s.Task != ss[0].Task || s.Condition != ss[0].Condition || s.Trial != ss[0].Trial {
				t.Fatalf("pair %s mixes blocks", pid)
			}
		}
	}
}

func TestPlanResolvesArms(t *testing.T) {
	b := newBench(t)
	e := testExperiment(
		schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
		schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
	)
	e.Arms["B"] = schemas.ArmSpec{Label: "note", Set: map[string]any{
		"notes.user_file": "experiments/fixtures/codewins.md", "model": "claude-other", "budgets.wall_s": 600}}
	m := b.plan(t, e)
	a, bb := m.Arms["A"], m.Arms["B"]
	if bb.Config.Notes.UserFile != "experiments/fixtures/codewins.md" || bb.Config.Model != "claude-other" || bb.Config.Budgets.WallS != 600 {
		t.Fatalf("overrides not applied: %+v", bb.Config)
	}
	if a.Config.Model != "claude-test" || a.Config.Budgets.WallS != 900 || a.Config.Notes.UserFile != "" {
		t.Fatalf("arm A changed: %+v", a.Config)
	}
	if h, _ := utils.HashJSON(&bb.Config); h != bb.ConfigHash || a.ConfigHash == bb.ConfigHash {
		t.Fatal("config hashes wrong")
	}
	clean, stale := filepath.Join(b.root, "benchmarks/coding-core/tasks/t01/fixtures/notes.clean.md"),
		filepath.Join(b.root, "benchmarks/coding-core/tasks/t01/fixtures/notes.stale.md")
	cleanSum, _ := utils.SHA256File(clean)
	staleSum, _ := utils.SHA256File(stale)
	noteSum, _ := utils.SHA256File(filepath.Join(b.root, "experiments/fixtures/codewins.md"))
	if got := a.Files["clean"]["t01:/workspace/CLAUDE.md"]; got != cleanSum {
		t.Errorf("clean notes hash %q", got)
	}
	if got := a.Files["stale-notes"]["t01:/workspace/CLAUDE.md"]; got != staleSum {
		t.Errorf("stale notes hash %q", got)
	}
	if got := bb.Files["stale-notes"]["/home/agent/.claude/CLAUDE.md"]; got != noteSum {
		t.Errorf("arm file hash %q", got)
	}
	if _, ok := a.Files["clean"]["/home/agent/.claude/CLAUDE.md"]; ok {
		t.Error("arm A lays down a user file it does not have")
	}
}

func TestPlanRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(b *bench, e *schemas.Experiment)
		want   string
	}{
		{"unknown override path", func(_ *bench, e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"notes.userfile": "x.md"}}
		}, `no field "userfile"`},
		{"override below a scalar", func(_ *bench, e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"model.name": "x"}}
		}, "has no field"},
		{"identical arms", func(_ *bench, e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Label: "same", Set: map[string]any{"model": "claude-test"}}
		}, "same config hash"},
		{"one arm", func(_ *bench, e *schemas.Experiment) { delete(e.Arms, "B") }, "at least 2 arms"},
		{"reserved arm id", func(_ *bench, e *schemas.Experiment) { e.Arms[CompatArm] = schemas.ArmSpec{} }, "reserved"},
		{"experiment id with a hyphen", func(_ *bench, e *schemas.Experiment) { e.ID = "e_code-wins" }, "must match"},
		{"no lock", func(b *bench, _ *schemas.Experiment) { b.suite.Lock = nil }, "has no lock"},
		{"task not admitted", func(b *bench, _ *schemas.Experiment) {
			b.suite.Lock.Tasks["t02"] = schemas.TaskLock{Status: schemas.TaskQuarantined}
		}, "t02 is quarantined"},
		{"arm file collides with probe fixture", func(_ *bench, e *schemas.Experiment) {
			e.Conditions = append(e.Conditions, schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
			e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"notes.user_file": "experiments/fixtures/codewins.md",
				"notes.user_path": "/workspace/CLAUDE.md"}}
		}, "both write /workspace/CLAUDE.md"},
		{"unknown condition", func(_ *bench, e *schemas.Experiment) {
			e.Conditions = append(e.Conditions, schemas.ConditionSpec{ID: "nope", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
		}, "neither clean nor a probe"},
		{"unknown task", func(_ *bench, e *schemas.Experiment) {
			e.Conditions[0].Tasks = schemas.TaskSelector{IDs: []string{"t99"}}
		}, "t99 is not loaded"},
		{"unregistered extra gate", func(_ *bench, e *schemas.Experiment) { e.ExtraGates = []string{"g42"} }, `"g42" is not registered`},
		{"primary metric not measured", func(_ *bench, e *schemas.Experiment) { e.Primary.Metric = "nope" }, "not measured"},
		{"user file without user path", func(_ *bench, e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"notes.user_file": "experiments/fixtures/codewins.md", "notes.user_path": ""}}
		}, "not an absolute container path"},
		{"missing user file", func(_ *bench, e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"notes.user_file": "experiments/fixtures/missing.md"}}
		}, "notes.user_file"},
		{"timeout parity on a condition that does not run", func(_ *bench, e *schemas.Experiment) {
			e.Guards = []schemas.GuardSpec{{Rule: GuardTimeoutParity, Condition: "stale-notes"}}
		}, `guard timeout_parity: condition "stale-notes" is not one of the conditions`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			e := testExperiment()
			tt.mutate(b, e)
			_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestPlanGatewayHygiene(t *testing.T) {
	tests := []struct {
		name    string
		hygiene *schemas.GatewayHygiene
		want    string // substring of the error; empty means the plan succeeds
	}{
		{name: "clean gateway", hygiene: &schemas.GatewayHygiene{MCPAutoToolInjectOff: true}},
		{name: "no hygiene record from this gateway"},
		{name: "violations refuse the plan", hygiene: &schemas.GatewayHygiene{SemanticCache: true, Violations: []string{
			"semantic cache plugin semantic_cache is loaded", "routing rule r1 (id 1) is enabled: 2 targets"}},
			want: "gateway hygiene check failed, fix the gateway config before planning: semantic cache plugin semantic_cache is loaded; routing rule r1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			b.gw.hygiene = tt.hygiene
			m, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment())
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if m.Gateway.Hygiene != tt.hygiene {
				t.Fatalf("hygiene %+v not recorded in the manifest", m.Gateway.Hygiene)
			}
		})
	}
}

func TestPlanBudgetDiffs(t *testing.T) {
	small := strings.Replace(fmt.Sprintf(testAgentYAML, "small", "command", "CLAUDE.md", "/home/agent/.claude/CLAUDE.md"),
		"budgets: {wall_s: 900, cpus: 2, mem_gb: 4}", "budgets: {wall_s: 900, turns: 40, cpus: 1, mem_gb: 4}", 1)
	tests := []struct {
		name string
		arm  schemas.ArmSpec
		want []schemas.BudgetDiff
	}{
		{name: "same budgets", arm: schemas.ArmSpec{Set: map[string]any{"model": "claude-other"}}},
		{name: "another agent config with other budgets", arm: schemas.ArmSpec{Agent: "agents/small.yaml"}, want: []schemas.BudgetDiff{
			{Field: "budgets.turns", PerArm: map[string]string{"A": "unset", "B": "40"}},
			{Field: "budgets.cpus", PerArm: map[string]string{"A": "2", "B": "1"}},
		}},
		{name: "a set: path declares the budget", arm: schemas.ArmSpec{Agent: "agents/small.yaml", Set: map[string]any{"budgets.cpus": 1}},
			want: []schemas.BudgetDiff{{Field: "budgets.turns", PerArm: map[string]string{"A": "unset", "B": "40"}}}},
		{name: "setting budgets declares every budget", arm: schemas.ArmSpec{Set: map[string]any{"budgets": map[string]any{"wall_s": 600, "cpus": 2, "mem_gb": 4}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			if err := os.WriteFile(filepath.Join(b.root, "agents/small.yaml"), []byte(small), 0o644); err != nil {
				t.Fatal(err)
			}
			e := testExperiment()
			e.Arms["B"] = tt.arm
			m := b.plan(t, e)
			if !reflect.DeepEqual(m.BudgetDiffs, tt.want) {
				t.Fatalf("budget diffs = %+v, want %+v", m.BudgetDiffs, tt.want)
			}
		})
	}
}

func TestPlanPolicyHash(t *testing.T) {
	const policyYAML = "policy_version: 1\nname: p\nchecks:\n  - {id: a, metric: steps, max: 1}\n"
	tests := []struct {
		name, policy string
		write        bool
		want         string // error substring; empty means success
	}{
		{"no policy", "", false, ""},
		{"policy hashed", "policies/p.yaml", true, ""},
		{"missing policy fails", "policies/missing.yaml", false, "release policy policies/missing.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			if tt.write {
				p := filepath.Join(b.root, tt.policy)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(policyYAML), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			e := testExperiment()
			e.Policy = tt.policy
			m, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tt.write {
				want = utils.SHA256Bytes([]byte(policyYAML))
			}
			if m.PolicyHash != want {
				t.Fatalf("policy hash = %q, want %q", m.PolicyHash, want)
			}
			if h, _ := ManifestHash(m); h != m.Hash {
				t.Fatal("manifest hash does not cover the policy hash")
			}
		})
	}
}

func TestPlanSmokeIgnoresLock(t *testing.T) {
	b := newBench(t)
	b.suite.Lock = nil
	e := testExperiment()
	e.Name = "smoke-local"
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err != nil {
		t.Fatalf("smoke plan without a lock: %v", err)
	}
}

func TestPlanArmOrderFromSeed(t *testing.T) {
	b := newBench(t)
	e := loadExperiment(t, filepath.Join("..", "experiments", "codewins.yaml"))
	m1, m2 := b.plan(t, e), b.plan(t, e)
	if !equalOrders(m1.ArmOrder, m2.ArmOrder) {
		t.Fatal("same seed gave a different arm order")
	}
	if len(m1.ArmOrder) != 8 {
		t.Fatalf("arm order for %d tasks, want 8", len(m1.ArmOrder))
	}
	first := map[string]bool{}
	for _, o := range m1.ArmOrder {
		first[o[0]] = true
	}
	if len(first) != 2 {
		t.Errorf("seed %d puts the same arm first on every task: %v", e.Seed, m1.ArmOrder)
	}
	differs := false
	for seed := int64(1); seed <= 5 && !differs; seed++ {
		e2 := *e
		e2.Seed = seed
		differs = !equalOrders(m1.ArmOrder, b.plan(t, &e2).ArmOrder)
	}
	if !differs {
		t.Fatal("arm order ignores the seed")
	}
}

func equalOrders(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if strings.Join(v, ",") != strings.Join(b[k], ",") {
			return false
		}
	}
	return true
}

func TestManifestHashStable(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, loadExperiment(t, filepath.Join("..", "experiments", "codewins.yaml")))
	if !strings.HasPrefix(m.ExpID, "e_code_wins_sentence_") || len(m.ExpID) != len("e_code_wins_sentence_")+4 {
		t.Fatalf("exp id %q", m.ExpID)
	}
	h, err := ManifestHash(m)
	if err != nil || h != m.Hash || h == "" {
		t.Fatalf("hash %q, recorded %q, err %v", h, m.Hash, err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back schemas.Manifest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if h2, err := ManifestHash(&back); err != nil || h2 != m.Hash {
		t.Fatalf("hash after a JSON round trip %q, want %q (err %v)", h2, m.Hash, err)
	}
	back.Slots[0].Arm = "Z"
	if h3, _ := ManifestHash(&back); h3 == m.Hash {
		t.Fatal("an edited manifest kept its hash")
	}
}

// storeID is the id pattern the store enforces on every id it writes.
var storeID = regexp.MustCompile(`^[a-z0-9_]+$`)

func TestNewExpID(t *testing.T) {
	tests := []struct{ name, prefix string }{
		{"code-wins-sentence", "e_code_wins_sentence_"},
		{"Smoke Test!", "e_smoke_test_"},
		{"Code--Wins  v2", "e_code_wins_v2_"},
		{"???", "e_exp_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newExpID(tt.name)
			if !strings.HasPrefix(got, tt.prefix) || len(got) != len(tt.prefix)+4 || !storeID.MatchString(got) {
				t.Fatalf("newExpID(%q) = %q", tt.name, got)
			}
		})
	}
}
