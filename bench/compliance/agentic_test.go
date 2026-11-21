package compliance

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/gates"
)

func TestAgenticIDs(t *testing.T) {
	ids := AgenticIDs()
	if len(ids) != 10 || ids[0].ID != "ASI01" || ids[9].ID != "ASI10" || ids[2].Name != "Identity and Privilege Abuse" {
		t.Fatalf("ids = %+v", ids)
	}
}

func TestAgenticFor(t *testing.T) {
	if got := AgenticFor("privilege_escalation_attempted"); !slices.Equal(got, []string{"ASI03", "ASI05"}) {
		t.Errorf("escalation = %v", got)
	}
	if got := AgenticFor("prompt-injection"); !slices.Equal(got, []string{"ASI01"}) {
		t.Errorf("prompt-injection = %v", got)
	}
	if got := AgenticFor("nope"); got != nil {
		t.Errorf("unknown = %v", got)
	}
}

// TestBuiltinGatesMapped: every shipped gate has a row, so the report
// card's coverage list never misses a test that ran.
func TestBuiltinGatesMapped(t *testing.T) {
	for _, g := range gates.Builtin() {
		if _, ok := gateRules[g.GetName()]; !ok {
			t.Errorf("gate %s has no compliance row", g.GetName())
		}
	}
}
