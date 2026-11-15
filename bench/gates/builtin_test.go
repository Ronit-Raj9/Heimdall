package gates

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// testEditGates are the gates whose version follows testEditVersion.
var testEditGates = map[string]bool{ProtectedPaths: true, Regrade: true, MemoryTrapFollowed: true}

func TestBuiltin(t *testing.T) {
	want := map[string]struct {
		effect   schemas.GateEffect
		baseline bool
	}{
		ProtectedPaths:    {schemas.GateVeto, true},
		TestInfra:         {schemas.GateVeto, true},
		Regrade:           {schemas.GateVeto, true},
		NullAgents:        {schemas.GateAdmission, true},
		Egress:            {schemas.GateInvalidate, true},
		UpstreamErrors:    {schemas.GateReport, true},
		FalseCompletion:   {schemas.GateReport, false},
		ModelFidelity:     {schemas.GateInvalidate, false},
		DestroyedUserWork: {schemas.GateVeto, false},
		ProtectedPath:     {schemas.GateVeto, false},
		InjectionFollowed: {schemas.GateVeto, false},

		MemoryTrapFollowed: {schemas.GateVeto, false},

		ToolInjectionFollowed: {schemas.GateVeto, false}, // Tool safety
		ImpossiblePass:        {schemas.GateVeto, true},  // SWE importer and impossible tasks

		CanaryEchoed: {schemas.GateReport, false}, // Suite health: contamination canaries
	}
	gates := Builtin()
	if len(gates) != len(want) {
		t.Fatalf("got %d gates, want %d", len(gates), len(want))
	}
	seen := map[string]bool{}
	for _, g := range gates {
		t.Run(g.GetName(), func(t *testing.T) {
			w, ok := want[g.GetName()]
			if !ok {
				t.Fatalf("unexpected gate %q", g.GetName())
			}
			if seen[g.GetName()] {
				t.Fatalf("duplicate gate %q", g.GetName())
			}
			seen[g.GetName()] = true
			wantVersion := "1.0.0"
			if testEditGates[g.GetName()] {
				wantVersion = "1.1.0"
			}
			if g.GetVersion() != wantVersion {
				t.Errorf("version %q, want %q", g.GetVersion(), wantVersion)
			}
			if g.Effect() != w.effect || g.Baseline() != w.baseline {
				t.Errorf("effect %s baseline %v, want %s %v", g.Effect(), g.Baseline(), w.effect, w.baseline)
			}
		})
	}
}

func TestBaselineNames(t *testing.T) {
	got := BaselineNames()
	want := []string{ProtectedPaths, TestInfra, Regrade, NullAgents, Egress, UpstreamErrors, ImpossiblePass}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewResultStampsIdentity(t *testing.T) {
	g := newRegradeGate()
	r := newResult(g, true, "d", []string{"e"})
	if r.Gate != Regrade || r.Version != testEditVersion || r.Effect != schemas.GateVeto || !r.Fired {
		t.Fatalf("bad result %+v", r)
	}
}
