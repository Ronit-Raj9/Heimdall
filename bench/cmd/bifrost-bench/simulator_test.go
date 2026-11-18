package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/simuser"
)

func TestSimulatorUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"spread"}, {"spread", "-a", "e_1"}} {
		if code := runSimulator(args); code != exitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}

func TestPrintSpread(t *testing.T) {
	var b strings.Builder
	printSpread(&b, &simuser.SpreadReport{Suite: "acme-helpdesk@1.0.0", ExpA: "e_a", ExpB: "e_b", SimulatorA: "simulated user (model: a/x)", SimulatorB: "simulated user (model: a/y)",
		Arms: []simuser.SpreadArm{{Arm: "A", SuccessA: 0.5, SuccessB: 0.25, Spread: 0.25}}, Message: "WARNING: x"})
	out := b.String()
	for _, want := range []string{"simulated user (model: a/x)", "50%", "25pt", "WARNING"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
