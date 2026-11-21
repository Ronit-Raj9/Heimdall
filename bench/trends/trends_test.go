package trends

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/resultsindex"
)

func rates(vals ...float64) map[string]float64 {
	m := map[string]float64{}
	for i, v := range vals {
		m[fmt.Sprintf("t%02d", i+1)] = v
	}
	return m
}

func TestCompare(t *testing.T) {
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	zeros := []float64{0, 0, 0, 0, 0, 0, 0, 0}
	tests := []struct {
		name         string
		older, newer map[string]float64
		status       string
		discordant   int
	}{
		{"eight tasks all down", rates(ones...), rates(zeros...), StatusRegressed, 8},
		{"eight tasks all up", rates(zeros...), rates(ones...), StatusImproved, 8},
		{"five common tasks cannot tell", rates(1, 1, 1, 1, 1), rates(0, 0, 0, 0, 0), StatusCannotTell, 5},
		{"three of eight changed cannot tell", rates(ones...), rates(0, 0, 0, 1, 1, 1, 1, 1), StatusCannotTell, 3},
		{"identical", rates(ones...), rates(ones...), StatusNoChange, 0},
		{"mixed changes", rates(1, 1, 1, 1, 0, 0, 0, 0), rates(0, 0, 0, 0, 1, 1, 1, 1), StatusNoChange, 8},
		{"no common tasks", map[string]float64{"a": 1}, map[string]float64{"b": 0}, StatusCannotTell, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(tt.older, tt.newer)
			if got.Status != tt.status || got.Discordant != tt.discordant {
				t.Fatalf("Compare = %+v, want status %s, %d discordant", got, tt.status, tt.discordant)
			}
			if got.Reason == "" {
				t.Fatal("no reason")
			}
		})
	}
	t.Run("p is the exact sign-flip p", func(t *testing.T) {
		got := Compare(rates(ones...), rates(zeros...))
		if got.P == nil || math.Abs(*got.P-2.0/256) > 1e-12 || *got.Delta != -1 || got.CommonTasks != 8 {
			t.Fatalf("Compare = %+v", got)
		}
	})
}

// fixtureIndex: lineage codex|m1 over three experiments (e_1 config aaa, e_2
// configs aaa and bbb, e_3 invalid), one synthetic toy lineage, and runs that
// do not enter (unscored, other condition).
func fixtureIndex() *resultsindex.Snapshot {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	f := func(v float64) *float64 { return &v }
	var runs []resultsindex.RunRow
	add := func(exp string, created time.Time, arm, hash string, task int, success float64, cost *float64, tokens float64) {
		runs = append(runs, resultsindex.RunRow{Experiment: exp, ExperimentCreated: created, RunID: fmt.Sprintf("r_%s_%s_%d", exp, arm, task),
			Suite: "core@1", Task: fmt.Sprintf("t%02d", task), Arm: arm, Agent: "codex", Model: "m1", ConfigHash: hash,
			Condition: "clean", Trial: 1, Outcome: "pass", Success: f(success), CostUSD: cost, WallTimeS: f(10),
			Metrics: map[string]float64{"tokens": tokens, "wall_time_s": 10, "false_completion": 0}})
	}
	for task := 1; task <= 8; task++ {
		add("e_1", day(1), "A", "sha256:aaa", task, 1, f(0.5), 100)
		add("e_2", day(5), "A", "sha256:aaa", task, 1, f(0.5), 100)
		add("e_2", day(5), "B", "sha256:bbb", task, 0, f(1), 300)
		add("e_3", day(9), "A", "sha256:ccc", task, 0, f(1), 300)
	}
	runs[0].Success = nil // e_1 t01 did not score: left out
	runs = append(runs,
		resultsindex.RunRow{Experiment: "e_1", ExperimentCreated: day(1), RunID: "r_probe", Suite: "core@1", Task: "t01", Arm: "A",
			Agent: "codex", Model: "m1", ConfigHash: "sha256:aaa", Condition: "tool_timeout", Success: f(0), Metrics: map[string]float64{}},
		resultsindex.RunRow{Experiment: "e_9", ExperimentCreated: day(2), RunID: "r_toy", Suite: "core@1", Task: "t01", Arm: "A",
			Agent: "toy", Model: "scripted", ConfigHash: "sha256:toy", Condition: "clean", Success: f(1), Synthetic: true, Metrics: map[string]float64{}},
	)
	return &resultsindex.Snapshot{
		RunRows: runs,
		AnalysisRows: []resultsindex.AnalysisRow{
			{Experiment: "e_2", AnalysisID: "a_old", Decision: "inconclusive", CreatedAt: day(6)},
			{Experiment: "e_2", AnalysisID: "a_new", Decision: "regressed", Policy: "block", CreatedAt: day(7)},
			{Experiment: "e_1", AnalysisID: "a_1", Decision: "improved", Policy: "block", CreatedAt: day(2)},
			{Experiment: "e_3", AnalysisID: "a_3", Decision: "invalid", CreatedAt: day(10)},
		},
		Rejects: []resultsindex.Rejected{{Experiment: "e_2", RunID: "r_x", Reason: "seal does not verify"}},
	}
}

func TestBuild(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	resp, err := Build(fixtureIndex(), Query{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Metric != MetricSuccess || resp.Condition != "clean" || resp.Synthetic != SyntheticExclude || resp.Rejected != 1 {
		t.Fatalf("defaults = %+v", resp)
	}
	if len(resp.Lineages) != 1 || resp.Points != 3 {
		t.Fatalf("lineages = %+v, points %d; want codex only (toy synthetic, e_3 invalid) with 3 points", resp.Lineages, resp.Points)
	}
	if resp.Invalid != 8 { // live test l13: e_3's 8 runs (newest verdict INVALID) are counted, not dropped silently
		t.Fatalf("invalid = %d, want 8 (the runs of e_3)", resp.Invalid)
	}
	if fmt.Sprint(resp.Conditions) != "[clean tool_timeout]" || fmt.Sprint(resp.Agents) != "[codex]" || fmt.Sprint(resp.Suites) != "[core@1]" {
		t.Fatalf("pickers = %v %v %v", resp.Conditions, resp.Agents, resp.Suites)
	}
	l := resp.Lineages[0]
	p0, p1, p2 := l.Points[0], l.Points[1], l.Points[2]
	if p0.Experiment != "e_1" || p1.Experiment != "e_2" || p1.Arm != "A" || p2.Arm != "B" {
		t.Fatalf("order = %s/%s %s/%s %s/%s", p0.Experiment, p0.Arm, p1.Experiment, p1.Arm, p2.Experiment, p2.Arm)
	}
	if p0.Tasks != 7 || p0.Runs != 7 || *p0.Success != 1 || p0.Regression.Status != StatusFirst {
		t.Fatalf("p0 = %+v", p0)
	}
	if p1.ConfigChanged || !p2.ConfigChanged {
		t.Fatalf("config_changed = %v %v, want false true", p1.ConfigChanged, p2.ConfigChanged)
	}
	if p1.Regression.Status != StatusNoChange || p1.Regression.CommonTasks != 7 {
		t.Fatalf("p1 regression = %+v", p1.Regression)
	}
	if p2.Regression.Status != StatusRegressed || p2.Regression.Against != "e_2" || l.Regression.Status != StatusRegressed {
		t.Fatalf("p2 regression = %+v, lineage %+v", p2.Regression, l.Regression)
	}
	if p2.Release != ReleaseRegressed || p2.Decision != "regressed" || p2.Policy != "block" {
		t.Fatalf("p2 release = %q decision %q policy %q", p2.Release, p2.Decision, p2.Policy)
	}
	if p0.Decision != "improved" || p0.Release != ReleaseBlocked {
		t.Fatalf("p0 decision %q release %q, want improved and blocked by the policy", p0.Decision, p0.Release)
	}
	if *p1.CostPerSuccess != 0.5 || p2.CostPerSuccess != nil || *p2.Tokens != 300 || *p1.WallTime != 10 || *p1.FalseCompletion != 0 {
		t.Fatalf("metrics p1 %+v p2 %+v", p1, p2)
	}
	if p1.SuccessLow != nil {
		// Every task rate is 1: the t interval collapses to the point.
		if *p1.SuccessLow != 1 || *p1.SuccessHigh != 1 {
			t.Fatalf("interval = %v %v", *p1.SuccessLow, *p1.SuccessHigh)
		}
	}
	if *p1.Value != *p1.Success {
		t.Fatal("value is not the charted metric")
	}
}

func TestBuildFilters(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		q        Query
		lineages int
		points   int
		check    func(t *testing.T, r *Response)
	}{
		{"synthetic included", Query{Synthetic: SyntheticInclude}, 2, 4, nil},
		{"synthetic only", Query{Synthetic: SyntheticOnly}, 1, 1, func(t *testing.T, r *Response) {
			if !r.Lineages[0].Synthetic || r.Lineages[0].Agent != "toy" {
				t.Fatalf("lineage = %+v", r.Lineages[0])
			}
		}},
		{"agent filter", Query{Agent: "nobody"}, 0, 0, nil},
		{"suite filter", Query{Suite: "core@1"}, 1, 3, nil},
		{"condition filter", Query{Condition: "tool_timeout"}, 1, 1, nil},
		{"tokens metric", Query{Metric: MetricTokens}, 1, 3, func(t *testing.T, r *Response) {
			if v := r.Lineages[0].Points[2].Value; v == nil || *v != 300 {
				t.Fatalf("value = %v, want 300", v)
			}
		}},
		{"cost per success null without success", Query{Metric: MetricCostPerSuccess}, 1, 3, func(t *testing.T, r *Response) {
			if r.Lineages[0].Points[2].Value != nil {
				t.Fatal("cost per success with no success is not null")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Build(fixtureIndex(), tt.q, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Lineages) != tt.lineages || r.Points != tt.points {
				t.Fatalf("lineages %d points %d, want %d %d", len(r.Lineages), r.Points, tt.lineages, tt.points)
			}
			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}
	for _, q := range []Query{{Metric: "score"}, {Synthetic: "maybe"}} {
		if _, err := Build(fixtureIndex(), q, now); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("query %+v: err = %v, want ErrBadQuery", q, err)
		}
	}
}

func TestBuildEmpty(t *testing.T) {
	r, err := Build(&resultsindex.Snapshot{}, Query{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Points != 0 || r.Lineages == nil || r.Suites == nil || r.Agents == nil || r.Conditions == nil {
		t.Fatalf("empty response = %+v; slices must be [] not null", r)
	}
}

func TestBuildLeavesOutNotRepeatable(t *testing.T) {
	snap := fixtureIndex()
	for i := range snap.RunRows {
		if snap.RunRows[i].Experiment == "e_1" {
			snap.RunRows[i].NotRepeatable = true
		}
	}
	r, err := Build(snap, Query{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Points != 2 || r.Lineages[0].Points[0].Experiment != "e_2" {
		t.Fatalf("points %d, first %s: want e_1 (external live) left out", r.Points, r.Lineages[0].Points[0].Experiment)
	}
}

// A held-out task's runs never enter a trend: with every task of e_1 held
// out its point disappears, as the public leaderboard never counts them.
func TestBuildLeavesOutHeldOut(t *testing.T) {
	q := Query{HeldOut: func(expID, task string) bool { return expID == "e_1" }}
	r, err := Build(fixtureIndex(), q, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Points != 2 || r.Lineages[0].Points[0].Experiment != "e_2" {
		t.Fatalf("points %d, first %s: want e_1 (held out) left out", r.Points, r.Lineages[0].Points[0].Experiment)
	}
	one := Query{HeldOut: func(expID, task string) bool { return task == "t02" }}
	r, err = Build(fixtureIndex(), one, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range r.Lineages {
		for _, p := range l.Points {
			if p.Tasks > 7 {
				t.Fatalf("point %s counts %d tasks, want t02 left out", p.Experiment, p.Tasks)
			}
		}
	}
}

// Two suites that reuse task IDs t01..t06 are not paired: a newer point of
// another suite cannot be a regression of an older one (finding 1-D-03).
func TestBuildDoesNotPairOtherSuites(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	var runs []resultsindex.RunRow
	for task := 1; task <= 6; task++ {
		for _, s := range []struct {
			exp, suite string
			day        int
			y          float64
		}{{"e_easy", "easy@1", 1, 1}, {"e_hard", "hard@1", 2, 0}} {
			runs = append(runs, resultsindex.RunRow{Experiment: s.exp, ExperimentCreated: time.Date(2026, 11, s.day, 0, 0, 0, 0, time.UTC),
				RunID: fmt.Sprintf("r_%s_%d", s.exp, task), Suite: s.suite, Task: fmt.Sprintf("t%02d", task), Arm: "A", Agent: "codex",
				Model: "m1", ConfigHash: "sha256:a", Condition: "clean", Success: f(s.y), Metrics: map[string]float64{}})
		}
	}
	r, err := Build(&resultsindex.Snapshot{RunRows: runs}, Query{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Lineages) != 1 || r.Points != 2 {
		t.Fatalf("lineages %+v", r.Lineages)
	}
	if reg := r.Lineages[0].Points[1].Regression; reg.Status != StatusCannotTell || reg.CommonTasks != 0 || reg.P != nil {
		t.Fatalf("regression across suites = %+v, want cannot_tell with no pairs", reg)
	}
}

// A run whose evidence has a problem (a slot with two counted runs) stays in
// the index but is never charted (finding 1-D-04).
func TestBuildLeavesOutProblemRuns(t *testing.T) {
	snap := fixtureIndex()
	for i := range snap.RunRows {
		if snap.RunRows[i].Experiment == "e_1" {
			snap.RunRows[i].Problems = []string{"slot s has 2 counted runs (a, b); exactly one may count"}
		}
	}
	r, err := Build(snap, Query{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Points != 2 || r.Lineages[0].Points[0].Experiment != "e_2" || r.Invalid != 9+8 { // e_1 problem runs plus e_3 (verdict INVALID)
		t.Fatalf("points %d invalid %d: want e_1 left out and counted invalid", r.Points, r.Invalid)
	}
}

// TestConfigChangedReadsComparisonReference is the regression test for audit
// qc2-05-06: points A (suite S1, config x), B (S2, y), C (S1, y). C compares
// against A, so its config label says changed (x to y), not unchanged (B to C).
func TestConfigChangedReadsComparisonReference(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	one := 1.0
	var runs []resultsindex.RunRow
	add := func(exp string, d int, suite, hash string) {
		for task := 1; task <= 4; task++ {
			runs = append(runs, resultsindex.RunRow{Experiment: exp, ExperimentCreated: day(d), RunID: fmt.Sprintf("r_%s_%d", exp, task),
				Suite: suite, Task: fmt.Sprintf("t%02d", task), Arm: "A", Agent: "codex", Model: "m1", ConfigHash: hash,
				Condition: "clean", Trial: 1, Outcome: "pass", Success: &one, Metrics: map[string]float64{}})
		}
	}
	add("e_a", 1, "s1@1", "sha256:x")
	add("e_b", 2, "s2@1", "sha256:y")
	add("e_c", 3, "s1@1", "sha256:y")
	resp, err := Build(&resultsindex.Snapshot{RunRows: runs}, Query{}, day(10))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Lineages) != 1 || len(resp.Lineages[0].Points) != 3 {
		t.Fatalf("lineages %+v", resp.Lineages)
	}
	c := resp.Lineages[0].Points[2]
	if c.Regression.Against != "e_a" || !c.ConfigChanged {
		t.Fatalf("point C against %q config_changed %v, want e_a and true", c.Regression.Against, c.ConfigChanged)
	}
	b := resp.Lineages[0].Points[1]
	if b.ConfigChanged {
		t.Fatal("point B has no reference, so its config label must be false")
	}
}

// split=held_out charts only the held-out tasks' runs, and only when the
// server config makes them visible (RESEARCH_R2 rec 3).
func TestBuildHeldOutSplit(t *testing.T) {
	held := func(expID, task string) bool { return expID == "e_1" }
	if _, err := Build(fixtureIndex(), Query{HeldOut: held, Split: "held_out"}, time.Now()); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("hidden held-out split: err %v, want ErrBadQuery", err)
	}
	if _, err := Build(fixtureIndex(), Query{Split: "nope"}, time.Now()); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("unknown split: err %v", err)
	}
	r, err := Build(fixtureIndex(), Query{HeldOut: held, Split: "held_out", HeldOutVisible: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Split != "held_out" || r.Points == 0 {
		t.Fatalf("split %q points %d", r.Split, r.Points)
	}
	for _, l := range r.Lineages {
		for _, p := range l.Points {
			if p.Experiment != "e_1" {
				t.Fatalf("held-out series has %s", p.Experiment)
			}
		}
	}
	pub, err := Build(fixtureIndex(), Query{HeldOut: held, Split: "public", HeldOutVisible: true}, time.Now())
	if err != nil || pub.Split != "" {
		t.Fatalf("public: %v %q", err, pub.Split)
	}
}
