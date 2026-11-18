package quickstart

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func TestParseArm(t *testing.T) {
	tests := []struct {
		in    string
		agent string
		set   map[string]any
		bad   bool
	}{
		{in: "agents/claude-code.yaml", agent: "agents/claude-code.yaml"},
		{in: "model=claude-haiku-4-5", set: map[string]any{"model": "claude-haiku-4-5"}},
		{in: "model=gpt-5.4-mini,budgets.wall_s=600,skills.enabled=false",
			set: map[string]any{"model": "gpt-5.4-mini", "budgets.wall_s": 600, "skills.enabled": false}},
		{in: "notes.user_file=a,b.md", set: map[string]any{"notes.user_file": "a,b.md"}},
		{in: "label=x: y", set: map[string]any{"label": "x: y"}},
		{in: "claude", bad: true},
		{in: "=x", bad: true},
		{in: "", bad: true},
	}
	for _, tc := range tests {
		got, err := ParseArm(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("ParseArm(%q) accepted", tc.in)
			}
			continue
		}
		if err != nil || got.Agent != tc.agent || (tc.set != nil && !reflect.DeepEqual(got.Set, tc.set)) {
			t.Errorf("ParseArm(%q) = %+v, %v", tc.in, got, err)
		}
	}
}

// decode strictly decodes an experiment file as `run` and `check` do.
func decode(t *testing.T, raw []byte) schemas.Experiment {
	t.Helper()
	var exp schemas.Experiment
	if err := suite.DecodeYAML(raw, "compare.yaml", suite.SchemaExperiment, &exp); err != nil {
		t.Fatalf("experiment does not decode: %v\n%s", err, raw)
	}
	return exp
}

func TestExperimentYAMLForms(t *testing.T) {
	t.Run("two configs", func(t *testing.T) {
		raw, err := ExperimentYAML(CompareOptions{Name: "c1", Suite: "minitext",
			A: ArmInput{Agent: "agents/oracle.yaml"}, B: ArmInput{Agent: "agents/noop.yaml"}, Trials: 1, Smoke: true})
		if err != nil {
			t.Fatal(err)
		}
		e := decode(t, raw)
		if e.Base != "agents/oracle.yaml" || e.Arms["A"].Agent != "" || e.Arms["B"].Agent != "agents/noop.yaml" || !e.Smoke {
			t.Errorf("arms: base %s A %+v B %+v smoke %v", e.Base, e.Arms["A"], e.Arms["B"], e.Smoke)
		}
		if len(e.Conditions) != 1 || e.Conditions[0].ID != "clean" || e.Conditions[0].Trials != 1 || !e.Conditions[0].Tasks.All {
			t.Errorf("conditions %+v", e.Conditions)
		}
		if e.Policy != DefaultPolicy || e.Verdict.Policy != "paired_signflip" || e.Primary.Metric != "verified_success" || e.Budget.TotalUSD != 0 {
			t.Errorf("experiment %+v", e)
		}
	})
	t.Run("two models with faults", func(t *testing.T) {
		a, _ := ParseArm("model=claude-sonnet-4-5")
		b, _ := ParseArm("model=claude-haiku-4-5,budgets.wall_s=600")
		raw, err := ExperimentYAML(CompareOptions{Name: "c2", Suite: "s", Base: "agents/claude-code.yaml", A: a, B: b,
			Faults: []string{"model-outage", "clean", "model-outage", "tool-error"}, BudgetUSD: 4.5, Concurrency: 4, Policy: "none"})
		if err != nil {
			t.Fatal(err)
		}
		e := decode(t, raw)
		if e.Arms["B"].Set["model"] != "claude-haiku-4-5" || e.Arms["B"].Set["budgets.wall_s"] != 600 || e.Arms["A"].Set["model"] != "claude-sonnet-4-5" {
			t.Errorf("arm sets %+v %+v", e.Arms["A"].Set, e.Arms["B"].Set)
		}
		var ids []string
		for _, c := range e.Conditions {
			ids = append(ids, c.ID)
			if c.Trials != DefaultTrials {
				t.Errorf("%s trials %d", c.ID, c.Trials)
			}
		}
		if strings.Join(ids, ",") != "clean,model-outage,tool-error" || e.Budget.TotalUSD != 4.5 || e.Concurrency != 4 || e.Policy != "" || e.Smoke {
			t.Errorf("conditions %v budget %v concurrency %d policy %q", ids, e.Budget.TotalUSD, e.Concurrency, e.Policy)
		}
		if e.Arms["B"].Label != "budgets.wall_s=600 model=claude-haiku-4-5" {
			t.Errorf("label %q", e.Arms["B"].Label)
		}
	})
	t.Run("refs", func(t *testing.T) {
		raw, err := ExperimentYAML(CompareOptions{Name: "c3", Suite: "s", Base: "agents/claude-code.yaml",
			A: ArmInput{Label: "main (abc)", Set: map[string]any{"notes.user_file": "experiments/fixtures/c3/A/CLAUDE.md"}},
			B: ArmInput{Label: "HEAD (def)", Agent: "experiments/fixtures/c3/B/.heimdall/agents/claude-code.yaml",
				Set: map[string]any{"notes.user_file": "experiments/fixtures/c3/B/CLAUDE.md", "skills.extra": []any{"experiments/fixtures/c3/B/.claude/skills/review"}}},
			Command: "heimdall compare --baseline main --candidate HEAD"})
		if err != nil {
			t.Fatal(err)
		}
		e := decode(t, raw)
		if e.Arms["A"].Label != "main (abc)" || e.Arms["B"].Agent == "" || !reflect.DeepEqual(e.Arms["B"].Set["skills.extra"], []any{"experiments/fixtures/c3/B/.claude/skills/review"}) {
			t.Errorf("arms %+v", e.Arms)
		}
		if !strings.Contains(string(raw), "# from: heimdall compare --baseline main --candidate HEAD") {
			t.Error("command line not recorded")
		}
	})
	if _, err := ExperimentYAML(CompareOptions{Name: "x", Suite: "s", A: ArmInput{Set: map[string]any{"model": "m"}}, B: ArmInput{Agent: "agents/b.yaml"}}); err == nil {
		t.Error("an override arm without a base config was accepted")
	}
	if _, err := ExperimentYAML(CompareOptions{Name: "x", A: ArmInput{Agent: "a.yaml"}, B: ArmInput{Agent: "b.yaml"}}); err == nil {
		t.Error("no suite accepted")
	}
	if TierSupported() {
		raw, err := ExperimentYAML(CompareOptions{Name: "x", Suite: "s", Tier: "pr", A: ArmInput{Agent: "a.yaml"}, B: ArmInput{Agent: "b.yaml"}})
		if err != nil {
			t.Fatal(err)
		}
		e := decode(t, raw)
		if v := reflect.ValueOf(e).FieldByName("Tier"); !v.IsValid() || v.String() != "pr" {
			t.Errorf("tier not written: %s", raw)
		}
	} else {
		if _, err := ExperimentYAML(CompareOptions{Name: "x", Suite: "s", Tier: "pr", A: ArmInput{Agent: "a.yaml"}, B: ArmInput{Agent: "b.yaml"}}); err == nil {
			t.Error("--tier accepted by a build without tiers")
		}
	}
}

func TestExperimentName(t *testing.T) {
	for in, want := range map[string]string{"Compare 2026/11/18": "compare-2026-11-18", "": "compare", "pr-gate": "pr-gate"} {
		if got := ExperimentName(in); got != want {
			t.Errorf("ExperimentName(%q) = %q, want %q", in, got, want)
		}
	}
}
