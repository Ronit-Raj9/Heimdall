package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCheckScenarioScopesCompatibility: a remote compatibility agent is
// checked only against its own condition's tasks (an answer task), not the
// main arms' diff task it never runs (audit 8-B-10).
func TestCheckScenarioScopesCompatibility(t *testing.T) {
	suite := &schemas.Suite{ID: "s", Tasks: []*schemas.Task{
		{ID: "diff"},
		{ID: "answer", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}},
	}}
	local := schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "command"}}
	remote := &schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{}}}
	p := &Planner{reg: &schemas.Registry{}}
	arms := map[string]schemas.ArmManifest{"a": local, "b": local}
	main := []condPlan{{id: "clean", tasks: []string{"diff"}}}
	if err := p.checkScenario(suite, main, []condPlan{{id: "compat", tasks: []string{"answer"}}}, arms, remote); err != nil {
		t.Fatalf("compatibility agent checked against a task it never runs: %v", err)
	}
	if err := p.checkScenario(suite, main, []condPlan{{id: "compat", tasks: []string{"diff"}}}, arms, remote); err == nil {
		t.Fatal("remote compatibility agent on its own diff task was not refused")
	}
}
