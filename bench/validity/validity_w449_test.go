package validity

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func analysis(source string, d schemas.Decision, a schemas.Action, reasons ...string) *schemas.Analysis {
	an := &schemas.Analysis{GradeSource: source}
	an.Verdict.Decision, an.Verdict.Action, an.Verdict.InvalidReasons = d, a, reasons
	return an
}

func TestDiffFlippedOnDecision(t *testing.T) {
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	e := Diff("e_1", "rg_1", analysis("original", "invalid", "rerun", "test broken"), analysis("rg_1", "inconclusive", "rerun"), RegradeMeta{ID: "rg_1", CreatedAt: at, Note: "fixed check"})
	if !e.Flipped || e.ID != "e_1:rg_1" || e.Before.Decision != "invalid" || e.After.Decision != "inconclusive" || e.Note != "fixed check" || !e.At.Equal(at) {
		t.Fatalf("entry %+v", e)
	}
	if e := Diff("e_1", "rg_1", analysis("original", "improved", "ship"), analysis("rg_1", "improved", "hold"), RegradeMeta{}); !e.Flipped {
		t.Fatal("action change not a flip")
	}
}

func TestDiffNotFlippedOnReasonsOnly(t *testing.T) {
	e := Diff("e_1", "rg_1", analysis("original", "invalid", "rerun", "a"), analysis("rg_1", "invalid", "rerun", "b"), RegradeMeta{})
	if e.Flipped || e.Before.InvalidReasons[0] != "a" || e.After.InvalidReasons[0] != "b" {
		t.Fatalf("entry %+v", e)
	}
}

func TestLazyLedgerCreatesOncePerRegrade(t *testing.T) {
	st := Open(t.TempDir())
	t0 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	regs := []RegradeMeta{{ID: "rg_2", CreatedAt: t0.Add(time.Hour)}, {ID: "rg_1", CreatedAt: t0}}
	calls := map[string]int{}
	analyze := func(_ context.Context, expID, src string) (*schemas.Analysis, error) {
		calls[src]++
		switch src {
		case "original":
			return analysis(src, "invalid", "rerun"), nil
		case "rg_1":
			return analysis(src, "inconclusive", "rerun"), nil
		}
		return analysis(src, "inconclusive", "rerun"), nil
	}
	lister := func(context.Context, string) ([]RegradeMeta, error) { return regs, nil }
	made, err := Fill(context.Background(), st, []string{"e_1"}, lister, analyze, MaxFillPerRequest)
	if err != nil || len(made) != 2 {
		t.Fatalf("first fill %v %v", made, err)
	}
	if made[0].RegradeID != "rg_1" || made[0].Before.Source != "original" || !made[0].Flipped || made[1].Before.Source != "rg_1" || made[1].Flipped {
		t.Fatalf("entries %+v", made)
	}
	n := len(calls)
	again, err := Fill(context.Background(), st, []string{"e_1"}, lister, analyze, MaxFillPerRequest)
	if err != nil || len(again) != 0 || len(calls) != n {
		t.Fatalf("second fill %v %v", again, err)
	}
	if es, _ := st.Entries(); len(es) != 2 {
		t.Fatalf("ledger %d", len(es))
	}
	// The cap holds.
	st2 := Open(t.TempDir())
	if made, _ := Fill(context.Background(), st2, []string{"e_1"}, lister, analyze, 1); len(made) != 1 {
		t.Fatalf("cap %d", len(made))
	}
}

func TestStaleScanVersions(t *testing.T) {
	now := time.Now()
	cur := map[string]string{"protected_files": "2", "tests_ran": "1"}
	sealed := [][]schemas.GateResult{{{Gate: "protected_files", Version: "1"}, {Gate: "tests_ran", Version: "1"}}}
	s, ok := StaleOf("e_1", cur, sealed, now)
	if !ok || s.Gates["protected_files"] != [2]string{"1", "2"} || len(s.Gates) != 1 {
		t.Fatalf("stale %+v", s)
	}
	if _, ok := StaleOf("e_1", cur, [][]schemas.GateResult{{{Gate: "protected_files", Version: "2"}}}, now); ok {
		t.Fatal("current versions marked stale")
	}
	st := Open(t.TempDir())
	if err := st.SetStale([]Stale{s}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Stale(); len(got) != 1 || got[0].ExpID != "e_1" {
		t.Fatalf("stored %+v", got)
	}
}

func TestSeenPerUser(t *testing.T) {
	st := Open(t.TempDir())
	if _, err := st.Add([]Entry{{ID: "e_1:rg_1", ExpID: "e_1", RegradeID: "rg_1", Flipped: true}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.MarkSeen("e_1:rg_1", "u1"); !ok || err != nil {
		t.Fatalf("mark %v %v", ok, err)
	}
	_, _ = st.MarkSeen("e_1:rg_1", "u1")
	es, _ := st.Entries()
	if !es[0].Seen("u1") || es[0].Seen("u2") || len(es[0].SeenBy) != 1 {
		t.Fatalf("seen %+v", es[0])
	}
	if ok, _ := st.MarkSeen("nope", "u1"); ok {
		t.Fatal("unknown id marked")
	}
}
