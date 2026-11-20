package resultsindex

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

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// fixture is a run store with one experiment of two arms over two tasks.
type fixture struct {
	t  *testing.T
	st *store.FSStore
	m  *schemas.Manifest
}

func newFixture(t *testing.T, expID string, smoke bool) *fixture {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{
		ExpID: expID, CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), SuiteID: "coding_core", SuiteVersion: "1.0.0",
		Hash:       "sha256:manifest",
		Experiment: schemas.Experiment{Smoke: smoke},
		Arms: map[string]schemas.ArmManifest{
			"A": {Label: "base", ConfigHash: "sha256:aaa", Config: schemas.AgentConfig{ID: "codex", Model: "m1", Adapter: "command"}},
			"B": {Label: "cand", ConfigHash: "sha256:bbb", Config: schemas.AgentConfig{ID: "codex", Model: "m1", Adapter: "command"}},
		},
	}
	for _, task := range []string{"t1", "t2"} {
		for _, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + strings.ToLower(arm), PairID: "p_" + task,
				Task: task, Arm: arm, Condition: "clean", Trial: 1, Counted: true})
		}
	}
	if err := st.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, st: st, m: m}
}

// seal creates and seals the run of a slot with an outcome and a cost.
func (f *fixture) seal(slotID string, outcome schemas.Outcome, cost float64) *schemas.Run {
	f.t.Helper()
	ctx := context.Background()
	var slot schemas.Slot
	for _, s := range f.m.Slots {
		if s.SlotID == slotID {
			slot = s
		}
	}
	slot.ExpID = f.m.ExpID
	run := &schemas.Run{RunID: "r_" + strings.TrimPrefix(slotID, "s_"), SlotID: slotID, PairID: slot.PairID, ExpID: f.m.ExpID,
		Attempt: 1, Stage: schemas.StageCleanup, ManifestHash: f.m.Hash, Outcome: outcome,
		Timing: schemas.RunTiming{Started: time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC)},
		Agent:  schemas.RunAgentInfo{ConfigID: "codex", ConfigHash: f.m.Arms[slot.Arm].ConfigHash}}
	if y, ok := outcome.Score(); ok {
		run.Y = schemas.Observed(y, schemas.EvidenceOutcome)
	}
	if _, err := f.st.NewRun(ctx, &slot, run); err != nil {
		f.t.Fatal(err)
	}
	metrics := map[string]schemas.Value{
		"cost_usd":    schemas.Observed(cost, schemas.EvidenceGateway),
		"wall_time_s": schemas.Observed(42, schemas.EvidenceOutcome),
		"tokens":      schemas.Unknown("no usage", schemas.EvidenceGateway),
	}
	b, err := json.Marshal(metrics)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.WriteRunFile(ctx, run, "metrics.json", b); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.Seal(ctx, run); err != nil {
		f.t.Fatal(err)
	}
	return run
}

// analysis writes an analysis folder beside the runs.
func (f *fixture) analysis(id string, d schemas.Decision, policy schemas.PolicyDecision) {
	f.t.Helper()
	a := &schemas.Analysis{AnalysisID: id, ExpID: f.m.ExpID, CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		GradeSource: "original", Verdict: schemas.Verdict{Decision: d, Action: schemas.ActionNoDecision}}
	if policy != "" {
		a.Policy = &schemas.PolicyResult{Decision: policy}
	}
	if err := f.st.WriteAnalysis(context.Background(), a, nil); err != nil {
		f.t.Fatal(err)
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

// rows drops the timestamps that differ between two writes.
func rows(t *testing.T, ix *Index) *Snapshot {
	t.Helper()
	s, err := ix.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s.BuiltAt, s.UpdatedAt = time.Time{}, time.Time{}
	return s
}

func TestRebuildEqualsIncremental(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "e_one", false)
	inc := New(f.st, nil)
	steps := []struct {
		slot    string
		outcome schemas.Outcome
	}{{"s_t1_a", schemas.OutcomePass}, {"s_t1_b", schemas.OutcomeFail}, {"s_t2_a", schemas.OutcomePass}, {"s_t2_b", schemas.OutcomeTimeout}}
	for i, s := range steps {
		f.seal(s.slot, s.outcome, float64(i+1)/10)
		if err := inc.UpdateExperiment(ctx, f.m.ExpID); err != nil {
			t.Fatal(err)
		}
	}
	f.analysis("a_1", schemas.DecisionRegressed, schemas.PolicyBlock)
	if err := inc.UpdateExperiment(ctx, f.m.ExpID); err != nil {
		t.Fatal(err)
	}
	incremental := rows(t, inc)
	if err := os.Remove(inc.Path()); err != nil {
		t.Fatal(err)
	}
	st, err := New(f.st, nil).Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Runs != 4 || st.Analyses != 1 || st.Rejected != 0 || st.Experiments != 1 {
		t.Fatalf("status = %+v", st)
	}
	rebuilt := rows(t, inc)
	if !reflect.DeepEqual(incremental, rebuilt) {
		t.Fatalf("incremental and rebuilt index differ:\n%+v\n%+v", incremental, rebuilt)
	}
	r := rebuilt.Runs(Filter{RunID: "r_t2_b"})
	if len(r) != 1 || r[0].Outcome != "timeout" || *r[0].Success != 0 || r[0].Suite != "coding_core@1.0.0" ||
		r[0].ConfigHash != "sha256:bbb" || r[0].Model != "m1" || r[0].Agent != "codex" || *r[0].CostUSD != 0.4 ||
		*r[0].WallTimeS != 42 || r[0].SealHash == "" || r[0].Synthetic {
		t.Fatalf("row = %+v", r)
	}
	if _, ok := r[0].Metrics["tokens"]; ok {
		t.Fatal("an unknown metric is indexed as a value")
	}
	if a := rebuilt.Analyses(""); len(a) != 1 || a[0].Decision != "regressed" || a[0].Policy != "block" {
		t.Fatalf("analyses = %+v", a)
	}
}

func TestTamperedRunRejected(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "e_tamper", false)
	f.seal("s_t1_a", schemas.OutcomePass, 0.1)
	run := f.seal("s_t1_b", schemas.OutcomePass, 0.2)
	dir := runDir(t, f.st.Root(), run.RunID)
	p := filepath.Join(dir, "metrics.json")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"cost_usd":{"state":"observed","v":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New(f.st, nil)
	st, err := ix.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Runs != 1 || st.Rejected != 1 {
		t.Fatalf("status = %+v, want 1 run and 1 rejected", st)
	}
	snap := rows(t, ix)
	rej := snap.Rejected()
	if rej[0].RunID != run.RunID || !strings.HasPrefix(rej[0].Reason, sealProblemPrefix) {
		t.Fatalf("rejected = %+v", rej)
	}
	if got := snap.Runs(Filter{RunID: run.RunID}); len(got) != 0 {
		t.Fatalf("tampered run indexed: %+v", got)
	}
}

func TestSyncIsIncremental(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "e_sync", false)
	f.seal("s_t1_a", schemas.OutcomePass, 0.1)
	ix := New(f.st, nil)
	st, err := ix.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Runs != 1 || st.BuiltAt.IsZero() {
		t.Fatalf("first sync = %+v", st)
	}
	before, err := os.Stat(ix.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(ix.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("sync rewrote an unchanged index")
	}
	f.seal("s_t1_b", schemas.OutcomeFail, 0.2)
	if st, err = ix.Sync(ctx); err != nil || st.Runs != 2 {
		t.Fatalf("sync after a seal = %+v, %v", st, err)
	}
}

func TestSyntheticClassification(t *testing.T) {
	tests := []struct {
		name   string
		smoke  bool
		ledger string
		want   bool
	}{
		{"live", false, "", false},
		{"smoke experiment", true, "", true},
		{"scripted upstream label", false, `{"kind":"message","upstream":"scripted"}` + "\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "e_syn", tt.smoke)
			if tt.ledger != "" {
				slot := schemas.Slot{SlotID: "s_t1_a", PairID: "p_t1", ExpID: "e_syn", Task: "t1", Arm: "A", Condition: "clean", Trial: 1, Counted: true}
				run := &schemas.Run{RunID: "r_t1_a", SlotID: slot.SlotID, PairID: slot.PairID, ExpID: "e_syn", Attempt: 1,
					Stage: schemas.StageCleanup, ManifestHash: f.m.Hash, Outcome: schemas.OutcomePass}
				ctx := context.Background()
				if _, err := f.st.NewRun(ctx, &slot, run); err != nil {
					t.Fatal(err)
				}
				if err := f.st.WriteRunFile(ctx, run, ledgerFile, []byte(tt.ledger)); err != nil {
					t.Fatal(err)
				}
				if _, err := f.st.Seal(ctx, run); err != nil {
					t.Fatal(err)
				}
			} else {
				f.seal("s_t1_a", schemas.OutcomePass, 0.1)
			}
			ix := New(f.st, nil)
			if _, err := ix.Rebuild(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := rows(t, ix).Runs(Filter{})
			if len(got) != 1 || got[0].Synthetic != tt.want {
				t.Fatalf("rows = %+v, want synthetic %v", got, tt.want)
			}
		})
	}
}

func TestScriptedLedgerFingerprint(t *testing.T) {
	fp := `{"kind":"message","action":"pass","status":200,"tokens":{"out":32},"response_id":"msg_0123456789abcdefghijklmn"}`
	real := `{"kind":"message","action":"pass","status":200,"tokens":{"out":32},"response_id":"msg_01AbCdEfGhIjKlMnOpQrStUv"}`
	if !scriptedLedger([]byte(fp)) {
		t.Fatal("scripted fingerprint not recognised")
	}
	if scriptedLedger([]byte(real)) {
		t.Fatal("real response id read as scripted")
	}
	if scriptedLedger([]byte(`{"upstream":"anthropic"}` + "\n" + fp)) {
		t.Fatal("a labelled ledger fell back to the fingerprint")
	}
}

func TestUpdateRemovedExperiment(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "e_gone", false)
	f.seal("s_t1_a", schemas.OutcomePass, 0.1)
	ix := New(f.st, nil)
	if _, err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	makeWritable(filepath.Join(f.st.Root(), "e_gone"))
	if err := os.RemoveAll(filepath.Join(f.st.Root(), "e_gone")); err != nil {
		t.Fatal(err)
	}
	if err := ix.UpdateExperiment(ctx, "e_gone"); err != nil {
		t.Fatal(err)
	}
	if st := rows(t, ix).Status(); st.Runs != 0 || st.Experiments != 0 {
		t.Fatalf("status = %+v", st)
	}
}

// runDir finds a run folder under the store root.
func runDir(t *testing.T, root, runID string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "*", "slots", "*", "runs", runID))
	if err != nil || len(matches) != 1 {
		t.Fatalf("run dir of %s: %v %v", runID, matches, err)
	}
	return matches[0]
}

// A dot folder under analysis/ is an analysis still being staged by
// store.WriteAnalysis: the index never reads it, even with a verdict.json.
func TestStagedAnalysisSkipped(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "e_one", false)
	f.seal("s_t1_a", schemas.OutcomePass, 0.1)
	stage := filepath.Join(f.st.Root(), f.m.ExpID, "analysis", ".stage-x")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "verdict.json"), []byte(`{"analysis_id":"x","verdict":{"decision":"invalid"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := New(f.st, nil).Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Analyses != 0 {
		t.Fatalf("analyses %d, want the staged one skipped", st.Analyses)
	}
}
