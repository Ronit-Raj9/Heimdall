package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheckA2AArm(t *testing.T) {
	stream := &schemas.ProbeSpec{ID: "stream-drop", Layer: schemas.LayerA2A, Apply: schemas.ProbeApply{A2A: &schemas.A2AFault{Stream: "drop", AfterEvents: 1}}}
	a2aCfg := func(streaming bool) *schemas.AgentConfig {
		return &schemas.AgentConfig{ID: "x", Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://a/card", Streaming: streaming}}
	}
	if err := checkA2AArm(a2aCfg(true), []condPlan{{id: "clean"}, {id: "stream-drop", probe: stream}}); err != nil {
		t.Fatalf("a streaming a2a arm runs the stream probe: %v", err)
	}
	if err := checkA2AArm(&schemas.AgentConfig{ID: "c", Adapter: "command"}, []condPlan{{id: "clean"}}); err != nil {
		t.Fatalf("a non-a2a arm without the probe is untouched: %v", err)
	}
	cases := map[string]struct {
		cfg   *schemas.AgentConfig
		conds []condPlan
		want  string
	}{
		"probe on a non-a2a arm":       {&schemas.AgentConfig{ID: "c", Adapter: "command"}, []condPlan{{probe: stream}}, "not an a2a agent"},
		"probe on a unary arm":         {a2aCfg(false), []condPlan{{probe: stream}}, "does not stream"},
		"bad stream fault":             {a2aCfg(true), []condPlan{{probe: &schemas.ProbeSpec{ID: "p", Layer: schemas.LayerA2A, Apply: schemas.ProbeApply{A2A: &schemas.A2AFault{Stream: "cut"}}}}}, "want drop or delay"},
		"a2a block on another adapter": {&schemas.AgentConfig{ID: "c", Adapter: "command", A2A: &schemas.A2AAgent{CardURL: "http://a/card"}}, nil, "needs adapter a2a"},
		"team container members":       {&schemas.AgentConfig{ID: "c", Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://a/card"}, Team: []schemas.TeamMember{{ID: "w"}}}, nil, "a2a.team"},
	}
	for name, c := range cases {
		err := checkA2AArm(c.cfg, c.conds)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, c.want)
		}
	}
}

func TestA2ATaskErrs(t *testing.T) {
	suite := &schemas.Suite{ID: "s", Output: &schemas.OutputSpec{Kind: schemas.OutputDiff}}
	task := &schemas.Task{ID: "t1"}
	arms := map[string]schemas.ArmManifest{
		"A": {Config: schemas.AgentConfig{ID: "a", Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://a/card"}}},
		"B": {Config: schemas.AgentConfig{ID: "b", Adapter: "command"}},
	}
	errs := a2aTaskErrs(suite, task, arms, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "arm A runs an A2A agent") {
		t.Fatalf("a diff task is refused for the a2a arm only: %v", errs)
	}
	suite.Output.Kind = schemas.OutputState
	errs = a2aTaskErrs(suite, task, arms, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "a2a.tools") {
		t.Fatalf("a state task needs a2a.tools expose: %v", errs)
	}
	suite.Output.Kind = schemas.OutputAnswer
	if errs := a2aTaskErrs(suite, task, arms, nil); len(errs) != 0 {
		t.Fatalf("an answer task runs: %v", errs)
	}
}
