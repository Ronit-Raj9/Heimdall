package simuser

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func side(id, model string, success map[string]schemas.Ratio) SpreadSide {
	return SpreadSide{ExpID: id, Suite: "acme-helpdesk@1.0.0", Success: success,
		Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: model, PromptVersion: PromptUserV1, MaxTurns: 12}}
}

func TestSpreadWarnsWhenSimulatorMovesMoreThanEffect(t *testing.T) {
	a := side("e_a", "sim-1", map[string]schemas.Ratio{"A": {Num: 4, Den: 6}, "B": {Num: 5, Den: 6}})
	b := side("e_b", "sim-2", map[string]schemas.Ratio{"A": {Num: 2, Den: 6}, "B": {Num: 5, Den: 6}})
	rep, err := Spread(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Warn || rep.MaxSpread < 0.33 || rep.Effect > 0.17 || rep.EffectArms != [2]string{"A", "B"} || !strings.Contains(rep.Message, "WARNING") {
		t.Fatalf("report %+v", rep)
	}
	if rep.SimulatorA != "simulated user (model: anthropic/sim-1)" {
		t.Fatalf("label %q", rep.SimulatorA)
	}
}

func TestSpreadQuietWhenEffectIsLarger(t *testing.T) {
	a := side("e_a", "sim-1", map[string]schemas.Ratio{"A": {Num: 1, Den: 6}, "B": {Num: 6, Den: 6}})
	b := side("e_b", "sim-2", map[string]schemas.Ratio{"A": {Num: 1, Den: 6}, "B": {Num: 5, Den: 6}})
	rep, err := Spread(a, b)
	if err != nil || rep.Warn {
		t.Fatalf("report %+v err %v", rep, err)
	}
}

func TestSpreadRefusals(t *testing.T) {
	ok := map[string]schemas.Ratio{"A": {Num: 1, Den: 2}}
	scripted := side("e_s", "x", ok)
	scripted.Simulator = &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}
	other := side("e_o", "sim-2", ok)
	other.Suite = "other@1.0.0"
	for name, pair := range map[string][2]SpreadSide{
		"scripted":    {scripted, side("e_b", "sim-2", ok)},
		"same model":  {side("e_a", "sim-1", ok), side("e_b", "sim-1", ok)},
		"other suite": {side("e_a", "sim-1", ok), other},
		"no arm":      {side("e_a", "sim-1", ok), side("e_b", "sim-2", map[string]schemas.Ratio{"Z": {Num: 1, Den: 1}})},
		"undecided":   {side("e_a", "sim-1", ok), side("e_b", "sim-2", map[string]schemas.Ratio{"A": {Unknown: 2}})},
	} {
		if _, err := Spread(pair[0], pair[1]); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSuccessFromRuns(t *testing.T) {
	one, zero := 1.0, 0.0
	runs := []schemas.SealedRun{
		{Slot: schemas.Slot{Arm: "A", Condition: "clean", Counted: true}, Metrics: map[string]schemas.Value{"verified_success": {State: schemas.ValueObserved, V: &one}}},
		{Slot: schemas.Slot{Arm: "A", Condition: "clean", Counted: true}, Metrics: map[string]schemas.Value{"verified_success": {State: schemas.ValueObserved, V: &zero}}},
		{Slot: schemas.Slot{Arm: "A", Condition: "clean", Counted: true}, Metrics: map[string]schemas.Value{"verified_success": {State: schemas.ValueUnknown}}},
		{Slot: schemas.Slot{Arm: "A", Condition: "hostile-customer", Counted: true}, Metrics: map[string]schemas.Value{"verified_success": {State: schemas.ValueObserved, V: &one}}},
		{Slot: schemas.Slot{Arm: "compat", Condition: "clean"}, Metrics: map[string]schemas.Value{"verified_success": {State: schemas.ValueObserved, V: &one}}},
	}
	got := SuccessFromRuns(runs, "clean")
	if got["A"] != (schemas.Ratio{Num: 1, Den: 2, Unknown: 1}) || len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
}
