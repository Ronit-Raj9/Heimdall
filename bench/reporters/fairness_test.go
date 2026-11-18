package reporters

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWriteFairness(t *testing.T) {
	tests := []struct {
		name   string
		f      *schemas.FairnessReport
		want   []string
		absent []string
	}{
		{name: "no rule writes nothing", f: nil},
		{name: "run over its cap, no best-of-N", f: &schemas.FairnessReport{
			Rule: schemas.FairnessEqualBudget, CapsUSD: map[string]float64{"B": 2, "A": 2},
			Topologies: map[string]string{"A": schemas.TopologySingle, "B": schemas.TopologyTeam},
			Exceeded:   []string{"r_1", "r_2"}, SingleAgentBaseline: true,
			Notes: []string{"no best-of-N single-agent baseline arm (topology best_of_n)"},
		}, want: []string{
			"## Fairness",
			"| Rule | equal_budget |",
			"| Caps (USD) | A 2.00, B 2.00 |",
			"| Topologies | A single, B team |",
			"| Runs over their arm's cap | r_1, r_2 |",
			"| Single-agent baseline | yes |",
			"| Best-of-N baseline | no |",
			"| Claim allowed | no |",
			"- no best-of-N single-agent baseline arm (topology best_of_n)",
			"A claim that multi-agent is better needs a single-agent baseline and a best-of-N single-agent baseline at the same spend: missing.",
		}},
		{name: "both baselines, nothing over", f: &schemas.FairnessReport{
			Rule: schemas.FairnessEqualBudget, CapsUSD: map[string]float64{"A": 1.5, "B": 1.5, "N": 1.5},
			SingleAgentBaseline: true, BestOfNBaseline: true, ClaimAllowed: true,
		}, want: []string{
			"| Caps (USD) | A 1.50, B 1.50, N 1.50 |",
			"| Runs over their arm's cap | none |",
			"| Claim allowed | yes |",
			"at the same spend: present.",
		}, absent: []string{"| Topologies |"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			w := bufio.NewWriter(&b)
			writeFairness(w, tt.f)
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if tt.f == nil && b.Len() != 0 {
				t.Fatalf("got %q, want nothing", b.String())
			}
			for _, s := range tt.want {
				if !strings.Contains(b.String(), s) {
					t.Errorf("missing %q in:\n%s", s, b.String())
				}
			}
			for _, s := range tt.absent {
				if strings.Contains(b.String(), s) {
					t.Errorf("unexpected %q in:\n%s", s, b.String())
				}
			}
		})
	}
}

func TestMarkdownFairnessSection(t *testing.T) {
	tests := []struct {
		name string
		f    *schemas.FairnessReport
		want bool
	}{
		{name: "without a rule the report has no fairness section"},
		{name: "with a rule it follows the instrument checks", f: &schemas.FairnessReport{Rule: schemas.FairnessEqualBudget, CapsUSD: map[string]float64{"A": 2, "B": 2}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testAnalysis()
			a.Verdict.Fairness = tt.f
			var buf bytes.Buffer
			if err := newMarkdownReporter().Write(context.Background(), a, nil, &buf); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			at := strings.Index(out, "## Fairness")
			if (at >= 0) != tt.want {
				t.Fatalf("fairness section present = %v, want %v", at >= 0, tt.want)
			}
			if tt.want && (at < strings.Index(out, "## Instrument checks") || at > strings.Index(out, "## Release policy")) {
				t.Errorf("fairness section out of place:\n%s", out)
			}
		})
	}
}
