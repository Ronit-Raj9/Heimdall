package annotate

import (
	"errors"
	"strings"
	"testing"
)

// Skipping the reserved item gives the other open item and frees the
// skipped one for someone else; with nothing else waiting the skipped item
// stays the caller's (wave 4, p12-3).
func TestNextSkippingReleasesReservation(t *testing.T) {
	s, _ := newStore(t)
	q, _ := s.Create(Queue{Name: "q"})
	_, _, _ = s.AddItems(q.ID, items(2), "")
	a, _, err := s.Next(q.ID, "u1")
	if err != nil || a == nil {
		t.Fatalf("first next: %v %v", a, err)
	}
	if again, _, _ := s.Next(q.ID, "u1"); again == nil || again.ID != a.ID {
		t.Fatal("next without skip must keep the reserved item")
	}
	b, _, err := s.NextSkipping(q.ID, "u1", []string{a.ID})
	if err != nil || b == nil || b.ID == a.ID {
		t.Fatalf("skip gave %v %v", b, err)
	}
	if c, _, _ := s.Next(q.ID, "u2"); c == nil || c.ID != a.ID {
		t.Fatalf("the skipped item must be free for another reviewer, got %v", c)
	}
	same, _, err := s.NextSkipping(q.ID, "u1", []string{b.ID})
	if err != nil || same == nil || same.ID != b.ID {
		t.Fatalf("with nothing else waiting the skipped item stays, got %v %v", same, err)
	}
}

// The two labelling refusals are plain sentences a reviewer can act on.
func TestFixedAssignmentMessagePlain(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Create(Queue{Name: "q", ReviewersPerItem: 3, Assignment: AssignFixed, Reviewers: []string{"u1"}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Name at least 3 people under Only these people, one for each label an item wants, or lower People per item.") {
		t.Fatalf("fixed assignment: %v", err)
	}
	q, _ := s.Create(Queue{Name: "q2"})
	_, _, _ = s.AddItems(q.ID, items(1), "")
	f, _ := s.Get(q.ID)
	_, _, err = s.Resolve(q.ID, f.Items[0].ID, Resolution{Label: LabelCorrect})
	if err == nil || !strings.Contains(err.Error(), "Label this item before deciding it.") {
		t.Fatalf("decide: %v", err)
	}
}
