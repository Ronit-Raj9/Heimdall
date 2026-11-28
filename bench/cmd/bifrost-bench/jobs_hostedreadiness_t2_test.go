package main

import (
	"strings"
	"testing"

	benchagents "github.com/maximhq/bifrost/bench/agents"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The hosted readiness plan of a task with variants (the refund story) has
// one arm per variant, its checks are named variant_<name> as local
// admission names them, and a variant that passes refuses the task by name.
func TestReadinessVariants(t *testing.T) {
	s, tasks := readinessFixture(t)
	refund := *tasks[0]
	refund.Controls.Variants = []schemas.TaskVariant{
		{Name: "claims_issued", File: "controls/claims-issued.sh"},
		{Name: "no_tool_call", File: "controls/no-tool-call.sh"},
		{Name: "wrong_order_id", File: "controls/wrong-order-id.sh"},
	}
	ts := []*schemas.Task{&refund}
	e := readinessExperiment("qa", bridgeOracleAgent, bridgeNoopAgent, s, ts, schemas.SplitPublic, false)
	base, err := benchagents.Files.ReadFile("oracle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"variant_1", "variant_2", "variant_3"} {
		a, ok := e.Arms[id]
		if !ok {
			t.Fatalf("no arm %s: %v", id, e.Arms)
		}
		cfg, err := engine.ResolveAgentConfig(base, a.Set)
		if err != nil {
			t.Fatalf("arm %s: %v", id, err)
		}
		if cfg.Scripted.Kind != "variant" || cfg.Scripted.Index != i+1 {
			t.Fatalf("arm %s resolves to %s/%d", id, cfg.Scripted.Kind, cfg.Scripted.Index)
		}
	}
	runs := func(passing string) []bridgeRun {
		var out []bridgeRun
		for arm := range e.Arms {
			o := "fail"
			if isReferenceArm(arm) || arm == passing {
				o = "pass"
			}
			out = append(out, bridgeRun{Task: refund.ID, Arm: arm, Outcome: o, RunID: "r-" + arm})
		}
		return out
	}
	got := readinessVerdict(s, ts, runs(""), nil, nil)
	names := map[string]bool{}
	for _, c := range got[0].Checks {
		names[c.Name] = c.Passed
	}
	for _, n := range []string{"variant_claims_issued", "variant_no_tool_call", "variant_wrong_order_id"} {
		if passed, ok := names[n]; !ok || !passed {
			t.Errorf("check %s: present %v passed %v", n, ok, passed)
		}
	}
	if !got[0].Admitted {
		t.Fatalf("every variant failed, task not admitted: %+v", got[0])
	}
	got = readinessVerdict(s, ts, runs("variant_2"), nil, nil)
	if got[0].Admitted || !strings.Contains(got[0].Reason, "no_tool_call") {
		t.Fatalf("a passing variant: admitted %v reason %q", got[0].Admitted, got[0].Reason)
	}
	// An invalid variant is a check that fails, never left out.
	bad := refund
	bad.Controls.Variants = []schemas.TaskVariant{{Name: "outside", File: "verifier/x.sh"}}
	got = readinessVerdict(s, []*schemas.Task{&bad}, runs(""), nil, nil)
	if got[0].Admitted {
		t.Fatalf("a variant outside controls/ admitted the task: %+v", got[0])
	}
}
