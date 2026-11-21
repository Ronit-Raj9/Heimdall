package leaderboard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSimulatedUserExclusion(t *testing.T) {
	exp := func(mode string) *expInfo {
		m := &schemas.Manifest{}
		if mode != "" {
			m.Simulator = &schemas.SimulatorSpec{Mode: mode, Provider: "anthropic", Model: "claude-sim"}
		}
		return &expInfo{id: "e", m: m}
	}
	pass := schemas.Run{Outcome: schemas.OutcomePass}
	for name, tt := range map[string]struct {
		rec  record
		want string
	}{
		"no simulator":          {record{exp: exp(""), sealed: true, run: pass}, ""},
		"scripted user counts":  {record{exp: exp(schemas.SimulatorScripted), sealed: true, run: pass}, ""},
		"llm user excluded":     {record{exp: exp(schemas.SimulatorLLM), sealed: true, run: pass}, ReasonSimulatedUser},
		"invalid verdict first": {record{exp: &expInfo{id: "e", invalid: true, m: exp(schemas.SimulatorLLM).m}, sealed: true, run: pass}, ReasonVerdictInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			if got := excludeReason(&tt.rec); got != tt.want {
				t.Errorf("excludeReason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSimulatorPinAndBoard(t *testing.T) {
	llm := &schemas.Manifest{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: "claude-sim", PromptVersion: "v2"}}
	if got := SimulatorPin(llm); got != "anthropic/claude-sim@v2" {
		t.Fatalf("pin %q", got)
	}
	if SimulatorPin(&schemas.Manifest{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}}) != "" || SimulatorPin(nil) != "" {
		t.Fatal("scripted or no simulator has no pin")
	}
	sim := &record{exp: &expInfo{id: "e", m: llm}, sealed: true, run: schemas.Run{Outcome: schemas.OutcomePass}}
	plain := &record{exp: &expInfo{id: "p", m: &schemas.Manifest{}}, sealed: true, run: schemas.Run{Outcome: schemas.OutcomePass}}
	q := Query{Customer: CustomerSimulated}
	if !onCustomerBoard(sim, q) || onCustomerBoard(plain, q) {
		t.Fatal("simulated board keeps only model-played runs")
	}
	if q.Simulator = "other/x"; onCustomerBoard(sim, q) {
		t.Fatal("simulated board keeps only its pin")
	}
	if !onCustomerBoard(sim, Query{}) || !onCustomerBoard(plain, Query{}) {
		t.Fatal("default board sees every run (exclusion lists the model-played ones)")
	}
	if got := excludeReasonSimulated(sim); got != "" {
		t.Fatalf("simulated board excluded its own run: %q", got)
	}
	if got := excludeReason(sim); got != ReasonSimulatedUser {
		t.Fatalf("default board reason %q", got)
	}
	if _, err := (Query{Customer: "robot"}).Normalize(); err == nil {
		t.Fatal("bad customer accepted")
	}
}
