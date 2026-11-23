package reporters

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3 (H09's setups verdict): the markdown report of a comparison of
// three or more setups leads with the setups result and does not present the
// descriptive B-against-A statistics as the result.
func TestMarkdownSetupsVerdict(t *testing.T) {
	a := testAnalysis()
	p, padj := 0.5, 1.0
	a.Verdict.Decision, a.Verdict.Action = schemas.DecisionLeader, schemas.ActionNoDecision
	a.Verdict.Setups = &schemas.SetupsVerdict{Leader: "C", TopGroup: []string{"C", "B"}, Behind: []string{"A"}, Separable: true,
		Note: "C leads; B cannot be told apart from C",
		Pairs: []schemas.MatrixPair{
			{ArmX: "C", ArmY: "B", Tasks: 8, NNonzero: 2, MeanDiff: 0.125, P: &p, PAdjusted: &padj, Reason: "cannot separate: too few differing tasks (2 of 8)"},
			{ArmX: "C", ArmY: "A", Tasks: 8, NNonzero: 6, MeanDiff: 0.5, P: &p, PAdjusted: &padj, Significant: true},
		},
		Safety: map[string][]string{"A": {"t03"}}}
	var buf bytes.Buffer
	if err := newMarkdownReporter().Write(context.Background(), a, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Bifrost Bench: LEADER, action NO_DECISION",
		"## Setups",
		"| Leader | C |",
		"| Top group | C, B |",
		"| Behind the leader | A |",
		"C leads; B cannot be told apart from C",
		"| C | B | 8 | 2 | +12.5 pp |",
		"| A | t03 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"## Primary endpoint", "| d_bar |", "## Noise floor"} {
		if strings.Contains(out, not) {
			t.Errorf("setups report presents B against A as the result: has %q", not)
		}
	}
}
