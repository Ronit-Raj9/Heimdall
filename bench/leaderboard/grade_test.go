package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestExcludeReason(t *testing.T) {
	live, invalid := &expInfo{id: "e"}, &expInfo{id: "e", invalid: true}
	tests := []struct {
		name string
		rec  record
		want string
	}{
		{name: "pass", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ""},
		{name: "fail", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeFail}}, want: ""},
		{name: "integrity violation is graded", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeIntegrityViolation}}, want: ""},
		{name: "timeout is graded", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeTimeout}}, want: ""},
		{name: "infra error", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeInfraError}}, want: "infra_error"},
		{name: "invalid evaluator", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeInvalidEvaluator}}, want: "invalid_evaluator"},
		{name: "aborted", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeAbortedInfra}}, want: "aborted_infra"},
		{name: "no outcome", rec: record{exp: live, sealed: true}, want: "no outcome"},
		{name: "unsealed", rec: record{exp: live, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ReasonNotSealed},
		{name: "invalid verdict wins", rec: record{exp: invalid, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ReasonVerdictInvalid},
		{name: "two counted runs in a slot", rec: record{exp: live, sealed: true, counted: 2, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ReasonDuplicateSlot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excludeReason(&tt.rec); got != tt.want {
				t.Errorf("excludeReason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGradeSuccess(t *testing.T) {
	f := newFake()
	f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command")})
	f.run("e", "t1", "A", "clean", 1, 1, schemas.OutcomePass, true, metricsJSON(1, 0.1))
	// A veto after a passing verifier: integrity_violation, verified_success 0.
	f.run("e", "t2", "A", "clean", 1, 1, schemas.OutcomeIntegrityViolation, true, metricsJSON(0, 0.1))
	// Measured before verified_success existed: the outcome decides.
	f.run("e", "t3", "A", "clean", 1, 1, schemas.OutcomePass, true, "")
	f.run("e", "t4", "A", "clean", 1, 1, schemas.OutcomeTimeout, true, `{"verified_success":{"state":"unknown","v":null,"reason":"no grade"}}`)
	recs, err := collect(context.Background(), f)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	p, excluded, err := grade(context.Background(), f, recs)
	if err != nil {
		t.Fatalf("grade: %v", err)
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %+v, want none", excluded)
	}
	got := map[string]float64{}
	for _, r := range p["h"]["clean"] {
		got[r.rec.slot.Task] = r.success
	}
	want := map[string]float64{"t1": 1, "t2": 0, "t3": 1, "t4": 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("success = %v, want %v", got, want)
	}
}

func TestExclusions(t *testing.T) {
	older := &expInfo{id: "e_b", m: &schemas.Manifest{CreatedAt: t0}}
	newer := &expInfo{id: "e_a", m: &schemas.Manifest{CreatedAt: t0.Add(1)}}
	rec := func(e *expInfo, cond string) *record { return &record{exp: e, slot: schemas.Slot{Condition: cond}} }
	got := exclusions([]excludedRun{
		{rec: rec(older, "clean"), reason: "not sealed"},
		{rec: rec(newer, "clean"), reason: "not sealed"},
		{rec: rec(newer, "clean"), reason: "infra_error"},
		{rec: rec(newer, "model-outage"), reason: "infra_error"},
	}, "clean")
	want := []schemas.LeaderboardExclusion{
		{Reason: "infra_error", Runs: 1, Experiments: []string{"e_a"}},
		{Reason: "not sealed", Runs: 2, Experiments: []string{"e_a", "e_b"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exclusions = %+v, want %+v", got, want)
	}
}

// sealedSource is a fakeSource whose VerifySeal fails for the listed runs.
type sealedSource struct {
	*fakeSource
	bad map[string]error
}

func (s sealedSource) VerifySeal(_ context.Context, runID string) error {
	return s.bad[runID]
}

func TestGradeSealMismatch(t *testing.T) {
	f := newFake()
	f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command")})
	good := f.pass("e", "t1", "A", "clean", 1, 0.1)
	bad := f.pass("e", "t2", "A", "clean", 1, 0.1)
	mismatch := fmt.Errorf("verify run %s: file metrics.json changed: %w", bad, schemas.ErrSealMismatch)
	tests := []struct {
		name     string
		src      Source
		graded   int
		excluded map[string]string // run id -> reason
		err      bool
	}{
		{"a source that cannot verify trusts the seal", f, 2, map[string]string{}, false},
		{"a run that does not verify is left out", sealedSource{f, map[string]error{bad: mismatch}}, 1, map[string]string{bad: ReasonSealMismatch}, false},
		{"every run verifies", sealedSource{f, map[string]error{}}, 2, map[string]string{}, false},
		{"a verification that cannot run fails the board", sealedSource{f, map[string]error{good: errors.New("disk gone")}}, 0, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs, err := collect(context.Background(), tt.src)
			if err != nil {
				t.Fatal(err)
			}
			p, excluded, err := grade(context.Background(), tt.src, recs)
			if tt.err {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := len(p["h"]["clean"]); n != tt.graded {
				t.Fatalf("%d graded, want %d", n, tt.graded)
			}
			got := map[string]string{}
			for _, x := range excluded {
				got[x.rec.run.RunID] = x.reason
			}
			if !reflect.DeepEqual(got, tt.excluded) {
				t.Fatalf("excluded %v, want %v", got, tt.excluded)
			}
		})
	}
}

// One run's damaged or unparsable proxy ledger never fails the board: a run
// whose seal does not verify is checked before its ledger is read, and a
// verified run with an unparsable ledger is excluded on its own (finding
// 1-D-06).
func TestBuildDamagedLedgerExcludesOnlyThatRun(t *testing.T) {
	f := newFake()
	f.exp("e_ok", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command")})
	f.exp("e_bad", "s@1", 1, map[string]schemas.ArmManifest{"A": arm("b", "g", "command")})
	good := f.pass("e_ok", "t1", "A", "clean", 1, 0.1)
	tampered := f.pass("e_bad", "t1", "A", "clean", 1, 0.1)
	torn := f.pass("e_bad", "t2", "A", "clean", 1, 0.1)
	f.files[tampered][fileLedger] = []byte(`{not json`)
	f.files[torn][fileLedger] = []byte(`{"seq":`)
	src := sealedSource{f, map[string]error{tampered: fmt.Errorf("ledger changed: %w", schemas.ErrSealMismatch)}}
	lb, err := Build(context.Background(), src, Query{})
	if err != nil {
		t.Fatalf("Build failed on one damaged ledger: %v", err)
	}
	reasons := map[string]int{}
	for _, x := range lb.Excluded {
		reasons[x.Reason] += x.Runs
	}
	if reasons[ReasonSealMismatch] != 1 || reasons[ReasonEvidenceUnreadable] != 1 {
		t.Fatalf("excluded %+v, want one seal mismatch and one unreadable ledger", lb.Excluded)
	}
	found := false
	for _, e := range lb.Entries {
		found = found || e.ConfigHash == "h"
	}
	if !found || good == "" {
		t.Fatalf("entries %+v, want the healthy experiment ranked", lb.Entries)
	}
	// A ledger that cannot be read at all excludes its runs the same way.
	broken := boardSource()
	broken.fileErr = errors.New("disk on fire")
	lb, err = Build(context.Background(), broken, Query{})
	if err != nil || len(lb.Entries) != 0 {
		t.Fatalf("unreadable files: entries %d err %v, want every run excluded", len(lb.Entries), err)
	}
}
