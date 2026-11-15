package clusters

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeSource is an in-memory Source: experiments with manifests, verified
// runs and evidence files.
type fakeSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]schemas.SealedRun
	files     map[string]map[string][]byte // run id -> name -> bytes
	broken    map[string]bool              // experiments whose VerifiedRuns fails
}

func (f *fakeSource) Experiments() []string {
	var ids []string
	for id := range f.manifests {
		ids = append(ids, id)
	}
	return ids
}

func (f *fakeSource) Manifest(expID string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[expID]
	return m, ok
}

func (f *fakeSource) VerifiedRuns(_ context.Context, expID string) ([]schemas.SealedRun, error) {
	if f.broken[expID] {
		return nil, errors.New("disk on fire")
	}
	return f.runs[expID], nil
}

func (f *fakeSource) RunEvidence(runID, name string) ([]byte, error) {
	if data, ok := f.files[runID][name]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("run %s file %s: %w", runID, name, fs.ErrNotExist)
}

var day = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// fixture holds two experiments of coding-core@1.0.0 and one smoke
// experiment:
//
//   - e_old: codex config h1 fails t01 on test_window once, passes t02;
//   - e_new: codex config h2 (a later config) fails t01 on test_window twice
//     and t02 on test_csv once; custom (config h9) fails t01 on test_window
//     with a stderr error; one run's seal does not verify;
//   - e_smoke: a scripted agent fails t01 (synthetic).
func fixture() *fakeSource {
	src := &fakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]schemas.SealedRun{},
		files: map[string]map[string][]byte{}, broken: map[string]bool{}}
	manifest := func(id string, at time.Time, arms map[string]schemas.ArmManifest) {
		src.manifests[id] = &schemas.Manifest{ExpID: id, CreatedAt: at, SuiteID: "coding-core", SuiteVersion: "1.0.0", Arms: arms}
	}
	codex := func(hash string) schemas.ArmManifest {
		return schemas.ArmManifest{ConfigHash: hash, Config: schemas.AgentConfig{ID: "codex", Adapter: "command"}}
	}
	manifest("e_old", day, map[string]schemas.ArmManifest{"A": codex("h1")})
	manifest("e_new", day.Add(48*time.Hour), map[string]schemas.ArmManifest{"A": codex("h2"),
		"B": {ConfigHash: "h9", Config: schemas.AgentConfig{ID: "custom", Adapter: "command"}}})
	manifest("e_smoke", day.Add(72*time.Hour), map[string]schemas.ArmManifest{"A": {ConfigHash: "h0", Config: schemas.AgentConfig{ID: "toy", Adapter: "scripted"}}})
	add := func(exp, id, task, arm string, at time.Time, outcome schemas.Outcome, tests ...string) *schemas.SealedRun {
		r := schemas.SealedRun{
			Slot:    schemas.Slot{SlotID: "s_" + id, ExpID: exp, Suite: "coding-core", Task: task, Arm: arm, Condition: schemas.ConditionClean, Counted: true},
			Run:     schemas.Run{RunID: id, ExpID: exp, Outcome: outcome, Timing: schemas.RunTiming{Started: at}},
			Metrics: map[string]schemas.Value{},
		}
		if len(tests) > 0 {
			r.Grade = &schemas.Grade{FailedTests: tests}
		}
		src.runs[exp] = append(src.runs[exp], r)
		return &src.runs[exp][len(src.runs[exp])-1]
	}
	add("e_old", "r_old1", "t01", "A", day, schemas.OutcomeFail, "tests.test_limit::test_window")
	add("e_old", "r_old2", "t02", "A", day.Add(time.Hour), schemas.OutcomePass)
	add("e_new", "r_new1", "t01", "A", day.Add(48*time.Hour), schemas.OutcomeFail, "tests.test_limit::test_window")
	add("e_new", "r_new2", "t01", "A", day.Add(49*time.Hour), schemas.OutcomeFail, "tests.test_limit::test_window")
	add("e_new", "r_new3", "t02", "A", day.Add(50*time.Hour), schemas.OutcomeFail, "tests.test_csv::test_quotes")
	add("e_new", "r_new4", "t01", "B", day.Add(51*time.Hour), schemas.OutcomeFail, "tests.test_limit::test_window")
	src.files["r_new4"] = map[string][]byte{FileStderr: []byte("loading...\nRuntimeError: window 17 overflow\n")}
	bad := add("e_new", "r_new5", "t02", "B", day.Add(52*time.Hour), schemas.OutcomeFail, "tests.test_csv::test_quotes")
	bad.Problems = []string{"seal does not verify: file grade.json changed"}
	add("e_smoke", "r_smoke1", "t01", "A", day.Add(72*time.Hour), schemas.OutcomeFail, "tests.test_limit::test_window")
	return src
}

func TestBuild(t *testing.T) {
	now := day.Add(96 * time.Hour)
	tests := []struct {
		name     string
		q        Query
		runs     int
		failures int
		counts   []int // cluster counts in order
		hidden   int
	}{
		{name: "defaults", q: Query{}, runs: 6, failures: 5, counts: []int{3, 1, 1}},
		{name: "include synthetic", q: Query{Synthetic: SyntheticInclude}, runs: 7, failures: 6, counts: []int{4, 1, 1}},
		{name: "synthetic only", q: Query{Synthetic: SyntheticOnly}, runs: 1, failures: 1, counts: []int{1}},
		{name: "agent by id", q: Query{Agent: "custom"}, runs: 1, failures: 1, counts: []int{1}},
		{name: "agent by hash prefix needs 6 characters", q: Query{Agent: "h2"}, runs: 0, failures: 0, counts: nil},
		{name: "one experiment", q: Query{Experiments: []string{"e_old"}}, runs: 2, failures: 1, counts: []int{1}},
		{name: "comma list", q: Query{Experiments: []string{"e_old, e_new"}}, runs: 6, failures: 5, counts: []int{3, 1, 1}},
		{name: "since a date", q: Query{Since: "2026-11-11"}, runs: 4, failures: 4, counts: []int{2, 1, 1}},
		{name: "since an age", q: Query{Since: "2d"}, runs: 4, failures: 4, counts: []int{2, 1, 1}},
		{name: "since a shorter age", q: Query{Since: "36h"}, runs: 0, failures: 0, counts: nil},
		{name: "suite with version", q: Query{Suite: "coding-core@1.0.0"}, runs: 6, failures: 5, counts: []int{3, 1, 1}},
		{name: "other suite", q: Query{Suite: "coding-core@2.0.0"}, runs: 0, failures: 0, counts: nil},
		{name: "min", q: Query{Min: 2}, runs: 6, failures: 5, counts: []int{3}, hidden: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.q.Now = now
			rep, err := Build(context.Background(), fixture(), tt.q)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if rep.Runs != tt.runs || rep.Failures != tt.failures || rep.Hidden != tt.hidden {
				t.Fatalf("runs %d failures %d hidden %d, want %d %d %d", rep.Runs, rep.Failures, rep.Hidden, tt.runs, tt.failures, tt.hidden)
			}
			var counts []int
			for _, c := range rep.Clusters {
				counts = append(counts, c.Count)
			}
			if fmt.Sprint(counts) != fmt.Sprint(tt.counts) {
				t.Fatalf("counts %v, want %v", counts, tt.counts)
			}
		})
	}
}

func TestBuildCluster(t *testing.T) {
	rep, err := Build(context.Background(), fixture(), Query{Now: day.Add(96 * time.Hour)})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	top := rep.Clusters[0]
	t.Run("summary", func(t *testing.T) {
		if top.Title != "fails test_window" || top.Count != 3 || top.Scope != ScopeTask {
			t.Fatalf("top %q count %d scope %s", top.Title, top.Count, top.Scope)
		}
		if fmt.Sprint(top.Experiments) != "[e_new e_old]" || fmt.Sprint(top.ConfigHashes) != "[h1 h2]" || fmt.Sprint(top.Tasks) != "[coding-core/t01]" {
			t.Fatalf("experiments %v hashes %v tasks %v", top.Experiments, top.ConfigHashes, top.Tasks)
		}
		if !top.FirstSeen.Equal(day) || !top.LastSeen.Equal(day.Add(49*time.Hour)) {
			t.Fatalf("seen %s to %s", top.FirstSeen, top.LastSeen)
		}
		if fmt.Sprint(top.Examples) != "[r_new2 r_new1 r_old1]" {
			t.Fatalf("examples %v, want newest first", top.Examples)
		}
		if top.Share != 3.0/5.0 {
			t.Fatalf("share %v", top.Share)
		}
	})
	t.Run("related, never merged", func(t *testing.T) {
		if len(top.Related) != 1 || top.Related[0].Reason != "same failed set, different error fingerprint" {
			t.Fatalf("related %+v", top.Related)
		}
		var other *Cluster
		for i := range rep.Clusters {
			if rep.Clusters[i].Signature == top.Related[0].Signature {
				other = &rep.Clusters[i]
			}
		}
		if other == nil || other.ErrorText != "runtimeerror: window <n> overflow" || len(other.Related) != 1 {
			t.Fatalf("related cluster %+v", other)
		}
	})
	t.Run("config change", func(t *testing.T) {
		var csv *Cluster
		for i := range rep.Clusters {
			if rep.Clusters[i].Title == "fails test_quotes" {
				csv = &rep.Clusters[i]
			}
		}
		if csv == nil || csv.AfterConfigChange == nil {
			t.Fatalf("csv cluster %+v, want a config change", csv)
		}
		cc := csv.AfterConfigChange
		if cc.Agent != "codex" || cc.FromHash != "h1" || cc.ToHash != "h2" || !cc.At.Equal(day.Add(48*time.Hour)) {
			t.Fatalf("config change %+v", cc)
		}
		if top.AfterConfigChange != nil {
			t.Fatalf("the window cluster spans both configs: %+v", top.AfterConfigChange)
		}
	})
	t.Run("exclusions", func(t *testing.T) {
		if fmt.Sprint(rep.Excluded) != "[{seal does not verify 1} {synthetic 1}]" {
			t.Fatalf("excluded %v", rep.Excluded)
		}
	})
	t.Run("find", func(t *testing.T) {
		d, err := Find(rep, top.Signature)
		if err != nil || len(d.Runs) != 3 || d.Runs[0].RunID != "r_old1" || d.Failures != 5 {
			t.Fatalf("detail %+v, %v", d, err)
		}
		if _, err := Find(rep, "c_nope"); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("err %v, want not found", err)
		}
	})
	t.Run("find ignores min", func(t *testing.T) {
		small, err := Build(context.Background(), fixture(), Query{Min: 10, Now: day.Add(96 * time.Hour)})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if _, err := Find(small, top.Signature); err != nil {
			t.Fatalf("find: %v", err)
		}
	})
}

func TestBuildErrors(t *testing.T) {
	bad := []Query{{Synthetic: "maybe"}, {Min: -1}, {Since: "last tuesday"}, {Agent: "a b"}, {Since: "-3d"}}
	for _, q := range bad {
		if _, err := Build(context.Background(), fixture(), q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("query %+v: err %v, want ErrBadQuery", q, err)
		}
	}
	t.Run("unreadable experiment is excluded, not fatal", func(t *testing.T) {
		src := fixture()
		src.broken["e_old"] = true
		rep, err := Build(context.Background(), src, Query{})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if fmt.Sprint(rep.Excluded) != "[{experiment unreadable 1} {seal does not verify 1} {synthetic 1}]" {
			t.Fatalf("excluded %v", rep.Excluded)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		src := &cancelSource{fixture()}
		if _, err := Build(ctx, src, Query{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v, want canceled", err)
		}
	})
}

// cancelSource returns the context's error from VerifiedRuns, as the store does.
type cancelSource struct{ *fakeSource }

func (c *cancelSource) VerifiedRuns(ctx context.Context, expID string) ([]schemas.SealedRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.fakeSource.VerifiedRuns(ctx, expID)
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Time{
		"":                     {},
		"2026-11-01":           time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		"2026-11-01T08:00:00Z": time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"36h":                  now.Add(-36 * time.Hour),
	}
	for in, want := range tests {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
}

func TestBuildEmptyListsAreNotNull(t *testing.T) {
	rep, err := Build(context.Background(), fixture(), Query{Suite: "other"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Experiments == nil || rep.Clusters == nil || rep.Excluded == nil || rep.Filters.Experiments == nil {
		t.Fatalf("nil list in %+v", rep)
	}
}
