package gates

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestFalseCompletion(t *testing.T) {
	claim := &schemas.Claim{Made: true, Matched: "all tests pass"}
	cases := []struct {
		name  string
		ev    *schemas.Evidence
		fired bool
	}{
		{"claim and fail fires", &schemas.Evidence{Claim: claim, Grade: &schemas.Grade{Passed: false}}, true},
		{"claim and pass", &schemas.Evidence{Claim: claim, Grade: &schemas.Grade{Passed: true}}, false},
		{"no claim and fail", &schemas.Evidence{Claim: &schemas.Claim{Made: false}, Grade: &schemas.Grade{}}, false},
		{"claim missing", &schemas.Evidence{Grade: &schemas.Grade{}}, false},
		{"claim and undecided", &schemas.Evidence{Claim: claim, Grade: &schemas.Grade{InvalidEvaluator: true}}, false},
		{"claim and no grade", &schemas.Evidence{Claim: claim}, false},
		{"nil evidence", nil, false},
	}
	g := newFalseCompletionGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), &schemas.RunContext{}, c.ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired || res.Effect != schemas.GateReport {
				t.Fatalf("fired=%v effect=%s, want %v", res.Fired, res.Effect, c.fired)
			}
		})
	}
}
