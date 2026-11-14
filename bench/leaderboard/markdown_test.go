package leaderboard

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMarkdown(t *testing.T) {
	live := buildOrFail(t, boardSource(), Query{Now: t0})
	synthetic := buildOrFail(t, boardSource(), Query{Synthetic: SyntheticOnly, Now: t0})
	empty := buildOrFail(t, boardSource(), Query{Suite: "nope@1", Now: t0})
	piped := buildOrFail(t, boardSource(), Query{Now: t0})
	piped.Entries[0].Label = "a|b"
	unknown := buildOrFail(t, boardSource(), Query{Now: t0})
	unknown.Entries[0].Metrics[MetricTokens] = schemas.Unknown("no rows", schemas.EvidenceGateway)
	unknown.Entries[0].Metrics[MetricCostPerSuccess] = schemas.NotApplicable()
	tests := []struct {
		name    string
		lb      *schemas.Leaderboard
		want    []string
		notWant []string
	}{
		{
			name: "live board",
			lb:   live,
			want: []string{
				"# Leaderboard: s@2, condition clean",
				"| Rank | Tier | Agent | Label |",
				"| 1 | 1 | agent-h_a | alpha two | model-h_a | `h_a` | 66.7% [0.0, 100.0] | 4/5 | unknown | 3 | 5 | $0.1250 | - | leader |",
				"| 2 | 1 | agent-h_b | beta |",
				"too few common tasks (2) to separate",
				"| model-outage |",
				"- experiment verdict invalid: 1 run (e_bad)",
				"- infra_error: 1 run (e_one)",
				"Common tasks (2): t1, t2.",
				"Generated 2026-11-14T10:00:00Z.",
			},
			notWant: []string{"SYNTHETIC"},
		},
		{
			name: "synthetic banner",
			lb:   synthetic,
			want: []string{"> **SYNTHETIC.** Scripted agent or scripted model runs only.", "| 1 | 1 | agent-h_toy | toy |", "Excluded runs: none."},
		},
		{
			name: "empty board",
			lb:   empty,
			want: []string{"No graded runs for this suite and condition.", "Common tasks (0): none."},
		},
		{
			name: "pipes are escaped",
			lb:   piped,
			want: []string{`| a\|b |`},
		},
		{
			name:    "unknown is never zero",
			lb:      unknown,
			want:    []string{"| 1 | `h_a` | 4.0 | unknown | unknown | $0.1000 |", "| n/a | - | leader |"},
			notWant: []string{"| 0 |"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := Markdown(tt.lb)
			for _, w := range tt.want {
				if !strings.Contains(md, w) {
					t.Errorf("missing %q in:\n%s", w, md)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(md, w) {
					t.Errorf("unexpected %q in:\n%s", w, md)
				}
			}
		})
	}
}

func TestShort(t *testing.T) {
	tests := map[string]string{
		"sha256:606c9210eb23aca5fa378fc487c6bf34": "606c9210eb23",
		"606c9210eb23aca5":                        "606c9210eb23",
		"h_a":                                     "h_a",
	}
	for in, want := range tests {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}
