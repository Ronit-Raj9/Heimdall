package leaderboard

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestNewestRegrade(t *testing.T) {
	tests := []struct {
		name   string
		in     []Regrade
		want   string
		wantOK bool
	}{
		{name: "none", in: nil, wantOK: false},
		{name: "one", in: []Regrade{{ID: "rg_a", CreatedAt: t0}}, want: "rg_a", wantOK: true},
		{name: "latest created_at wins over id", in: []Regrade{{ID: "rg_z", CreatedAt: t0}, {ID: "rg_a", CreatedAt: t0.Add(time.Second)}}, want: "rg_a", wantOK: true},
		{name: "tie broken by the larger id", in: []Regrade{{ID: "rg_b", CreatedAt: t0}, {ID: "rg_c", CreatedAt: t0}, {ID: "rg_a", CreatedAt: t0}}, want: "rg_c", wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := newestRegrade(tt.in)
			if ok != tt.wantOK || got.ID != tt.want {
				t.Errorf("newestRegrade = %q %v, want %q %v", got.ID, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// metricsOf parses a metrics.json body.
func metricsOf(t *testing.T, body string) map[string]schemas.Value {
	t.Helper()
	m := map[string]schemas.Value{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// regradeSource has config h_a in e_one, where a gate vetoed t1, and in the
// newer e_two, which has no regrade. e_one's newer regrade rg_new lifts the
// veto; its older rg_old would have failed t2 instead. B's runs and the
// fault condition run are not in either regrade.
func regradeSource(t *testing.T) *fakeSource {
	f := newFake()
	f.exp("e_one", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h_a", "command"), "B": arm("b", "h_b", "command")})
	vetoed := f.run("e_one", "t1", "A", "clean", 1, 1, schemas.OutcomeIntegrityViolation, true, metricsJSON(0, 0.1))
	passed := f.pass("e_one", "t2", "A", "clean", 1, 0.1)
	f.fail("e_one", "t1", "B", "clean", 1, 0.1)
	f.fail("e_one", "t2", "B", "clean", 1, 0.1)
	f.fail("e_one", "t1", "A", "model-outage", 1, 0.1)
	f.exp("e_two", "s@1", 1, map[string]schemas.ArmManifest{"A": arm("a two", "h_a", "command")})
	f.pass("e_two", "t1", "A", "clean", 1, 0.1)

	f.regrades["e_one"] = []Regrade{{ID: "rg_new", CreatedAt: t0.Add(time.Hour)}, {ID: "rg_old", CreatedAt: t0}}
	f.regraded["e_one/rg_new"] = []schemas.SealedRun{
		{Run: schemas.Run{RunID: vetoed, Outcome: schemas.OutcomePass, Y: schemas.Observed(1, schemas.EvidenceOutcome)},
			GradeSource: "rg_new", Metrics: metricsOf(t, metricsJSON(1, 0.1))},
		{Run: schemas.Run{RunID: passed, Outcome: schemas.OutcomePass}, GradeSource: GradeSourceOriginal, Metrics: metricsOf(t, metricsJSON(1, 0.1))},
	}
	f.regraded["e_one/rg_old"] = []schemas.SealedRun{
		{Run: schemas.Run{RunID: passed, Outcome: schemas.OutcomeFail}, GradeSource: "rg_old", Metrics: metricsOf(t, metricsJSON(0, 0.1))},
	}
	return f
}

// sourceOnly hides the RegradeSource of the source it wraps.
type sourceOnly struct{ Source }

func TestBuildGradeSource(t *testing.T) {
	broken := regradeSource(t)
	broken.regradeErr = errors.New("regate hash mismatch")
	tests := []struct {
		name        string
		src         Source
		source      string
		wantSuccess float64
		wantPasses  schemas.Ratio
		wantSources []schemas.LeaderboardGradeSource
		wantErr     bool
	}{
		{
			name: "original reads every run as sealed", src: regradeSource(t), source: GradeSourceOriginal,
			wantSuccess: 0.75, wantPasses: schemas.Ratio{Num: 2, Den: 3},
			wantSources: []schemas.LeaderboardGradeSource{
				{Experiment: "e_two", Source: "original", Runs: 1},
				{Experiment: "e_one", Source: "original", Runs: 4},
			},
		},
		{
			name: "latest reads the newest regrade", src: regradeSource(t), source: GradeSourceLatest,
			wantSuccess: 1, wantPasses: schemas.Ratio{Num: 3, Den: 3},
			wantSources: []schemas.LeaderboardGradeSource{
				{Experiment: "e_two", Source: "original", Runs: 1},
				{Experiment: "e_one", Source: "rg_new", Runs: 4, RegradedRuns: 1},
			},
		},
		{
			name: "latest without a regrade source", src: sourceOnly{regradeSource(t)}, source: GradeSourceLatest,
			wantSuccess: 0.75, wantPasses: schemas.Ratio{Num: 2, Den: 3},
			wantSources: []schemas.LeaderboardGradeSource{
				{Experiment: "e_two", Source: "original", Runs: 1},
				{Experiment: "e_one", Source: "original", Runs: 4},
			},
		},
		{name: "unreadable regrade fails the board", src: broken, source: GradeSourceLatest, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb, err := Build(context.Background(), tt.src, Query{GradeSource: tt.source, Now: t0})
			if tt.wantErr {
				if err == nil {
					t.Fatal("Build succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if lb.GradeSource != tt.source {
				t.Errorf("grade source = %q, want %q", lb.GradeSource, tt.source)
			}
			if !reflect.DeepEqual(lb.GradeSources, tt.wantSources) {
				t.Errorf("grade sources = %+v, want %+v", lb.GradeSources, tt.wantSources)
			}
			a := lb.Entries[0]
			if v, _ := a.Success.Float(); a.ConfigHash != "h_a" || v != tt.wantSuccess || a.Passes != tt.wantPasses {
				t.Errorf("leader %s success %v passes %+v, want h_a %v %+v", a.ConfigHash, v, a.Passes, tt.wantSuccess, tt.wantPasses)
			}
		})
	}
}

func TestReadGradeSourceKeepsUnlistedRuns(t *testing.T) {
	f := regradeSource(t)
	recs, err := collect(context.Background(), f)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := readGradeSource(context.Background(), f, recs, GradeSourceLatest); err != nil {
		t.Fatalf("readGradeSource: %v", err)
	}
	for _, r := range recs {
		regraded := r.run.RunID == "r_e_one_t1_A_clean_1_1"
		if (r.source == "rg_new") != regraded {
			t.Errorf("run %s source %q", r.run.RunID, r.source)
		}
		if r.exp.id == "e_one" && r.slot.Arm == "B" && r.metrics != nil {
			t.Errorf("run %s not in the regrade got metrics %v", r.run.RunID, r.metrics)
		}
		if regraded && (r.run.Outcome != schemas.OutcomePass || r.metrics == nil) {
			t.Errorf("regraded run = %+v", r)
		}
	}
}
