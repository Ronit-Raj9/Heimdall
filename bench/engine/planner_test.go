package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
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
// in the queue, in the task's recorded arm order. An A/A slot is a pair of
// its own, on clean, right before or right after the A/B pair of its block.
func checkArmOrder(t *testing.T, m *schemas.Manifest) {
	t.Helper()
	pairs := map[string][]schemas.Slot{}
	for _, s := range m.Slots {
		if s.Counted {
			pairs[s.PairID] = append(pairs[s.PairID], s)
		}
	}
	for pid, ss := range pairs {
		if ss[0].Arm == AAArm {
			checkAASlot(t, m, pid, ss)
			continue
		}
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

// checkAASlot asserts that an A/A pair is one counted clean slot whose
// neighbours on one side are the A/B pair of the same block.
func checkAASlot(t *testing.T, m *schemas.Manifest, pid string, ss []schemas.Slot) {
	t.Helper()
	s := ss[0]
	if len(ss) != 1 || s.Condition != schemas.ConditionClean || !s.Counted {
		t.Fatalf("A/A pair %s: %+v", pid, ss)
	}
	n := len(m.ArmOrder[s.Task])
	sameBlock := func(from int) bool {
		if from < 0 || from+n > len(m.Slots) {
			return false
		}
		for i := from; i < from+n; i++ {
			o := m.Slots[i]
			if o.Arm == AAArm || o.Task != s.Task || o.Condition != s.Condition || o.Trial != s.Trial || o.PairID != m.Slots[from].PairID {
				return false
			}
		}
		return true
	}
	if !sameBlock(s.OrderIndex+1) && !sameBlock(s.OrderIndex-n) {
		t.Fatalf("A/A slot %d (task %s, trial %d) is not beside its block's A/B pair", s.OrderIndex, s.Task, s.Trial)
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

// multiOverlayBench adds a suite folder with suite-level fixtures and a
// fixture probe "multi" with the given overlays to a test bench.
func multiOverlayBench(t *testing.T, overlay *schemas.OverlaySpec, overlays ...schemas.OverlaySpec) *bench {
	t.Helper()
	b := newBench(t)
	b.suite.Root = filepath.Join(b.root, "benchmarks", "coding-core")
	for name, body := range map[string]string{"fixtures/plan.md": "release plan\n", "fixtures/memory.md": "from an earlier session\n"} {
		p := filepath.Join(b.suite.Root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.suite.Probes = append(b.suite.Probes, &schemas.ProbeSpec{ID: "multi", Version: "1.0.0", Layer: schemas.LayerFixture,
		Apply: schemas.ProbeApply{Overlay: overlay, Overlays: overlays}})
	b.suite.ProbeIDs = append(b.suite.ProbeIDs, "multi")
	return b
}

func multiExperiment() *schemas.Experiment {
	return testExperiment(
		schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
		schemas.ConditionSpec{ID: "multi", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
	)
}

// TestPlanHashesEveryOverlay checks that every overlay of a fixture probe,
// suite-level fixtures and user-level appends included, is hashed into the
// manifest, so editing any planted file changes the manifest.
func TestPlanHashesEveryOverlay(t *testing.T) {
	b := multiOverlayBench(t, nil,
		schemas.OverlaySpec{From: "{{suite.root}}/fixtures/plan.md", To: "notes/plan.md"},
		schemas.OverlaySpec{From: "fixtures/notes.stale.md", To: "docs/{{task.id}}.md"},
		schemas.OverlaySpec{From: "{{suite.root}}/fixtures/memory.md", To: "{{agent.notes.user_path}}"},
	)
	m := b.plan(t, multiExperiment())
	sum := func(rel string) string {
		s, err := utils.SHA256File(filepath.Join(b.root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	want := map[string]string{
		"t01:/workspace/CLAUDE.md":                   sum("benchmarks/coding-core/tasks/t01/fixtures/notes.clean.md"),
		"t01:/workspace/notes/plan.md":               sum("benchmarks/coding-core/fixtures/plan.md"),
		"t01:/workspace/docs/t01.md":                 sum("benchmarks/coding-core/tasks/t01/fixtures/notes.stale.md"),
		"t01:append:1:/home/agent/.claude/CLAUDE.md": sum("benchmarks/coding-core/fixtures/memory.md"),
		"/home/agent/.claude/CLAUDE.md":              sum("experiments/fixtures/codewins.md"),
	}
	got := m.Arms["B"].Files["multi"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arm B multi files\n got %v\nwant %v", got, want)
	}
	if err := os.WriteFile(filepath.Join(b.suite.Root, "fixtures/plan.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := b.plan(t, multiExperiment())
	if again.Arms["A"].Files["multi"]["t01:/workspace/notes/plan.md"] == m.Arms["A"].Files["multi"]["t01:/workspace/notes/plan.md"] {
		t.Fatal("editing a suite-level fixture did not change the manifest")
	}
}

func TestPlanRejectsOverlays(t *testing.T) {
	tests := []struct {
		name     string
		overlay  *schemas.OverlaySpec
		overlays []schemas.OverlaySpec
		want     string
	}{
		{"two overlays write one path", &schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "notes/plan.md"},
			[]schemas.OverlaySpec{{From: "{{suite.root}}/fixtures/plan.md", To: "./notes/plan.md"}},
			"probe multi: overlays 1 and 2 both write /workspace/notes/plan.md"},
		{"from outside the suite folder", nil, []schemas.OverlaySpec{{From: "{{suite.root}}/../escape.md", To: "x.md"}},
			"probe multi overlay 1: from"},
		{"single overlay into the user notes", &schemas.OverlaySpec{From: "{{suite.root}}/fixtures/memory.md", To: "{{agent.notes.user_path}}"}, nil,
			"the user-level notes target is allowed in overlays only"},
		{"missing fixture", nil, []schemas.OverlaySpec{{From: "{{suite.root}}/fixtures/none.md", To: "x.md"}}, "probe multi fixture"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := multiOverlayBench(t, tt.overlay, tt.overlays...)
			_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), multiExperiment())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
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
		{"unregistered verdict policy", func(b *bench, e *schemas.Experiment) {
			b.reg.Policies = []schemas.VerdictPolicy{testPolicy{part{"paired_signflip"}}}
			e.Verdict.Policy = "paired_typo"
		}, `verdict policy "paired_typo" is not registered`},
		{"verdict alpha above 1", func(_ *bench, e *schemas.Experiment) { e.Verdict.Alpha = 1.5 }, "verdict alpha 1.5 must be between 0 and 1"},
		{"negative assumed sd", func(_ *bench, e *schemas.Experiment) { e.Verdict.AssumedSD = -0.3 }, "assumed_sd -0.3 must be positive"},
		{"override value the agent schema refuses", func(_ *bench, e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"scripted.kind": "nooop"}}
		}, "scripted.kind"},
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

func TestPlanFreezesTaskAndRunInputHashes(t *testing.T) {
	b := newBench(t)
	e := testExperiment(
		schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 2},
		schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
	)
	e.Arms["B"] = schemas.ArmSpec{Set: map[string]any{"model": "claude-other"}}
	m := b.plan(t, e)
	if len(m.TaskIdentities) != 3 {
		t.Fatalf("task identities = %d, want 3", len(m.TaskIdentities))
	}
	for _, identity := range m.TaskIdentities {
		if identity.TaskHash == "" {
			t.Fatalf("empty task hash: %+v", identity)
		}
	}
	var cleanA, cleanB string
	for _, slot := range m.Slots {
		if !slot.Counted || slot.Condition != schemas.ConditionClean || slot.Trial != 1 {
			continue
		}
		if slot.TaskHash == "" || slot.RunInputHash == "" {
			t.Fatalf("slot hashes missing: %+v", slot)
		}
		if _, ok := m.RunInputs[slot.SlotID]; !ok {
			t.Fatalf("slot %s has no manifest run input", slot.SlotID)
		}
		if slot.Arm == "A" {
			cleanA = slot.RunInputHash
		} else if slot.Arm == "B" {
			cleanB = slot.RunInputHash
		}
	}
	if cleanA == "" || cleanB == "" || cleanA == cleanB {
		t.Fatalf("run input hashes do not distinguish arms: A=%q B=%q", cleanA, cleanB)
	}
	if m.Arms["A"].Identity == nil || m.Arms["B"].Identity == nil || m.Arms["A"].Identity.AgentConfigHash == "" || m.Arms["B"].Identity.AgentConfigHash == "" {
		t.Fatalf("resolved arm identities missing: %+v", m.Arms)
	}
}

func TestPlanCapabilityRequirements(t *testing.T) {
	tests := []struct {
		name     string
		required []string
		optional []string
		wantErr  string
		wantGap  string
	}{
		{name: "required capability rejects", required: []string{"structured_events"}, wantErr: "lacks required capability"},
		{name: "optional capability records gap", optional: []string{"structured_events"}, wantGap: "structured_events"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			e := testExperiment()
			e.Requirements = schemas.CapabilityRequirements{Required: tt.required, Optional: tt.optional}
			m, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(m.CapabilityGaps["A"], ","), tt.wantGap) {
				t.Fatalf("capability gaps = %+v", m.CapabilityGaps)
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

// TestPlanSmokeField checks that only smoke: true relaxes the suite lock, that
// the experiment name never does, and that the flag is frozen in the manifest
// (and absent from the manifest of every other experiment, so their hashes
// stay as they were).
func TestPlanSmokeField(t *testing.T) {
	tests := []struct {
		name    string
		smoke   bool
		lock    string // none | draft (t01 not admitted) | ok
		wantErr string
	}{
		{"smoke-local", false, "none", "suite lock: suite coding-core@1.0.0 has no lock; the experiment name does not relax the lock; a synthetic smoke experiment sets smoke: true"},
		{"smoke-acme", false, "draft", "task t01 is draft, not admitted"},
		{"Smoke Test", false, "draft", "sets smoke: true"},
		{"unit", false, "none", "has no lock"},
		{"unit", true, "none", ""},
		{"smoke", true, "draft", ""},
		{"unit", false, "ok", ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s smoke=%t lock=%s", tt.name, tt.smoke, tt.lock), func(t *testing.T) {
			b := newBench(t)
			switch tt.lock {
			case "none":
				b.suite.Lock = nil
			case "draft":
				b.suite.Lock.Tasks["t01"] = schemas.TaskLock{Version: "1.0.0", Status: schemas.TaskDraft}
			}
			e := testExperiment()
			e.Name, e.Smoke = tt.name, tt.smoke
			m, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(raw), `"smoke":true`); got != tt.smoke || m.Experiment.Smoke != tt.smoke {
				t.Fatalf("manifest smoke = %t (json %t), want %t", m.Experiment.Smoke, got, tt.smoke)
			}
			if !tt.smoke && strings.Contains(string(raw), `"smoke"`) {
				t.Fatal("a manifest without smoke must not carry the key, or old manifest hashes would change")
			}
			if h, _ := ManifestHash(m); h != m.Hash {
				t.Fatal("manifest hash does not cover the smoke flag")
			}
		})
	}
}

// TestShippedSmokeExperimentsDeclareSmoke checks every shipped smoke
// experiment sets the field, now that its name relaxes nothing.
func TestShippedSmokeExperimentsDeclareSmoke(t *testing.T) {
	for _, f := range []string{
		filepath.Join("..", "experiments", "smoke.yaml"),
		filepath.Join("..", "examples", "acme-node", "experiments", "acme-smoke.yaml"),
		filepath.Join("..", "scaffold", "templates", "project", "experiments", "smoke.yaml"),
	} {
		t.Run(f, func(t *testing.T) {
			if !loadExperiment(t, f).Smoke {
				t.Fatalf("%s does not set smoke: true", f)
			}
		})
	}
}

// TestA2AExampleExperimentsAreScripted is 7-C-04: the acme-a2a walkthrough
// promises every model call is scripted, so each of its experiments sets
// scripted_upstream and its runs bind the scripted plan.
func TestA2AExampleExperimentsAreScripted(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "examples", "acme-a2a", "experiments", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no acme-a2a experiments: %v", err)
	}
	p := NewPipeline(nil, "", nil)
	for _, f := range files {
		e := loadExperiment(t, f)
		if !e.ScriptedUpstream || !e.NoPaidTraffic() {
			t.Errorf("%s does not set scripted_upstream: true", f)
		}
		if got := p.runFaultMode(&schemas.RunContext{Experiment: e}); got != faultModeScripted {
			t.Errorf("%s: run fault mode %q, want scripted", f, got)
		}
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

// fakeChecker is an UpstreamChecker with a canned answer.
type fakeChecker struct {
	statuses []UpstreamStatus
	err      error
	calls    int
}

func (f *fakeChecker) CheckUpstreams(context.Context) ([]UpstreamStatus, error) {
	f.calls++
	return f.statuses, f.err
}

func TestPlanUpstreamCheck(t *testing.T) {
	const dnsErr = "dial tcp: lookup api.anthropic.com on 127.0.0.11:53: server misbehaving"
	anthDown := []UpstreamStatus{{Provider: "anthropic", Host: "api.anthropic.com:443", Error: dnsErr}}
	openaiDown := []UpstreamStatus{
		{Provider: "anthropic", Host: "api.anthropic.com:443", OK: true},
		{Provider: "openai", Host: "api.openai.com:443", Error: "dial tcp: lookup api.openai.com: no such host"},
	}
	toyYAML := fmt.Sprintf(testAgentYAML, "toy-model", "scripted", "CLAUDE.md", "/tmp/toy/CLAUDE.md") + "scripted: {kind: toy}\n"
	oracleYAML := fmt.Sprintf(testAgentYAML, "oracle", "scripted", "CLAUDE.md", "/tmp/toy/CLAUDE.md") + "scripted: {kind: oracle}\n"
	tests := []struct {
		name      string
		checker   *fakeChecker // nil: no checker wired
		mutate    func(e *schemas.Experiment)
		want      string // error substring; empty means the plan succeeds
		wantCalls int
	}{
		{name: "no checker wired", mutate: func(*schemas.Experiment) {}},
		{name: "reachable", checker: &fakeChecker{statuses: openaiDown[:1]}, wantCalls: 1},
		{name: "used provider unreachable refuses", checker: &fakeChecker{statuses: anthDown}, wantCalls: 1,
			want: "upstream check failed, every model call would fail; fix the network or the provider key before planning: " +
				"provider anthropic (api.anthropic.com:443, arms A, B): " + dnsErr},
		{name: "old proxy proceeds", checker: &fakeChecker{err: fmt.Errorf("fault proxy upstream check: %w", ErrUpstreamCheckUnsupported)},
			wantCalls: 1},
		{name: "checker error refuses", checker: &fakeChecker{err: errors.New("fault proxy upstream check: status 401: control token required")},
			wantCalls: 1, want: "upstream check: fault proxy upstream check: status 401"},
		{name: "provider not used by arms proceeds", checker: &fakeChecker{statuses: openaiDown}, wantCalls: 1},
		{name: "skipped provider proceeds", checker: &fakeChecker{statuses: []UpstreamStatus{
			{Provider: "anthropic", Host: "api.anthropic.com:443", Skipped: "through an HTTP proxy"}}}, wantCalls: 1},
		{name: "one arm on the unreachable provider refuses", checker: &fakeChecker{statuses: openaiDown}, wantCalls: 1,
			mutate: func(e *schemas.Experiment) {
				e.Arms["B"] = schemas.ArmSpec{Label: "gpt", Set: map[string]any{"gateway.provider": "openai"}}
			}, want: "provider openai (api.openai.com:443, arms B): dial tcp: lookup api.openai.com"},
		{name: "compatibility agent counts", checker: &fakeChecker{statuses: openaiDown}, wantCalls: 1,
			mutate: func(e *schemas.Experiment) {
				e.Arms["B"] = schemas.ArmSpec{Label: "other model", Set: map[string]any{"model": "claude-other"}}
				e.Compatibility = &schemas.CompatibilitySpec{Agent: "agents/compat-openai.yaml",
					Conditions: []string{"clean"}, Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1}
			}, want: "provider openai (api.openai.com:443, arms " + CompatArm + ")"},
		{name: "offline scripted controls skip the check", checker: &fakeChecker{statuses: anthDown},
			mutate: func(e *schemas.Experiment) {
				e.Base = "agents/oracle.yaml"
				e.Arms["B"] = schemas.ArmSpec{Label: "other model", Set: map[string]any{"model": "claude-other"}}
			}},
		{name: "toy control calls its provider", checker: &fakeChecker{statuses: anthDown}, wantCalls: 1,
			mutate: func(e *schemas.Experiment) {
				e.Arms["B"] = schemas.ArmSpec{Label: "toy", Agent: "agents/toy-model.yaml"}
			}, want: "provider anthropic (api.anthropic.com:443, arms A, B)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			for name, body := range map[string]string{
				"agents/toy-model.yaml":     toyYAML,
				"agents/oracle.yaml":        oracleYAML,
				"agents/compat-openai.yaml": strings.Replace(fmt.Sprintf(testAgentYAML, "compat", "command", "CLAUDE.md", "/tmp/c/CLAUDE.md"), "provider: anthropic", "provider: openai", 1),
			} {
				if err := os.WriteFile(filepath.Join(b.root, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			e := testExperiment()
			if tt.mutate != nil {
				tt.mutate(e)
			}
			var opts []PlannerOption
			if tt.checker != nil {
				opts = append(opts, WithUpstreamChecker(tt.checker))
			}
			m, err := NewPlanner(b.reg, b.root, nil, opts...).Plan(context.Background(), e)
			if tt.checker != nil && tt.checker.calls != tt.wantCalls {
				t.Fatalf("checker called %d times, want %d", tt.checker.calls, tt.wantCalls)
			}
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if m.Hash == "" {
				t.Fatal("plan returned an unhashed manifest")
			}
		})
	}
}

// aaExperiment is a two-arm experiment on three clean tasks with two trials
// and one stale-notes task, with or without the A/A arm.
func aaExperiment(aa bool) *schemas.Experiment {
	e := testExperiment(
		schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02", "t03"}}, Trials: 2},
		schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
	)
	e.AA = aa
	return e
}

// slotKeys lists the slots that keep(s) accepts, without their random ids,
// in queue order.
func slotKeys(m *schemas.Manifest, keep func(schemas.Slot) bool) []string {
	var out []string
	for _, s := range m.Slots {
		if keep(s) {
			out = append(out, fmt.Sprintf("%s/%s/%s/%d/%t", s.Condition, s.Task, s.Arm, s.Trial, s.Counted))
		}
	}
	return out
}

// TestPlanAAArm checks the A/A arm: a copy of arm A with its config hash, on
// clean only, one counted slot per clean task and trial in a pair of its own
// beside the A/B pair, with the A/B slots, arm order and budget parity of the
// same experiment without aa.
func TestPlanAAArm(t *testing.T) {
	b := newBench(t)
	m, plain := b.plan(t, aaExperiment(true)), b.plan(t, aaExperiment(false))
	again := b.plan(t, aaExperiment(true))
	a, a2 := m.Arms["A"], m.Arms[AAArm]
	aaSlots := slotsWhere(m, func(s schemas.Slot) bool { return s.Arm == AAArm })
	notAA := func(s schemas.Slot) bool { return s.Arm != AAArm }
	pairs := map[string]int{}
	for _, s := range m.Slots {
		pairs[s.PairID]++
	}
	aaOK, inputsOK := len(aaSlots) > 0, len(aaSlots) > 0
	for _, s := range aaSlots {
		aaOK = aaOK && s.Condition == schemas.ConditionClean && s.Counted && pairs[s.PairID] == 1
		peer := slotsWhere(m, func(o schemas.Slot) bool {
			return o.Arm == "A" && o.Task == s.Task && o.Condition == s.Condition && o.Trial == s.Trial
		})
		inputsOK = inputsOK && len(peer) == 1 && s.RunInputHash != "" && s.RunInputHash == peer[0].RunInputHash &&
			m.RunInputs[s.SlotID].ArmConfigHash == a.ConfigHash
	}
	twoArmOrders := true
	for _, o := range m.ArmOrder {
		twoArmOrders = twoArmOrders && len(o) == 2
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ManifestHash(m)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"A2 is the verdict's A/A arm", AAArm == verdict.ArmAA},
		{"A2 has arm A's config and config hash", a2.ConfigHash != "" && a2.ConfigHash == a.ConfigHash && reflect.DeepEqual(a2.Config, a.Config)},
		{"A2 is labelled as the copy", a2.Label == "A/A copy of arm A (baseline)"},
		{"A2 lays down arm A's clean files only", len(a2.Files) == 1 && reflect.DeepEqual(a2.Files["clean"], a.Files["clean"])},
		{"A2 has an identity of its own", a2.Identity != nil && a2.Identity != a.Identity && a2.Identity.AgentConfigHash == a.ConfigHash &&
			a2.BudgetSnapshot != nil && a2.BudgetSnapshot != a.BudgetSnapshot && reflect.DeepEqual(*a2.BudgetSnapshot, *a.BudgetSnapshot)},
		{"one A2 slot per clean task and trial", len(aaSlots) == 6},
		{"A2 slots are counted clean slots in pairs of their own", aaOK},
		{"A2 run inputs equal arm A's", inputsOK},
		{"A/B slots unchanged", reflect.DeepEqual(slotKeys(m, notAA), slotKeys(plain, notAA))},
		{"arm order unchanged and lists the pair only", equalOrders(m.ArmOrder, plain.ArmOrder) && twoArmOrders},
		{"budget parity unchanged", reflect.DeepEqual(m.BudgetParity, plain.BudgetParity) && reflect.DeepEqual(m.BudgetDiffs, plain.BudgetDiffs)},
		{"A2 placement repeats from the seed", reflect.DeepEqual(slotKeys(m, func(schemas.Slot) bool { return true }),
			slotKeys(again, func(schemas.Slot) bool { return true }))},
		{"aa frozen in the hashed manifest", m.Experiment.AA && strings.Contains(string(raw), `"aa":true`) && hash == m.Hash},
		{"no aa key without aa", !plain.Experiment.AA && !strings.Contains(mustJSON(t, plain), `"aa"`)},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Fatalf("failed; A2 %+v; A2 slots %+v", a2, aaSlots)
			}
		})
	}
	checkArmOrder(t, m)
}

// TestPlanAAPlacement checks that the A/A slot runs before the A/B pair on
// some tasks and after it on others, so A2 is not always later than A.
func TestPlanAAPlacement(t *testing.T) {
	b := newBench(t)
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
	e.AA = true
	m := b.plan(t, e)
	seen := map[bool]int{}
	for _, s := range slotsWhere(m, func(s schemas.Slot) bool { return s.Arm == AAArm }) {
		seen[s.OrderIndex%3 == 0]++
	}
	if seen[true] == 0 || seen[false] == 0 || seen[true]+seen[false] != 8 {
		t.Fatalf("A/A slot first on %d tasks and last on %d, want both on some of 8", seen[true], seen[false])
	}
	checkArmOrder(t, m)
}

func TestPlanAARejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *schemas.Experiment)
		want   string // error substring; empty means the plan succeeds
	}{
		{"no arm A to copy", func(e *schemas.Experiment) {
			e.Arms = map[string]schemas.ArmSpec{"C": e.Arms["A"], "B": e.Arms["B"]}
		}, "aa: true needs an arm A to copy"},
		{"declared arm A2", func(e *schemas.Experiment) {
			e.Arms[AAArm] = schemas.ArmSpec{Label: "other", Set: map[string]any{"model": "claude-other"}}
		}, "arm id A2 is reserved for the A/A copy of arm A when aa: true; rename the declared arm A2"},
		{"no clean condition", func(e *schemas.Experiment) {
			e.Conditions = []schemas.ConditionSpec{{ID: "stale-notes", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1}}
			e.Primary.Condition = "stale-notes"
		}, "aa: true needs the clean condition: the A/A arm runs only there"},
		{"matrix", func(e *schemas.Experiment) {
			e.Arms = nil
			e.Matrix = &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "m1"}, {Model: "m2"}}, AllowUnpriced: true}
		}, "aa: true is not supported with a matrix"},
		{"declared arms with one hash are still refused", func(e *schemas.Experiment) {
			e.Arms["B"] = schemas.ArmSpec{Label: "same", Set: map[string]any{"model": "claude-test"}}
		}, "same config hash"},
		{"declared arm A2 without aa plans as before", func(e *schemas.Experiment) {
			e.AA = false
			e.Arms[AAArm] = schemas.ArmSpec{Label: "other", Set: map[string]any{"model": "claude-other"}}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			e := testExperiment()
			e.AA = true
			tt.mutate(e)
			m, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("plan: %v", err)
				}
				if m.Arms[AAArm].Label != "other" {
					t.Fatalf("declared arm A2 replaced: %+v", m.Arms[AAArm])
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestPlanWithoutAAKeepsDraws pins the arm order and slot layout of a plan
// without aa to the algorithm that drew them before the A/A arm existed: one
// shuffle of the sorted arms per suite task on the "bifr" PCG stream, then
// trial, condition, task and arm order, then compatibility slots.
func TestPlanWithoutAAKeepsDraws(t *testing.T) {
	b := newBench(t)
	e := loadExperiment(t, filepath.Join("..", "experiments", "codewins.yaml"))
	m := b.plan(t, e)
	r := rand.New(rand.NewPCG(uint64(e.Seed), 0x62696672))
	order := map[string][]string{}
	for _, task := range b.suite.TaskIDs {
		o := []string{"A", "B"}
		r.Shuffle(len(o), func(i, j int) { o[i], o[j] = o[j], o[i] })
		order[task] = o
	}
	var want []string
	trials := 0
	for _, c := range e.Conditions {
		trials = max(trials, c.Trials)
	}
	for trial := 1; trial <= trials; trial++ {
		for _, c := range e.Conditions {
			if trial > c.Trials {
				continue
			}
			for _, task := range c.Tasks.Resolve(b.suite.TaskIDs) {
				for _, arm := range order[task] {
					want = append(want, fmt.Sprintf("%s/%s/%s/%d/true", c.ID, task, arm, trial))
				}
			}
		}
	}
	got := slotKeys(m, func(s schemas.Slot) bool { return s.Counted })
	if !equalOrders(m.ArmOrder, order) || !reflect.DeepEqual(got, want) {
		t.Fatalf("plan without aa moved:\narm order %v, want %v\nslots %v\nwant  %v", m.ArmOrder, order, got, want)
	}
	if _, ok := m.Arms[AAArm]; ok || strings.Contains(mustJSON(t, m), `"aa"`) {
		t.Fatal("a plan without aa carries the A/A arm or the aa key")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestPlanAARunsEndToEnd runs an aa experiment through the scheduler and the
// paired_signflip policy: every A/A slot runs and seals, and the verdict reads
// arm A2 as the A/A arm and keeps it out of the A-against-B effect.
func TestPlanAARunsEndToEnd(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, aaExperiment(true))
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(m.Slots) || len(m.Slots) != 20 {
		t.Fatalf("summary %+v for %d slots, want 20 sealed", sum, len(m.Slots))
	}
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
	var policy schemas.VerdictPolicy
	for _, p := range verdict.Builtin() {
		if p.GetName() == verdict.PolicyPairedSignFlip {
			policy = p
		}
	}
	an, err := policy.Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatal(err)
	}
	v := an.Verdict
	if v.Effect == nil || v.Effect.ArmA != "A" || v.Effect.ArmB != "B" || v.Variance == nil || v.Variance.AAArm != AAArm || v.Variance.AATasks != 3 {
		t.Fatalf("verdict effect %+v, variance %+v", v.Effect, v.Variance)
	}
	if r, ok := an.Naive[AAArm]; !ok || r.Den != 6 {
		t.Fatalf("naive scoreboard %+v, want 6 runs of arm A2", an.Naive)
	}
}

// testPolicy is a registered verdict policy for planner checks; planning never
// calls Decide.
type testPolicy struct{ part }

func (testPolicy) Decide(context.Context, *schemas.Manifest, []schemas.SealedRun) (*schemas.Analysis, error) {
	return nil, errors.New("not used")
}

func TestPlanKnownVerdictPolicy(t *testing.T) {
	b := newBench(t)
	b.reg.Policies = []schemas.VerdictPolicy{testPolicy{part{DefaultVerdictPolicy}}}
	e := testExperiment()
	e.Verdict.Policy = ""
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err != nil {
		t.Fatalf("empty policy falls back to %s: %v", DefaultVerdictPolicy, err)
	}
}
