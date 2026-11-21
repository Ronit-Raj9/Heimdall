package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

func TestLatestDecision(t *testing.T) {
	root := t.TempDir()
	write := func(exp, analysis, body string) {
		t.Helper()
		dir := filepath.Join(root, exp, "analysis", analysis)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "verdict.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("e_invalid", "a_1", `{"analysis_id":"a_1","verdict":{"decision":"invalid"}}`)
	write("e_corrupt", "a_1", `{not json`)
	write("e_empty", "a_1", `{"verdict":{}}`)
	// Audit 1-D-07: an interrupted write left a newer folder with no
	// verdict.json, and a copied archive gave an older analysis the newest
	// folder time. The newest complete record by created_at still decides.
	write("e_interrupted", "a_1", `{"analysis_id":"a_1","created_at":"2026-11-14T10:00:00Z","verdict":{"decision":"invalid"}}`)
	if err := os.MkdirAll(filepath.Join(root, "e_interrupted", "analysis", "a_2"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("e_copied", "a_new", `{"analysis_id":"a_new","created_at":"2026-11-14T10:00:00Z","verdict":{"decision":"invalid"}}`)
	write("e_copied", "a_old", `{"analysis_id":"a_old","created_at":"2026-11-13T10:00:00Z","verdict":{"decision":"improved"}}`)
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "e_copied", "analysis", "a_new"), past, past); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		exp    string
		want   schemas.Decision
		wantOK bool
	}{
		{exp: "e_invalid", want: schemas.DecisionInvalid, wantOK: true},
		// Unreadable analysis state fails closed (audit 1-D-07).
		{exp: "e_corrupt", want: schemas.DecisionInvalid, wantOK: true},
		{exp: "e_interrupted", want: schemas.DecisionInvalid, wantOK: true},
		{exp: "e_copied", want: schemas.DecisionInvalid, wantOK: true},
		{exp: "e_empty", wantOK: false},
		{exp: "e_none", wantOK: false},
	}
	x := indexAdapter{root: root}
	for _, tt := range tests {
		t.Run(tt.exp, func(t *testing.T) {
			got, ok := x.LatestDecision(tt.exp)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("LatestDecision = %q %v, want %q %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestLeaderboardUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "positional argument", args: []string{"extra"}},
		{name: "unknown format", args: []string{"-format", "xml"}},
		{name: "unknown flag", args: []string{"-alpha", "0.1"}},
		{name: "grade source is not original or latest", args: []string{"-grade-source", "rg_gates_1_1_0"}},
		{name: "k of 1", args: []string{"-k", "1"}},
		{name: "k not an integer", args: []string{"-k", "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runLeaderboard(tt.args); got != exitUsage {
				t.Errorf("exit = %d, want %d", got, exitUsage)
			}
		})
	}
}

// regradeStore builds a run store in a temp dir: experiment e_rg, where arm
// A (sha256:aaa) ran t1 and t2 once each, sealed, and a gate vetoed t1. Its
// regrades: rg_b (older) fails t1 again, rg_a and rg_c share the newest
// created_at and only rg_c passes t1, so the tie must go to the larger id.
// rg_partial has no regrade.json and a stray file sits beside the folders.
func regradeStore(t *testing.T) (*store.FSStore, map[string]time.Time) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(t, root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{ExpID: "e_rg", SuiteID: "s", SuiteVersion: "1", CreatedAt: time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC),
		Arms: map[string]schemas.ArmManifest{"A": {Label: "a", ConfigHash: "sha256:aaa", Config: schemas.AgentConfig{ID: "codex", Adapter: "command"}}},
		Slots: []schemas.Slot{
			{SlotID: "s_t1", PairID: "p_t1", Task: "t1", Arm: "A", Condition: "clean", Trial: 1, Counted: true},
			{SlotID: "s_t2", PairID: "p_t2", Task: "t2", Arm: "A", Condition: "clean", Trial: 1, OrderIndex: 1, Counted: true},
		}}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	seal := func(slot, runID string, o schemas.Outcome, y float64) {
		sl := m.Slots[slices.IndexFunc(m.Slots, func(s schemas.Slot) bool { return s.SlotID == slot })]
		sl.ExpID = m.ExpID
		r := &schemas.Run{RunID: runID, Attempt: 1, Stage: schemas.StageCleanup, Outcome: o}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		r.ExpID, r.SlotID, r.PairID = m.ExpID, sl.SlotID, sl.PairID
		metrics := fmt.Sprintf(`{"verified_success":{"state":"observed","v":%v,"evidence":"outcome"}}`, y)
		if err := st.WriteRunFile(ctx, r, "metrics.json", []byte(metrics)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	seal("s_t1", "r_t1", schemas.OutcomeIntegrityViolation, 0)
	seal("s_t2", "r_t2", schemas.OutcomePass, 1)

	newest := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	created := map[string]time.Time{"rg_a": newest, "rg_b": newest.Add(-time.Hour), "rg_c": newest}
	outcomes := map[string]schemas.Outcome{"rg_a": schemas.OutcomeTimeout, "rg_b": schemas.OutcomeFail, "rg_c": schemas.OutcomePass}
	versions := map[string]string{"g1_protected_paths": "1.1.0"}
	for id, at := range created {
		y := 0.0
		if outcomes[id] == schemas.OutcomePass {
			y = 1
		}
		r := &store.GateRegrade{SealedOutcome: schemas.OutcomeIntegrityViolation, Outcome: outcomes[id], Y: schemas.Observed(y, schemas.EvidenceOutcome),
			Metrics: map[string]schemas.Value{"verified_success": schemas.Observed(y, schemas.EvidenceOutcome)}}
		if err := st.WriteGateRegrades(ctx, "e_rg", id, store.RegradeInfo{SuiteVersion: "1", GateVersions: versions}, map[string]*store.GateRegrade{"r_t1": r}); err != nil {
			t.Fatal(err)
		}
		// Pin created_at: rewrite the committed regrade.json.
		dir := filepath.Join(root, "e_rg", "regrades", id)
		info, err := json.Marshal(store.RegradeInfo{RegradeID: id, ExpID: "e_rg", SuiteVersion: "1", GateVersions: versions, CreatedAt: at, Runs: []string{"r_t1"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "regrade.json"), info, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "e_rg", "regrades", "rg_partial"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "e_rg", "regrades", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return st, created
}

func TestIndexAdapterRegrades(t *testing.T) {
	ctx := context.Background()
	st, created := regradeStore(t)
	x := indexAdapter{root: st.Root()}
	list, err := x.Regrades(ctx, "e_rg")
	if err != nil {
		t.Fatalf("Regrades: %v", err)
	}
	got := map[string]time.Time{}
	for _, r := range list {
		got[r.ID] = r.CreatedAt.UTC()
	}
	if !reflect.DeepEqual(got, created) {
		t.Errorf("regrades = %v, want %v (rg_partial has no regrade.json)", got, created)
	}
	if list, err := x.Regrades(ctx, "e_none"); err != nil || len(list) != 0 {
		t.Errorf("experiment without regrades = %v, %v", list, err)
	}
	runs, err := x.RegradedRuns(ctx, "e_rg", "rg_c")
	if err != nil || len(runs) != 2 || runs[0].GradeSource != "rg_c" || runs[0].Run.Outcome != schemas.OutcomePass || runs[1].GradeSource != "original" {
		t.Errorf("regraded runs = %+v, %v", runs, err)
	}
	if _, err := (indexAdapter{root: filepath.Join(st.Root(), "missing")}).RegradedRuns(ctx, "e_rg", "rg_c"); err == nil {
		t.Error("a missing store root opened")
	}
	if _, err := os.Stat(filepath.Join(st.Root(), "missing")); err == nil {
		t.Error("reading created the missing store root")
	}
}

func TestLeaderboardGradeSourceOnDisk(t *testing.T) {
	ctx := context.Background()
	st, _ := regradeStore(t)
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	src := handlers.LeaderboardSource(indexAdapter{ix: ix, root: st.Root()})
	before := treeState(t, st.Root())
	tests := []struct {
		source      string
		wantSuccess float64
		wantSources []schemas.LeaderboardGradeSource
	}{
		{source: leaderboard.GradeSourceOriginal, wantSuccess: 0.5,
			wantSources: []schemas.LeaderboardGradeSource{{Experiment: "e_rg", Source: "original", Runs: 2}}},
		{source: leaderboard.GradeSourceLatest, wantSuccess: 1,
			wantSources: []schemas.LeaderboardGradeSource{{Experiment: "e_rg", Source: "rg_c", Runs: 2, RegradedRuns: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			lb, err := leaderboard.Build(ctx, src, leaderboard.Query{GradeSource: tt.source})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if !reflect.DeepEqual(lb.GradeSources, tt.wantSources) {
				t.Errorf("grade sources = %+v, want %+v", lb.GradeSources, tt.wantSources)
			}
			if len(lb.Entries) != 1 {
				t.Fatalf("entries = %+v", lb.Entries)
			}
			if v, _ := lb.Entries[0].Success.Float(); v != tt.wantSuccess {
				t.Errorf("success = %v, want %v", v, tt.wantSuccess)
			}
		})
	}
	if after := treeState(t, st.Root()); !reflect.DeepEqual(before, after) {
		t.Errorf("the leaderboard wrote to the run store:\nbefore %v\nafter  %v", before, after)
	}
}

// treeState maps every path under root to its size, mode and modification
// time, so a test can tell that nothing was written.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%d %v %d", info.Size(), info.Mode(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// makeWritable undoes the read-only modes of sealed folders so the temp dir
// can be removed.
func makeWritable(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o644)
	})
	if err != nil {
		t.Logf("make writable %s: %v", root, err)
	}
}

func TestLeaderboardHints(t *testing.T) {
	empty := &schemas.Leaderboard{}
	two := &schemas.Leaderboard{Suite: "pyapp@0.1.0", Suites: []string{"pyapp@0.1.0", "webapp@0.1.0"},
		Entries: []schemas.LeaderboardEntry{{Rank: 1}}}
	synth := &schemas.Leaderboard{Suites: []string{"pyapp@0.1.0", "webapp@0.1.0"}}
	tests := []struct {
		name      string
		suiteFlag string
		lb, synth *schemas.Leaderboard
		want      []string
	}{
		{"nothing to say", "", &schemas.Leaderboard{Suite: "a@1", Suites: []string{"a@1"}, Entries: []schemas.LeaderboardEntry{{Rank: 1}}}, nil, nil},
		{"several suites", "", two, nil, []string{"suites with runs: pyapp@0.1.0, webapp@0.1.0; this board is pyapp@0.1.0, pick another with -suite <id@version>"}},
		{"suite named", "webapp@0.1.0", two, nil, nil},
		{"only scripted runs", "", empty, synth, []string{"no live runs to rank; scripted runs (control agents or the scripted model) exist for pyapp@0.1.0, webapp@0.1.0: add -synthetic only to see them"}},
		{"no runs at all", "", empty, &schemas.Leaderboard{}, nil},
		// live test l13: live runs that were all excluded are named, not
		// reported as missing.
		{"live runs excluded", "tb2@1.0.0", &schemas.Leaderboard{Suite: "tb2@1.0.0",
			Excluded: []schemas.LeaderboardExclusion{{Reason: "experiment verdict invalid", Runs: 4, Experiments: []string{"e_tb2"}}}}, synth,
			[]string{"no live runs ranked on tb2@1.0.0: every live run was excluded; experiment verdict invalid: 4 runs (e_tb2)",
				"scripted runs (control agents or the scripted model) also exist for pyapp@0.1.0, webapp@0.1.0: add -synthetic only to see them"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leaderboardHints(tt.suiteFlag, tt.lb, tt.synth); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}
