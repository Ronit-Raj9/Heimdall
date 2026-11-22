package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// gw06Policy copies the repository's default release rule set to
// policies/default.yaml under the app's root and returns its hash.
func gw06Policy(t *testing.T, a *app) string {
	t.Helper()
	raw, err := os.ReadFile("../../policies/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.cfg.Root, "policies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return policy.Hash(raw)
}

// gw06Experiment freezes and seals an experiment with the given arms, one
// clean slot per task and arm, each run ending with the task's outcome.
func gw06Experiment(t *testing.T, a *app, base *schemas.Manifest, expID string, arms []string, outcomes map[string]schemas.Outcome, policyFile, policyHash string) {
	t.Helper()
	ctx := context.Background()
	m := &schemas.Manifest{ExpID: expID, Arms: map[string]schemas.ArmManifest{},
		Experiment: schemas.Experiment{Primary: base.Experiment.Primary, Arms: map[string]schemas.ArmSpec{}, Policy: policyFile}, PolicyHash: policyHash}
	for _, arm := range arms {
		m.Arms[arm] = schemas.ArmManifest{Label: "setup " + arm}
		m.Experiment.Arms[arm] = schemas.ArmSpec{Label: "setup " + arm}
	}
	tasks := make([]string, 0, len(outcomes))
	for task := range outcomes {
		tasks = append(tasks, task)
	}
	for _, task := range tasks {
		for _, arm := range arms {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + strings.ToLower(arm), PairID: "p_" + task, Task: task,
				TaskVersion: "1.0.0", Arm: arm, Condition: schemas.ConditionClean, Trial: 1, Counted: true})
		}
	}
	var err error
	if m.Hash, err = utils.ManifestHash(m); err != nil {
		t.Fatal(err)
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	for i := range m.Slots {
		sl := m.Slots[i]
		sl.ExpID = m.ExpID
		out := outcomes[sl.Task]
		v := "0"
		if out == schemas.OutcomePass {
			v = "1"
		}
		r := &schemas.Run{RunID: "r_" + expID + "_" + strings.TrimPrefix(sl.SlotID, "s_"), ExpID: m.ExpID, SlotID: sl.SlotID, PairID: sl.PairID,
			Attempt: 1, Stage: schemas.StageSeal, ManifestHash: m.Hash, Outcome: out}
		if _, err := a.store.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		metrics := `{"verified_success":{"state":"observed","v":` + v + `,"evidence":"outcome"}}`
		if _, ok := out.Score(); !ok {
			metrics = `{"verified_success":{"state":"unknown","evidence":"outcome"}}`
		}
		if err := a.store.WriteRunFile(ctx, r, "metrics.json", []byte(metrics)); err != nil {
			t.Fatal(err)
		}
		if err := a.store.WriteRunFile(ctx, r, "evidence.json", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}

// A one-setup score run has nothing to release, so its analysis carries no
// release policy result even when the experiment froze a rule set; a
// two-setup comparison with the same rule set still gets one (N61, D18).
func TestAnalyzeScoreExperimentHasNoPolicy(t *testing.T) {
	a, base := analysisFixture(t)
	hash := gw06Policy(t, a)
	pass := map[string]schemas.Outcome{"t01": schemas.OutcomePass, "t02": schemas.OutcomePass, "t03": schemas.OutcomeFail}
	gw06Experiment(t, a, base, "e_score", []string{"A"}, pass, "policies/default.yaml", hash)
	gw06Experiment(t, a, base, "e_pair", []string{"A", "B"}, pass, "policies/default.yaml", hash)
	ctx := context.Background()

	an, _, err := a.analyze(ctx, "e_score", "")
	if err != nil {
		t.Fatal(err)
	}
	if an.Verdict.Decision != schemas.DecisionScore {
		t.Fatalf("decision %s, want score", an.Verdict.Decision)
	}
	if an.Policy != nil {
		t.Fatalf("a one-setup score run got a release policy result: %s %+v", an.Policy.Decision, an.Policy)
	}
	pair, _, err := a.analyze(ctx, "e_pair", "")
	if err != nil {
		t.Fatal(err)
	}
	if pair.Policy == nil {
		t.Fatal("a two-setup comparison lost its release policy result")
	}
}
