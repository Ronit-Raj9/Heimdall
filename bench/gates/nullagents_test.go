package gates

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestNullAgents(t *testing.T) {
	scripted := func(kind string) *schemas.AgentConfig {
		return &schemas.AgentConfig{ID: "null-" + kind, Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: kind}}
	}
	pass := &schemas.Grade{Passed: true}
	fail := &schemas.Grade{Passed: false}
	cases := []struct {
		name  string
		agent *schemas.AgentConfig
		grade *schemas.Grade
		fired bool
	}{
		{"noop passed fires", scripted("noop"), pass, true},
		{"refuse passed fires", scripted("refuse"), pass, true},
		{"noop failed", scripted("noop"), fail, false},
		{"noop undecided", scripted("noop"), &schemas.Grade{Passed: true, InvalidEvaluator: true}, false},
		{"noop no grade", scripted("noop"), nil, false},
		{"oracle passed", scripted("oracle"), pass, false},
		{"command agent passed", &schemas.AgentConfig{Adapter: "command", Scripted: &schemas.ScriptedSpec{Kind: "noop"}}, pass, false},
	}
	g := newNullAgentsGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), &schemas.RunContext{Agent: c.agent}, &schemas.Evidence{Grade: c.grade})
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired || res.Effect != schemas.GateAdmission {
				t.Fatalf("fired=%v effect=%s detail=%q, want %v", res.Fired, res.Effect, res.Detail, c.fired)
			}
		})
	}
	if _, err := g.Check(context.Background(), &schemas.RunContext{}, &schemas.Evidence{}); err == nil {
		t.Fatal("want error without an agent")
	}
}
