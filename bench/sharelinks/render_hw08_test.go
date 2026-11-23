package sharelinks

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
)

// Fix wave 3, N182 and N215: the shared report card never prints a score
// without runs, says when every run was thrown out, and says how far a
// running evaluation is.
func TestRenderSharedReportNoScoreWithoutRuns(t *testing.T) {
	base := func(status string) *reportcard.Report {
		return &reportcard.Report{Evaluation: "ev_1", Status: status, Mode: "score",
			Arms: []reportcard.Arm{{Label: "A", Agent: "codex", Model: "gpt-6-luna"}},
			Benchmarks: []reportcard.Benchmark{{Benchmark: "hd-harbor-bfcl", Capability: "support", Arms: map[string]reportcard.Metrics{"A": {}},
				ThrownOut: map[string]reportcard.ThrownOut{"A": {Runs: 24, Checks: map[string]int{"model_fidelity": 24}}}}},
			ThrownOutRuns: 24}
	}
	gen := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	md := ReportDoc{Report: base("done"), Generated: gen}.Markdown()
	if !strings.Contains(md, "No score: every run was thrown out by a safety check") || strings.Contains(md, "0%") {
		t.Fatalf("all thrown out:\n%s", md)
	}
	html := ReportDoc{Report: base("done"), Generated: gen}.HTML()
	if !strings.Contains(html, "No score: every run was thrown out by a safety check") {
		t.Fatalf("html lacks the headline")
	}

	run := base("running")
	run.ThrownOutRuns, run.Benchmarks[0].ThrownOut = 0, nil
	run.Benchmarks[0].Arms["A"] = reportcard.Metrics{Runs: 2, Score: 0.5}
	run.PlannedRuns, run.DoneRuns = 6, 2
	if md := (ReportDoc{Report: run, Generated: gen}).Markdown(); !strings.Contains(md, "Still running: 2 of 6 runs done") {
		t.Fatalf("running:\n%s", md)
	}

	some := base("done")
	some.Benchmarks[0].Arms["A"] = reportcard.Metrics{Runs: 12, Score: 0.75}
	some.ThrownOutRuns = 7
	if md := (ReportDoc{Report: some, Generated: gen}).Markdown(); !strings.Contains(md, "7 runs were thrown out by a safety check and are not in the score.") {
		t.Fatalf("some thrown out:\n%s", md)
	}
}
