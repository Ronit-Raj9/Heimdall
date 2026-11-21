package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestPracticeRun covers X01: a run is labelled practice for a smoke
// experiment, a scripted control agent or a scripted ledger line, and not
// for a real run.
func TestPracticeRun(t *testing.T) {
	real := &schemas.RunContext{Agent: &schemas.AgentConfig{Adapter: "command"}, Experiment: &schemas.Experiment{}}
	if practiceRun(real, []schemas.LedgerEntry{{Upstream: schemas.LedgerUpstreamReal}}) {
		t.Fatal("a real run labelled practice")
	}
	if !practiceRun(real, []schemas.LedgerEntry{{Upstream: schemas.LedgerUpstreamReal}, {Upstream: schemas.LedgerUpstreamScripted}}) {
		t.Fatal("a scripted ledger line not labelled practice")
	}
	smoke := &schemas.RunContext{Agent: &schemas.AgentConfig{Adapter: "command"}, Experiment: &schemas.Experiment{Smoke: true}}
	if !practiceRun(smoke, nil) {
		t.Fatal("a smoke run not labelled practice")
	}
	oracle := &schemas.RunContext{Agent: &schemas.AgentConfig{Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: "oracle"}}}
	if !practiceRun(oracle, nil) {
		t.Fatal("a scripted control not labelled practice")
	}
	if practiceRun(nil, nil) {
		t.Fatal("nil run context labelled practice")
	}
}
