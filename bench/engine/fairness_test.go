package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestCheckFairness(t *testing.T) {
	arm := func(usd float64, topology string, members ...float64) schemas.ArmManifest {
		cfg := schemas.AgentConfig{Budgets: schemas.Budgets{USD: usd}, Topology: topology}
		for i, b := range members {
			cfg.Team = append(cfg.Team, schemas.TeamMember{ID: string(rune('a' + i)), BudgetUSD: b})
		}
		return schemas.ArmManifest{Config: cfg}
	}
	tests := []struct {
		name     string
		rule     string
		arms     map[string]schemas.ArmManifest
		wantErr  []string
		wantWarn []string
		wantInfo bool
	}{
		{name: "no rule checks nothing", arms: map[string]schemas.ArmManifest{"A": arm(0, ""), "B": arm(5, "")}},
		{name: "unknown rule", rule: "equal_tokens", arms: map[string]schemas.ArmManifest{"A": arm(1, "")},
			wantErr: []string{`fairness "equal_tokens" is not a known rule`}},
		{name: "unequal caps", rule: schemas.FairnessEqualBudget, arms: map[string]schemas.ArmManifest{"a": arm(1, ""), "b": arm(2, schemas.TopologyTeam, 1)},
			wantErr: []string{"fairness equal_budget: arms differ in total USD cap: a=1.00 b=2.00"}},
		{name: "missing cap", rule: schemas.FairnessEqualBudget, arms: map[string]schemas.ArmManifest{"a": arm(2, ""), "b": arm(0, "")},
			wantErr: []string{"fairness equal_budget: arm b has no total USD cap (budgets.usd)", "arms differ in total USD cap: a=2.00 b=0.00"}},
		{name: "member sub-budgets above the cap", rule: schemas.FairnessEqualBudget, arms: map[string]schemas.ArmManifest{"a": arm(2, ""), "b": arm(2, schemas.TopologyTeam, 1, 1.5)},
			wantErr: []string{"fairness equal_budget: arm b member sub-budgets sum to 2.50 USD, above its total cap 2.00"}},
		{name: "equal caps, members fit, no best-of-N warns", rule: schemas.FairnessEqualBudget,
			arms:     map[string]schemas.ArmManifest{"a": arm(2, ""), "b": arm(2, schemas.TopologyTeam, 1, 1)},
			wantWarn: []string{"single_agent_baseline=true", "best_of_n_baseline=false"}},
		{name: "a team without topology is no single-agent baseline", rule: schemas.FairnessEqualBudget,
			arms:     map[string]schemas.ArmManifest{"a": arm(2, schemas.TopologyBestOfN), "b": arm(2, "", 1)},
			wantWarn: []string{"single_agent_baseline=false", "best_of_n_baseline=true"}},
		{name: "both baselines", rule: schemas.FairnessEqualBudget,
			arms:     map[string]schemas.ArmManifest{"s": arm(2, schemas.TopologySingle), "n": arm(2, schemas.TopologyBestOfN), "t": arm(2, schemas.TopologyTeam, 2)},
			wantInfo: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log bytes.Buffer
			err := checkFairness(&schemas.Experiment{ID: "e_x", Fairness: tt.rule}, tt.arms, utils.NewStdLogger(&log, false))
			if len(tt.wantErr) == 0 && err != nil {
				t.Fatalf("err = %v", err)
			}
			for _, w := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Fatalf("err = %v, want it to contain %q", err, w)
				}
			}
			out := log.String()
			for _, w := range tt.wantWarn {
				if !strings.Contains(out, "WARN") || !strings.Contains(out, w) {
					t.Errorf("log %q, want a warning with %q", out, w)
				}
			}
			if len(tt.wantWarn) == 0 && strings.Contains(out, "WARN") {
				t.Errorf("unexpected warning: %q", out)
			}
			if tt.wantInfo != strings.Contains(out, "both baselines present") {
				t.Errorf("log %q, want info %v", out, tt.wantInfo)
			}
		})
	}
}

func TestPlanFairness(t *testing.T) {
	tests := []struct {
		name    string
		a, b    map[string]any
		wantErr string
	}{
		{name: "unequal caps are refused", a: map[string]any{"budgets.usd": 2.0}, b: map[string]any{"budgets.usd": 3.0},
			wantErr: "fairness equal_budget: arms differ in total USD cap: A=2.00 B=3.00"},
		{name: "a missing cap is refused", b: map[string]any{"budgets.usd": 2.0},
			wantErr: "fairness equal_budget: arm A has no total USD cap (budgets.usd)"},
		{name: "equal caps plan", a: map[string]any{"budgets.usd": 2.0}, b: map[string]any{"budgets.usd": 2.0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			e := testExperiment()
			e.Fairness = schemas.FairnessEqualBudget
			e.Arms["A"] = schemas.ArmSpec{Label: "baseline", Set: tt.a}
			setB := map[string]any{"notes.user_file": "experiments/fixtures/codewins.md"}
			for k, v := range tt.b {
				setB[k] = v
			}
			e.Arms["B"] = schemas.ArmSpec{Label: "note", Set: setB}
			var log bytes.Buffer
			m, err := NewPlanner(b.reg, b.root, utils.NewStdLogger(&log, false)).Plan(context.Background(), e)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if m.Experiment.Fairness != schemas.FairnessEqualBudget || m.Arms["A"].Config.Budgets.USD != 2 || m.Arms["B"].Config.Budgets.USD != 2 {
				t.Fatalf("manifest lost the rule or the caps: %q %+v %+v", m.Experiment.Fairness, m.Arms["A"].Config.Budgets, m.Arms["B"].Config.Budgets)
			}
			if !strings.Contains(log.String(), "best_of_n_baseline=false") {
				t.Errorf("no baseline warning in %q", log.String())
			}
		})
	}
}
