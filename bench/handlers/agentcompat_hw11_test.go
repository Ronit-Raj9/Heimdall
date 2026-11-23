package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The bare-model baseline's compatibility answers before its first launch
// wrote agents/bare-model.yaml into the project: the shipped config is used,
// so the wizard greys out BFCL before Launch (fix wave 3, N187).
func TestCompatibilityBareModelBeforeFirstLaunch(t *testing.T) {
	suites := &fakeSuites{
		ids: []string{"hd-harbor-bfcl", "qa"},
		suites: map[string]*schemas.Suite{
			"hd-harbor-bfcl": {ID: "hd-harbor-bfcl", Track: "support", Tasks: []*schemas.Task{{ID: "b1"}}},
			"qa":             {ID: "qa", Track: "support", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}, Tasks: []*schemas.Task{{ID: "q1"}}},
		},
	}
	// The org has other agents, but no bare-model.yaml yet.
	agents := compatAgents{{Path: "agents/codex.yaml", Config: &schemas.AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1"}}}
	env := OrgEnv{Mode: "local", Services: Services{Suites: suites, Agents: agents}}
	code, out := doCompat(t, env, "bare-model")
	if code != 200 || out.Agent != "bare-model" || len(out.Benchmarks) != 2 {
		t.Fatalf("status %d out %+v", code, out)
	}
	for _, b := range out.Benchmarks {
		switch b.ID {
		case "hd-harbor-bfcl":
			if b.OK || !strings.Contains(b.Reason, "a file the agent writes") {
				t.Fatalf("bfcl = %+v", b)
			}
		case "qa":
			if !b.OK {
				t.Fatalf("qa = %+v", b)
			}
		}
	}
}
