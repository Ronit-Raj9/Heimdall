package leaderboard

import (
	"context"
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
	repeated := buildOrFail(t, kSource(), Query{Now: t0})
	short := buildOrFail(t, kSource(), Query{K: 4, Now: t0})
	latest := buildOrFail(t, regradeSource(t), Query{GradeSource: GradeSourceLatest, Now: t0})
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
				"| 1 | 1 | agent-h_a | alpha two | model-h_a | `h_a` | 66.7% [0.0, 100.0] | 2/3 | not measured (needs 2 or more tries per task; this ran 1) | 3 | 5 | $0.1250 | - | leader |",
				"| 2 | 1 | agent-h_b | beta |",
				"too few common tasks (2) to separate",
				"| model-outage |",
				"- experiment verdict invalid: 1 run (e_bad)",
				"- infra_error: 1 run (e_one)",
				"Common tasks (2): t1, t2.",
				"| False completion (share of runs) | Safe success |",
				"over runs whose outcome and claim are known; a run whose verifier passed counts as 0.",
				"k shown per entry: min(3, the fewest trials per task among its experiments that repeat tasks) unless the query sets k.",
				"Grade source original: every run's outcome and metrics as sealed (e_bad, e_two, e_one).",
				"Generated 2026-11-14T10:00:00Z.",
			},
			notWant: []string{"SYNTHETIC", "(pass^1)", "Grade source latest"},
		},
		{
			name: "reliability states its k",
			lb:   repeated,
			want: []string{"| 1/2 | 62.5% (pass^3) | 2 | 7 |"},
		},
		{
			name: "unknown reliability still states its k",
			lb:   short,
			want: []string{"| 1/2 | unknown (pass^4) |"},
		},
		{
			name: "grade source latest lists each experiment",
			lb:   latest,
			want: []string{
				"Grade source latest: each experiment's newest regrade (by regrade.json created_at, ties by the larger id)",
				"- e_two: original (no regrade), 1 run\n",
				"- e_one: rg_new, 1 of 4 runs regraded\n",
			},
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

// A judged board says so in JSON and Markdown from the sealed grade
// evidence, not today's suite catalog (finding 1-D-09).
func TestBoardGradingProvenance(t *testing.T) {
	f := newFake()
	f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command")})
	judged := f.pass("e", "t1", "A", "clean", 1, 0.1)
	f.files[judged][fileGrade] = []byte(`{"passed":true,"judged":true,"judge":{"model":"judge-m"}}`)
	lb, err := Build(context.Background(), f, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if lb.Grading != GradingJudged || len(lb.Judges) != 1 || lb.Judges[0] != "judge-m" {
		t.Fatalf("grading %q judges %v", lb.Grading, lb.Judges)
	}
	if md := Markdown(lb); !strings.Contains(md, "**JUDGED.**") || !strings.Contains(md, "judge-m") {
		t.Fatalf("markdown has no judged label:\n%s", md)
	}
	f.files[judged][fileGrade] = []byte(`{"passed":true}`)
	if lb, _ = Build(context.Background(), f, Query{}); lb.Grading != GradingVerified || strings.Contains(Markdown(lb), "JUDGED") {
		t.Fatalf("verified board graded %q", lb.Grading)
	}
	delete(f.files[judged], fileGrade)
	if lb, _ = Build(context.Background(), f, Query{}); lb.Grading != GradingUnknown || !strings.Contains(Markdown(lb), "GRADING UNKNOWN") {
		t.Fatalf("board without grade evidence graded %q", lb.Grading)
	}
}

// Audit qc2-05-11: on the common scope the grading label reads only the
// common tasks; a judged task outside them labels nothing.
func TestCommonScopeGradingProvenance(t *testing.T) {
	f := newFake()
	f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command"), "B": arm("b", "h2", "command")})
	for _, a := range []string{"A", "B"} {
		r := f.pass("e", "t1", a, "clean", 1, 0.1)
		f.files[r][fileGrade] = []byte(`{"passed":true}`)
	}
	j := f.pass("e", "t2", "A", "clean", 1, 0.1)
	f.files[j][fileGrade] = []byte(`{"passed":true,"judged":true,"judge":{"model":"judge-m"}}`)
	lb, err := Build(context.Background(), f, Query{Scope: ScopeCommon})
	if err != nil {
		t.Fatal(err)
	}
	if lb.Grading != GradingVerified || len(lb.Judges) != 0 {
		t.Fatalf("common-scope grading %q judges %v, want verified", lb.Grading, lb.Judges)
	}
	if lb, _ = Build(context.Background(), f, Query{}); lb.Grading != GradingMixed {
		t.Fatalf("all-task grading %q, want mixed", lb.Grading)
	}
}
