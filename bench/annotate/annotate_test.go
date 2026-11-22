package annotate

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	return Open(t.TempDir()).WithClock(func() time.Time { return now }), &now
}

func items(n int) []Item {
	var out []Item
	for i := 0; i < n; i++ {
		out = append(out, Item{Task: fmt.Sprintf("t%d", i), Answer: fmt.Sprintf("answer %d", i), RunID: fmt.Sprintf("r%d", i), JudgeLabel: LabelCorrect})
	}
	return out
}

func TestQueueNormalize(t *testing.T) {
	for _, q := range []Queue{{}, {Name: "x", Kind: "pairs"}, {Name: "x", ReviewersPerItem: 9}, {Name: "x", Assignment: "fixed", ReviewersPerItem: 2, Reviewers: []string{"u1"}}, {Name: "x", Benchmark: "../etc"}} {
		if err := q.Normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted: %v", q, err)
		}
	}
	q := Queue{Name: " Support answers ", Reviewers: []string{"u2", "u1", "u2"}}
	if err := q.Normalize(); err != nil || q.Kind != KindRuns || q.ReviewersPerItem != 1 || q.Assignment != AssignOpen || len(q.Reviewers) != 2 {
		t.Fatalf("defaults %+v %v", q, err)
	}
}

func TestConsensusDisputeAndDecision(t *testing.T) {
	s, _ := newStore(t)
	q, err := s.Create(Queue{Name: "q", ReviewersPerItem: 2})
	if err != nil {
		t.Fatal(err)
	}
	added, dup, err := s.AddItems(q.ID, append(items(2), items(1)...), "a")
	if err != nil || added != 2 || dup != 1 {
		t.Fatalf("add %d %d %v", added, dup, err)
	}
	// u1 and u2 take the first item; u3 then gets the second.
	a, _, _ := s.Next(q.ID, "u1")
	b, _, _ := s.Next(q.ID, "u2")
	c, _, _ := s.Next(q.ID, "u3")
	if a.ID != b.ID || c.ID == a.ID {
		t.Fatalf("assignment: %s %s %s", a.ID, b.ID, c.ID)
	}
	if again, _, _ := s.Next(q.ID, "u1"); again.ID != a.ID {
		t.Fatal("a claimed item was not handed back to its claimer")
	}
	if _, err := s.AddLabel(q.ID, a.ID, Label{UserID: "u3", Label: "correct"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("third reviewer on a full item: %v", err)
	}
	r, err := s.AddLabel(q.ID, a.ID, Label{UserID: "u1", Label: "Correct"})
	if err != nil || r.Resolved {
		t.Fatalf("first label %+v %v", r, err)
	}
	if _, err := s.AddLabel(q.ID, a.ID, Label{UserID: "u1", Label: "incorrect"}); !errors.Is(err, ErrConflict) {
		t.Fatal("same person labelled twice")
	}
	r, _ = s.AddLabel(q.ID, a.ID, Label{UserID: "u2", Label: "correct"})
	if !r.Resolved || r.Item.Resolved() != LabelCorrect || r.Item.Resolution.Method != "consensus" {
		t.Fatalf("consensus %+v", r.Item.Resolution)
	}
	_, _ = s.AddLabel(q.ID, c.ID, Label{UserID: "u3", Label: "correct"})
	r, _ = s.AddLabel(q.ID, c.ID, Label{UserID: "u4", Label: "incorrect"})
	if r.Resolved || r.Item.State(2) != StateDisputed {
		t.Fatalf("dispute state %s", r.Item.State(2))
	}
	it, _, err := s.Resolve(q.ID, c.ID, Resolution{Label: "incorrect", UserID: "owner", Note: "policy says no refunds"})
	if err != nil || it.Resolved() != LabelIncorrect || it.Resolution.Method != "decided" {
		t.Fatalf("decide %+v %v", it, err)
	}
	f, _ := s.Get(q.ID)
	cnt := CountsOf(f.Items, 2)
	if cnt.Done != 2 || cnt.Disputed != 0 {
		t.Fatalf("counts %+v", cnt)
	}
	if nothing, _, _ := s.Next(q.ID, "u1"); nothing != nil {
		t.Fatal("next on a finished queue returned an item")
	}
}

func TestClaimExpires(t *testing.T) {
	s, now := newStore(t)
	q, _ := s.Create(Queue{Name: "q"})
	_, _, _ = s.AddItems(q.ID, items(1), "")
	a, _, _ := s.Next(q.ID, "u1")
	if b, _, _ := s.Next(q.ID, "u2"); b != nil {
		t.Fatal("claimed item given to a second person")
	}
	*now = now.Add(ClaimTTL + time.Minute)
	if b, _, _ := s.Next(q.ID, "u2"); b == nil || b.ID != a.ID {
		t.Fatal("expired claim not released")
	}
}

func TestFixedAssignmentAndReviewers(t *testing.T) {
	s, _ := newStore(t)
	q, err := s.Create(Queue{Name: "q", ReviewersPerItem: 2, Assignment: AssignFixed, Reviewers: []string{"u1", "u2", "u3"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.AddItems(q.ID, items(3), "")
	n, err := s.Assign(q.ID)
	if err != nil || n != 6 {
		t.Fatalf("assign %d %v", n, err)
	}
	if n, _ := s.Assign(q.ID); n != 0 {
		t.Fatalf("reassigned %d", n)
	}
	f, _ := s.Get(q.ID)
	per := map[string]int{}
	for _, it := range f.Items {
		if len(it.Assigned) != 2 || it.Assigned[0].UserID == it.Assigned[1].UserID {
			t.Fatalf("item assignment %+v", it.Assigned)
		}
		for _, a := range it.Assigned {
			per[a.UserID]++
		}
	}
	if per["u1"] != 2 || per["u2"] != 2 || per["u3"] != 2 {
		t.Fatalf("unbalanced %v", per)
	}
	if _, _, err := s.Next(q.ID, "outsider"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider: %v", err)
	}
	notMine := ""
	for _, it := range f.Items {
		if !it.assignedTo("u1", time.Now()) {
			notMine = it.ID
		}
	}
	if _, err := s.AddLabel(q.ID, notMine, Label{UserID: "u1", Label: "correct"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("label on another's item: %v", err)
	}
}

func TestAgreementStatistics(t *testing.T) {
	mk := func(judge string, labels ...string) Item {
		it := Item{JudgeLabel: judge}
		for i, l := range labels {
			it.Labels = append(it.Labels, Label{UserID: fmt.Sprintf("u%d", i+1), Label: l})
		}
		if len(labels) > 0 {
			agree := true
			for _, l := range labels {
				agree = agree && l == labels[0]
			}
			if agree {
				it.Resolution = &Resolution{Label: labels[0]}
			}
		}
		return it
	}
	c, i := LabelCorrect, LabelIncorrect
	// Two raters, 10 items: 4 both correct, 4 both incorrect, 2 split.
	var its []Item
	for n := 0; n < 4; n++ {
		its = append(its, mk(c, c, c), mk(c, i, i))
	}
	its = append(its, mk(c, c, i), mk(i, i, c))
	a := ComputeAgreement(its, 2)
	if a.MultiLabelled != 10 || a.Unanimous != 8 || *a.PercentAgreement != 0.8 {
		t.Fatalf("counts %+v", a)
	}
	if len(a.Cohen) != 1 || a.Cohen[0].Kappa == nil || *a.Cohen[0].Kappa != 0.6 {
		t.Fatalf("cohen %+v", a.Cohen)
	}
	// Fleiss with two raters on these counts: P-bar 0.8, P-e 0.5, kappa 0.6.
	if a.Fleiss == nil || *a.Fleiss != 0.6 || a.Interpretation != "moderate" {
		t.Fatalf("fleiss %v %s", a.Fleiss, a.Interpretation)
	}
	// The judge said correct on all 8 resolved items: 4 right, 4 false passes.
	if a.Judge == nil || a.Judge.Items != 8 || a.Judge.Agree != 4 || a.Judge.FalsePasses != 4 {
		t.Fatalf("judge %+v", a.Judge)
	}
	// Fleiss reference value: Fleiss (1971) style example of 3 raters.
	three := [][]int{{3, 0}, {0, 3}, {2, 1}, {1, 2}, {3, 0}}
	if k := fleiss(three); k == nil || *k != 0.444 {
		t.Fatalf("fleiss 3 raters %v", k)
	}
	if k := cohen([]string{c, c}, []string{c, c}); k == nil || *k != 1 {
		t.Fatal("perfect single-class agreement")
	}
	if ComputeAgreement(nil, 2).Fleiss != nil {
		t.Fatal("fleiss on nothing")
	}
}
