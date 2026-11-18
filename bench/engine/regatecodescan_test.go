package engine

// X4: a regate checks the code scan gate on runs sealed before it existed,
// from their sealed diff alone (docs/code-scan.md).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const oldRunDiff = `diff --git a/app/run.py b/app/run.py
index 1111111..2222222 100644
--- a/app/run.py
+++ b/app/run.py
@@ -1,2 +1,4 @@
 import os
+def run(name):
+    os.system("ls " + name)
 x = 1
`

// oldSealedRun is a run folder sealed before the code scan: evidence.json
// has the diff record and no codescan, gates.json has one older gate.
func oldSealedRun(t *testing.T) *schemas.SealedRun {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "workspace.diff")
	if err := os.WriteFile(p, []byte(oldRunDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := utils.SHA256File(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(schemas.Evidence{Diff: &schemas.Diff{Path: "/old/host/workspace.diff", SHA256: sum}, Exec: &schemas.ExecResult{}})
	if err := os.WriteFile(filepath.Join(dir, "evidence.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return &schemas.SealedRun{
		Slot:    schemas.Slot{SlotID: "s_1", Arm: "A", Task: "t08", Condition: schemas.ConditionClean, Counted: true},
		Run:     schemas.Run{RunID: "r_old", Outcome: schemas.OutcomePass, Y: scoreOf(schemas.OutcomePass)},
		Dir:     dir,
		Grade:   &schemas.Grade{Passed: true},
		Gates:   []schemas.GateResult{{Gate: "g1_protected_paths", Version: "1.1.0", Effect: schemas.GateVeto}},
		Metrics: map[string]schemas.Value{"verified_success": schemas.Observed(1, schemas.EvidenceOutcome)},
	}
}

func TestRegateCodeScanOnOldRun(t *testing.T) {
	var scan schemas.Gate
	for _, g := range gates.Builtin() {
		if g.GetName() == gates.InsecureCodeAdded {
			scan = g
		}
	}
	reg := &schemas.Registry{Gates: []schemas.Gate{scan}, Metrics: []schemas.Metric{successMetric{}}}
	suite := &schemas.Suite{ID: "coding-core", Tasks: []*schemas.Task{{ID: "t08"}}}
	for _, tt := range []struct {
		name    string
		policy  *schemas.CodeScanPolicy
		outcome schemas.Outcome
		effect  schemas.GateEffect
	}{
		{name: "report by default", outcome: schemas.OutcomePass, effect: schemas.GateReport},
		{name: "veto at high", policy: &schemas.CodeScanPolicy{VetoAt: "high"}, outcome: schemas.OutcomeIntegrityViolation, effect: schemas.GateVeto},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &schemas.Manifest{ExpID: "e_old", Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "codex"}}},
				Experiment: schemas.Experiment{CodeScan: tt.policy}}
			got, err := Regate(context.Background(), reg, m, suite, oldSealedRun(t))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Recomputed, []string{gates.InsecureCodeAdded}) || got.Outcome != tt.outcome {
				t.Fatalf("recomputed %v, outcome %s", got.Recomputed, got.Outcome)
			}
			var res *schemas.GateResult
			for i := range got.Gates {
				if got.Gates[i].Gate == gates.InsecureCodeAdded {
					res = &got.Gates[i]
				}
			}
			if res == nil || !res.Fired || res.Effect != tt.effect || res.Version != codescan.RulesVersion ||
				!slices.Equal(res.Evidence, []string{"shell-injection-python@app/run.py:3"}) {
				t.Fatalf("gate %+v", res)
			}
		})
	}
}
