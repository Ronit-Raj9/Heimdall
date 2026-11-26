package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func TestWeeklyCapStopsItems(t *testing.T) {
	b := &fakeBuild{}
	m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, time.March), cand(2, time.April)}}, Builder: b, Validator: fakeValidate{}})
	s, err := m.Scan(context.Background(), "a/b", ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Limit(s.ID, 0.5, 1); err != nil { // the first build costs 0.5
		t.Fatal(err)
	}
	if _, err := m.StartWith(s.ID, true, ModelChoice{}); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	s, _ = m.Load(s.ID)
	if b.calls != 1 {
		t.Fatalf("builds = %d, want 1", b.calls)
	}
	if s.Items[1].Status != StatusFailed || s.Items[1].Reason != CapReachedReason {
		t.Fatalf("second item %s %q", s.Items[1].Status, s.Items[1].Reason)
	}
	if s.Items[0].Options.BudgetUSD != 1 {
		t.Fatalf("budget %v", s.Items[0].Options.BudgetUSD)
	}
}

func TestKeepDropsOthers(t *testing.T) {
	m := NewManager(t.TempDir(), Stages{Scanner: fakeScan{cands: []issuebench.Candidate{cand(1, time.March), cand(2, time.April), cand(3, time.May)}}})
	s, _ := m.Scan(context.Background(), "a/b", ScanOptions{})
	s, err := m.Keep(s.ID, []int{102})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 1 || s.Items[0].Candidate.PR != 102 || s.Funnel.Reasons["already_a_task"] != 2 {
		t.Fatalf("items %+v reasons %v", s.Items, s.Funnel.Reasons)
	}
}
