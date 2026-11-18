package db_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// runStore is a run store with experiments of two arms over two tasks.
type runStore struct {
	t  *testing.T
	st *store.FSStore
	ms map[string]*schemas.Manifest
}

func newRunStore(t *testing.T) *runStore {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &runStore{t: t, st: st, ms: map[string]*schemas.Manifest{}}
}

func (r *runStore) experiment(expID, name string, created time.Time, smoke bool) {
	r.t.Helper()
	m := &schemas.Manifest{
		ExpID: expID, CreatedAt: created, SuiteID: "coding_core", SuiteVersion: "1.0.0", Hash: "sha256:" + expID,
		Experiment: schemas.Experiment{Name: name, Smoke: smoke},
		Arms: map[string]schemas.ArmManifest{
			"A": {Label: "base", ConfigHash: "sha256:aaa", Config: schemas.AgentConfig{ID: "codex", Model: "m1", Adapter: "command"}},
			"B": {Label: "cand", ConfigHash: "sha256:bbb", Config: schemas.AgentConfig{ID: "claude", Model: "m2", Adapter: "command"}},
		},
	}
	for _, task := range []string{"t1", "t2"} {
		for _, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + strings.ToLower(arm), PairID: "p_" + task,
				Task: task, Arm: arm, Condition: "clean", Trial: 1, Counted: true})
		}
	}
	if err := r.st.CreateExperiment(context.Background(), m); err != nil {
		r.t.Fatal(err)
	}
	r.ms[expID] = m
}

func (r *runStore) seal(expID, slotID string, outcome schemas.Outcome, cost float64) *schemas.Run {
	r.t.Helper()
	ctx := context.Background()
	m := r.ms[expID]
	var slot schemas.Slot
	for _, s := range m.Slots {
		if s.SlotID == slotID {
			slot = s
		}
	}
	slot.ExpID = expID
	run := &schemas.Run{RunID: "r_" + expID + "_" + strings.TrimPrefix(slotID, "s_"), SlotID: slotID, PairID: slot.PairID, ExpID: expID,
		Attempt: 1, Stage: schemas.StageCleanup, ManifestHash: m.Hash, Outcome: outcome,
		Timing: schemas.RunTiming{Started: m.CreatedAt.Add(time.Hour + time.Duration(len(slotID))*time.Minute)},
		Agent:  schemas.RunAgentInfo{ConfigID: m.Arms[slot.Arm].Config.ID, ConfigHash: m.Arms[slot.Arm].ConfigHash}}
	if y, ok := outcome.Score(); ok {
		run.Y = schemas.Observed(y, schemas.EvidenceOutcome)
	}
	if _, err := r.st.NewRun(ctx, &slot, run); err != nil {
		r.t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]schemas.Value{
		"cost_usd":    schemas.Observed(cost, schemas.EvidenceGateway),
		"wall_time_s": schemas.Observed(42, schemas.EvidenceOutcome),
		"tokens":      schemas.Observed(1200, schemas.EvidenceGateway),
	})
	if err := r.st.WriteRunFile(ctx, run, "metrics.json", b); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.st.Seal(ctx, run); err != nil {
		r.t.Fatal(err)
	}
	return run
}

func (r *runStore) analysis(expID, id string, dec schemas.Decision, at time.Time) {
	r.t.Helper()
	a := &schemas.Analysis{AnalysisID: id, ExpID: expID, CreatedAt: at, GradeSource: "original",
		Verdict: schemas.Verdict{Decision: dec, Action: schemas.ActionNoDecision}}
	if err := r.st.WriteAnalysis(context.Background(), a, nil); err != nil {
		r.t.Fatal(err)
	}
}

func (r *runStore) tamper(runID string) {
	r.t.Helper()
	matches, _ := filepath.Glob(filepath.Join(r.st.Root(), "*", "slots", "*", "runs", runID, "metrics.json"))
	if len(matches) != 1 {
		r.t.Fatalf("metrics of %s: %v", runID, matches)
	}
	_ = os.Chmod(matches[0], 0o644)
	if err := os.WriteFile(matches[0], []byte(`{"cost_usd":{"state":"observed","v":0}}`), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func makeWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o644)
	})
}

// dump is everything the org's index holds, minus the write times.
type dump struct {
	Snap *resultsindex.Snapshot
	Exps []db.IndexedExperiment
	Runs []db.IndexedRun
}

func dumpIndex(t *testing.T, d *db.DB, org string, st *store.FSStore) dump {
	t.Helper()
	ctx := context.Background()
	s, err := d.OrgIndex(org, st, nil).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.BuiltAt, s.UpdatedAt = time.Time{}, time.Time{}
	ep, err := d.ListIndexedExperiments(ctx, db.ExperimentQuery{OrgID: org, Sort: "created_asc"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range ep.Experiments {
		ep.Experiments[i].IndexedAt = time.Time{}
	}
	rp, err := d.ListIndexedRuns(ctx, db.RunQuery{OrgID: org, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	return dump{Snap: s, Exps: ep.Experiments, Runs: rp.Runs}
}

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func TestIndexSyncEqualsRebuild(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		rs := newRunStore(t)
		ix := d.OrgIndex("org_a", rs.st, nil)
		if st, err := ix.Sync(ctx); err != nil || !st.Exists || st.Experiments != 0 {
			t.Fatalf("empty sync %+v %v", st, err)
		}
		rs.experiment("e_one", "First", t0, false)
		rs.experiment("e_two", "Second smoke", t0.Add(24*time.Hour), true)
		steps := []struct {
			exp, slot string
			out       schemas.Outcome
		}{{"e_one", "s_t1_a", schemas.OutcomePass}, {"e_one", "s_t1_b", schemas.OutcomeFail}, {"e_two", "s_t1_a", schemas.OutcomePass},
			{"e_one", "s_t2_a", schemas.OutcomePass}, {"e_one", "s_t2_b", schemas.OutcomeTimeout}}
		for i, s := range steps {
			rs.seal(s.exp, s.slot, s.out, float64(i+1)/10)
			if i%2 == 0 {
				if _, err := ix.Sync(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := ix.UpdateExperiment(ctx, s.exp); err != nil {
				t.Fatal(err)
			}
		}
		rs.analysis("e_one", "a_1", schemas.DecisionRegressed, t0.Add(5*time.Hour))
		rs.analysis("e_one", "a_2", schemas.DecisionImproved, t0.Add(6*time.Hour))
		st, err := ix.Sync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if st.Experiments != 2 || st.Runs != 5 || st.Analyses != 2 || st.Rejected != 0 || st.Format != d.Driver() {
			t.Fatalf("status %+v", st)
		}
		incremental := dumpIndex(t, d, "org_a", rs.st)

		if _, err := ix.Rebuild(ctx); err != nil {
			t.Fatal(err)
		}
		rebuilt := dumpIndex(t, d, "org_a", rs.st)
		if !reflect.DeepEqual(incremental, rebuilt) {
			t.Fatalf("incremental and rebuilt differ:\n%+v\n%+v", incremental, rebuilt)
		}

		// The database holds the rows the JSONL index derives.
		jsonl := resultsindex.New(rs.st, nil)
		if _, err := jsonl.Rebuild(ctx); err != nil {
			t.Fatal(err)
		}
		js, _ := jsonl.Snapshot()
		if !reflect.DeepEqual(js.RunRows, rebuilt.Snap.RunRows) || !reflect.DeepEqual(js.AnalysisRows, rebuilt.Snap.AnalysisRows) {
			t.Fatalf("database rows differ from JSONL rows:\n%+v\n%+v", js.RunRows, rebuilt.Snap.RunRows)
		}

		one := rebuilt.Exps[0]
		if one.ExpID != "e_one" || one.Name != "First" || one.Suite != "coding_core@1.0.0" || !reflect.DeepEqual(one.Arms, []string{"A", "B"}) ||
			one.State != db.IndexStateDone || one.Decision != "improved" || one.AnalysisID != "a_2" || one.Slots != 4 || one.Runs != 4 ||
			one.CostUSD == nil || *one.CostUSD < 1.19 || *one.CostUSD > 1.21 || one.FinishedAt == nil || !one.CreatedAt.Equal(t0) ||
			one.ManifestHash != "sha256:e_one" {
			t.Fatalf("e_one row %+v", one)
		}
		if two := rebuilt.Exps[1]; two.State != db.IndexStatePartial || two.Runs != 1 || two.FinishedAt != nil {
			t.Fatalf("e_two row %+v", two)
		}
		for _, r := range rebuilt.Runs {
			if r.SlotID == "" || r.PairID == "" || r.Tokens == nil || *r.Tokens != 1200 {
				t.Fatalf("run %+v", r)
			}
			if r.Experiment == "e_two" && !r.Synthetic {
				t.Fatal("smoke run not synthetic")
			}
		}

		// Unchanged folders: a sync writes nothing.
		before := dumpIndex(t, d, "org_a", rs.st)
		if _, err := ix.Sync(ctx); err != nil {
			t.Fatal(err)
		}
		if after := dumpIndex(t, d, "org_a", rs.st); !reflect.DeepEqual(before, after) {
			t.Fatal("idle sync changed rows")
		}

		// A removed experiment is dropped.
		makeWritable(filepath.Join(rs.st.Root(), "e_two"))
		if err := os.RemoveAll(filepath.Join(rs.st.Root(), "e_two")); err != nil {
			t.Fatal(err)
		}
		if st, err := ix.Sync(ctx); err != nil || st.Experiments != 1 || st.Runs != 4 {
			t.Fatalf("after removal %+v %v", st, err)
		}
	})
}

func TestIndexTamperRejected(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		rs := newRunStore(t)
		rs.experiment("e_t", "Tamper", t0, false)
		rs.seal("e_t", "s_t1_a", schemas.OutcomePass, 0.1)
		bad := rs.seal("e_t", "s_t1_b", schemas.OutcomePass, 0.2)
		if _, err := d.SyncOrg(ctx, "org_a", rs.st); err != nil {
			t.Fatal(err)
		}
		rs.tamper(bad.RunID)
		st, err := d.RebuildOrg(ctx, "org_a", rs.st)
		if err != nil {
			t.Fatal(err)
		}
		if st.Runs != 1 || st.Rejected != 1 {
			t.Fatalf("status %+v, want 1 run 1 rejected", st)
		}
		rej, err := d.IndexedRejected(ctx, "org_a", "e_t")
		if err != nil || len(rej) != 1 || rej[0].RunID != bad.RunID || !strings.Contains(rej[0].Reason, "seal") {
			t.Fatalf("rejected %+v %v", rej, err)
		}
		rp, _ := d.ListIndexedRuns(ctx, db.RunQuery{OrgID: "org_a"})
		for _, r := range rp.Runs {
			if r.RunID == bad.RunID {
				t.Fatal("tampered run indexed")
			}
		}
		e, err := d.IndexedExperimentByID(ctx, "org_a", "e_t")
		if err != nil || e.Rejected != 1 || e.Runs != 1 || *e.CostUSD != 0.1 {
			t.Fatalf("experiment %+v %v", e, err)
		}
		// Tampered before the first index: never indexed either.
		rs2 := newRunStore(t)
		rs2.experiment("e_t2", "Tamper2", t0, false)
		r2 := rs2.seal("e_t2", "s_t1_a", schemas.OutcomePass, 0.1)
		rs2.tamper(r2.RunID)
		if st, err := d.SyncOrg(ctx, "org_b", rs2.st); err != nil || st.Runs != 0 || st.Rejected != 1 {
			t.Fatalf("org_b %+v %v", st, err)
		}
		// A broken manifest is a rejected experiment.
		rs3 := newRunStore(t)
		if err := os.MkdirAll(filepath.Join(rs3.st.Root(), "e_broken"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rs3.st.Root(), "e_broken", "manifest.json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := d.SyncOrg(ctx, "org_c", rs3.st); err != nil {
			t.Fatal(err)
		}
		if ids, _ := rs3.st.ListExperiments(ctx); len(ids) == 1 {
			e, err := d.IndexedExperimentByID(ctx, "org_c", "e_broken")
			if err != nil || e.State != db.IndexStateRejected {
				t.Fatalf("broken manifest %+v %v", e, err)
			}
		}
	})
}

func TestIndexQueries(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		a, b := newRunStore(t), newRunStore(t)
		a.experiment("e_alpha", "Alpha routing", t0, false)
		a.experiment("e_beta", "Beta prompts", t0.Add(time.Hour), false)
		a.experiment("e_gamma", "Gamma 100% cache_hit", t0.Add(2*time.Hour), true)
		a.seal("e_alpha", "s_t1_a", schemas.OutcomePass, 0.5)
		a.seal("e_alpha", "s_t1_b", schemas.OutcomeFail, 0.25)
		a.seal("e_beta", "s_t1_a", schemas.OutcomePass, 2)
		a.seal("e_gamma", "s_t2_b", schemas.OutcomePass, 0.1)
		a.analysis("e_alpha", "a_1", schemas.DecisionRegressed, t0.Add(3*time.Hour))
		b.experiment("e_other", "Other org", t0, false)
		b.seal("e_other", "s_t1_a", schemas.OutcomePass, 9)
		if _, err := d.SyncOrg(ctx, "org_a", a.st); err != nil {
			t.Fatal(err)
		}
		if _, err := d.SyncOrg(ctx, "org_b", b.st); err != nil {
			t.Fatal(err)
		}
		ids := func(p db.ExperimentPage) string {
			var s []string
			for _, e := range p.Experiments {
				s = append(s, e.ExpID)
			}
			return strings.Join(s, ",")
		}
		cases := []struct {
			q     db.ExperimentQuery
			want  string
			total int
		}{
			{db.ExperimentQuery{OrgID: "org_a"}, "e_gamma,e_beta,e_alpha", 3},
			{db.ExperimentQuery{OrgID: "org_a", Sort: "created_asc", Limit: 2}, "e_alpha,e_beta", 3},
			{db.ExperimentQuery{OrgID: "org_a", Sort: "created_asc", Limit: 2, Offset: 2}, "e_gamma", 3},
			{db.ExperimentQuery{OrgID: "org_a", Sort: "cost"}, "e_beta,e_alpha,e_gamma", 3},
			{db.ExperimentQuery{OrgID: "org_a", Sort: "name"}, "e_alpha,e_beta,e_gamma", 3},
			{db.ExperimentQuery{OrgID: "org_a", Text: "ROUTING"}, "e_alpha", 1},
			{db.ExperimentQuery{OrgID: "org_a", Text: "100%"}, "e_gamma", 1},
			{db.ExperimentQuery{OrgID: "org_a", Text: "e_b"}, "e_beta", 1},
			{db.ExperimentQuery{OrgID: "org_a", Text: "_"}, "e_gamma,e_beta,e_alpha", 3},
			{db.ExperimentQuery{OrgID: "org_a", State: db.IndexStatePartial, Suite: "coding_core"}, "e_gamma,e_beta,e_alpha", 3},
			{db.ExperimentQuery{OrgID: "org_a", Suite: "coding_core@2.0.0"}, "", 0},
			{db.ExperimentQuery{OrgID: "org_a", State: db.IndexStateDone}, "", 0},
			{db.ExperimentQuery{OrgID: "org_b"}, "e_other", 1},
			{db.ExperimentQuery{OrgID: "org_missing"}, "", 0},
		}
		for _, c := range cases {
			p, err := d.ListIndexedExperiments(ctx, c.q)
			if err != nil {
				t.Fatalf("%+v: %v", c.q, err)
			}
			if got := ids(p); got != c.want || p.Total != c.total {
				t.Errorf("%+v: got %q total %d, want %q total %d", c.q, got, p.Total, c.want, c.total)
			}
		}
		if _, err := d.ListIndexedExperiments(ctx, db.ExperimentQuery{OrgID: "org_a", Sort: "bogus"}); err == nil {
			t.Fatal("bad sort accepted")
		}
		p, _ := d.ListIndexedExperiments(ctx, db.ExperimentQuery{OrgID: "org_a", Text: "alpha"})
		if e := p.Experiments[0]; e.Decision != "regressed" || e.Action != string(schemas.ActionNoDecision) || *e.CostUSD != 0.75 {
			t.Fatalf("alpha row %+v", e)
		}

		runCases := []struct {
			q     db.RunQuery
			total int
		}{
			{db.RunQuery{OrgID: "org_a"}, 4},
			{db.RunQuery{OrgID: "org_a", Experiment: "e_alpha"}, 2},
			{db.RunQuery{OrgID: "org_a", Task: "t2"}, 1},
			{db.RunQuery{OrgID: "org_a", Arm: "B"}, 2},
			{db.RunQuery{OrgID: "org_a", Condition: "clean", Outcome: "fail"}, 1},
			{db.RunQuery{OrgID: "org_a", Agent: "claude"}, 2},
			{db.RunQuery{OrgID: "org_a", Synthetic: resultsindex.SyntheticOnly}, 1},
			{db.RunQuery{OrgID: "org_a", Synthetic: resultsindex.SyntheticExclude}, 3},
			{db.RunQuery{OrgID: "org_b"}, 1},
		}
		for _, c := range runCases {
			p, err := d.ListIndexedRuns(ctx, c.q)
			if err != nil {
				t.Fatalf("%+v: %v", c.q, err)
			}
			if p.Total != c.total || len(p.Runs) != c.total {
				t.Errorf("%+v: total %d rows %d, want %d", c.q, p.Total, len(p.Runs), c.total)
			}
			for _, r := range p.Runs {
				if c.q.OrgID == "org_a" && r.Experiment == "e_other" {
					t.Fatal("org_b run in org_a list")
				}
			}
		}
		p1, _ := d.ListIndexedRuns(ctx, db.RunQuery{OrgID: "org_a", Limit: 3})
		p2, _ := d.ListIndexedRuns(ctx, db.RunQuery{OrgID: "org_a", Limit: 3, Offset: 3})
		if len(p1.Runs) != 3 || len(p2.Runs) != 1 || p1.Runs[0].Started.Before(p1.Runs[1].Started) {
			t.Fatalf("run paging %d %d", len(p1.Runs), len(p2.Runs))
		}
		if _, err := d.ListIndexedRuns(ctx, db.RunQuery{OrgID: "org_a", Synthetic: "maybe"}); err == nil {
			t.Fatal("bad synthetic accepted")
		}
		an, err := d.IndexedAnalyses(ctx, "org_a", "e_alpha")
		if err != nil || len(an) != 1 || an[0].AnalysisID != "a_1" {
			t.Fatalf("analyses %+v %v", an, err)
		}
		if an, _ := d.IndexedAnalyses(ctx, "org_b", ""); len(an) != 0 {
			t.Fatal("org_a analysis visible in org_b")
		}

		// The database index serves the Reader trends use.
		snap, err := d.OrgIndex("org_a", a.st, nil).Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var r resultsindex.Reader = snap
		if got := r.Runs(resultsindex.Filter{Arm: "A"}); len(got) != 2 {
			t.Fatalf("reader runs %d", len(got))
		}
		if st := r.Status(); st.Format != d.Driver() || !st.Exists || st.Runs != 4 {
			t.Fatalf("reader status %+v", st)
		}
	})
}
