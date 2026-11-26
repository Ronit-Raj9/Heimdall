package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func runSkillDraft(t *testing.T, opts map[string]string) (*schemas.Experiment, []string, error) {
	t.Helper()
	f, ok := Lookup(schemas.GatewayFeatureSkillVersion)
	if !ok {
		t.Fatal("feature skill_version is not registered")
	}
	return f.Draft(context.Background(), DraftEnv{Root: t.TempDir()}, DraftRequest{Feature: f.ID, Suite: "s@1", Base: "agents/codex.yaml", Model: "m1", Trials: 2, BudgetUSD: 5, Options: opts})
}

func TestSkillDraftArms(t *testing.T) {
	exp, notes, err := runSkillDraft(t, map[string]string{"control_path": "agents/codex/skills/review-v3", "variant_path": "agents/codex/skills/review-v4"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := exp.Arms["A"], exp.Arms["B"]
	if a.Label != "skill review v3" || b.Label != "skill review v4" {
		t.Fatalf("labels %q %q", a.Label, b.Label)
	}
	if fmt.Sprint(a.Set["skills.extra"]) != "[agents/codex/skills/review-v3]" || fmt.Sprint(b.Set["skills.extra"]) != "[agents/codex/skills/review-v4]" || b.Set["model"] != "m1" {
		t.Fatalf("sets %v %v", a.Set, b.Set)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "replaces the agent's own extra skills list") {
		t.Fatalf("notes %v", notes)
	}
	none, _, err := runSkillDraft(t, map[string]string{"control_path": "none", "variant_path": "agents/codex/skills/review-v4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, set := none.Arms["A"].Set["skills.extra"]; set {
		t.Fatalf("control none sets skills.extra: %v", none.Arms["A"].Set)
	}
}

func TestSkillDraftRefusesBadPath(t *testing.T) {
	for _, opts := range []map[string]string{
		{"control_path": "none", "variant_path": "skills/review"},
		{"control_path": "none", "variant_path": "agents/codex/skills/../x"},
		{"control_path": "agents/codex/skills/r", "variant_path": "agents/codex/skills/r"},
		{"control_path": "none"},
	} {
		if _, _, err := runSkillDraft(t, opts); err == nil {
			t.Errorf("%v: want a refusal", opts)
		}
	}
}

func TestSkillNotAppliedWhenNeverRead(t *testing.T) {
	src := newFakeSource()
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Label: "skill review v3"}, "B": {Label: "skill review v4"}}}
	in := Input{ExpID: "e_s", Manifest: m, Src: src, Test: schemas.GatewayTest{Feature: schemas.GatewayFeatureSkillVersion, Control: "A", Variants: []string{"B"},
		Options: map[string]string{"control_path": "agents/codex/skills/review-v3", "variant_path": "agents/codex/skills/review-v4"}}}
	for i := 0; i < 6; i++ {
		for _, arm := range []string{"A", "B"} {
			ok := true
			id := fmt.Sprintf("r_%s%d", arm, i)
			in.Runs = append(in.Runs, Run{RunID: id, Task: fmt.Sprintf("t%d", i), Arm: arm, Success: &ok})
			read := 2.0
			if arm == "B" {
				read = 0
			}
			b, _ := json.Marshal(map[string]schemas.Value{"skill_files_read": {State: "observed", V: &read}})
			src.setFile(id, "metrics.json", b)
		}
	}
	f, _ := Lookup(schemas.GatewayFeatureSkillVersion)
	e := f.Compute(context.Background(), in)
	if e.State != StateNotApplied || e.Result != "failed" || !strings.Contains(e.Reason, "the agent did not open the skill in any run") {
		t.Fatalf("effect = %+v", e)
	}
	// One run without the metric: the check does not apply.
	src.files["r_B0"] = nil
	e = f.Compute(context.Background(), in)
	if e.State != StateMeasured || e.Setting == nil || e.Setting.Kind != "none" || e.Setting.Title != "Make version 4 the current version in Bifrost's Skills Repository" {
		t.Fatalf("effect = %+v setting %+v", e, e.Setting)
	}
}
