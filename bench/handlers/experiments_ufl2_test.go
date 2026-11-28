package handlers

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
)

// ufl2Agent is a base agent with a gateway provider, as routing needs.
const ufl2Agent = `id: claude-code
adapter: command
image: bench/agent:1
argv: [agent]
model: claude-test
budgets: {wall_s: 900, cpus: 2, mem_gb: 4}
gateway: {provider: anthropic}
`

// TestUFL2WebArmSetsResolve takes the arm B sets the New comparison form
// sends (testdata/ufl2_web_experiment.json, checked against the form's own
// toExperiment in experimentFormSchema.ufl2.test.ts) through the experiment
// decoder (DecodeExperiment, what POST /experiments/plan reads) and the agent override resolver the planner uses.
func TestUFL2WebArmSetsResolve(t *testing.T) {
	raw, err := os.ReadFile("testdata/ufl2_web_experiment.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Arms     map[string]map[string]any `json:"arms"`
		Advanced map[string]any            `json:"advanced"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	for kind, set := range fx.Arms {
		t.Run(kind, func(t *testing.T) {
			exp := map[string]any{
				"name": "demo", "suite": "s@1", "base": "agents/a.yaml",
				"arms":       map[string]any{"A": map[string]any{"label": "baseline"}, "B": map[string]any{"label": "changed", "set": set}},
				"primary":    map[string]any{"condition": "clean", "metric": "verified_success"},
				"conditions": []any{map[string]any{"id": "clean", "tasks": map[string]any{"all": true}, "trials": 3}},
				"budget":     map[string]any{"total_usd": 5},
				"verdict":    map[string]any{"policy": "paired_signflip", "alpha": 0.05},
			}
			doc, err := json.Marshal(exp)
			if err != nil {
				t.Fatal(err)
			}
			e, _, err := DecodeExperiment("application/json", doc)
			if err != nil {
				t.Fatalf("experiment schema refuses the form's %s set: %v", kind, err)
			}
			cfg, err := engine.ResolveAgentConfig([]byte(ufl2Agent), e.Arms["B"].Set)
			if err != nil {
				t.Fatalf("override resolver refuses the form's %s set: %v", kind, err)
			}
			switch kind {
			case "routing", "routing_fallback_chain":
				if cfg.Gateway.Routing == nil {
					t.Fatalf("routing not applied")
				}
			case "prompt_cache", "code_mode":
				if cfg.Gateway.Bifrost == nil {
					t.Fatalf("bifrost features not applied")
				}
			case "failover":
				if len(cfg.Gateway.Fallbacks) != 2 {
					t.Fatalf("fallbacks not applied")
				}
			case "team":
				if len(cfg.Team) != 2 {
					t.Fatalf("team not applied: %d", len(cfg.Team))
				}
			}
		})
	}
	t.Run("advanced", func(t *testing.T) {
		exp := map[string]any{
			"name": "demo", "suite": "s@1", "base": "agents/a.yaml",
			"arms":    map[string]any{"A": map[string]any{"label": "baseline"}, "B": map[string]any{"label": "changed", "set": map[string]any{"model": "x"}}},
			"primary": map[string]any{"condition": "clean", "metric": "verified_success"},
			"budget":  map[string]any{"total_usd": 5},
		}
		for k, v := range fx.Advanced {
			exp[k] = v
		}
		doc, err := json.Marshal(exp)
		if err != nil {
			t.Fatal(err)
		}
		e, _, err := DecodeExperiment("application/json", doc)
		if err != nil {
			t.Fatalf("experiment schema refuses the form's advanced options: %v", err)
		}
		if !e.AA || e.Verdict.Alpha != 0.01 || len(e.FramingPairs) != 1 || e.Conditions[0].Split != "held_out" {
			t.Fatalf("advanced options lost: %+v", e)
		}
	})
}
