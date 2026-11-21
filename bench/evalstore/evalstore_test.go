package evalstore

import (
	"errors"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestStoreRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	if l, err := s.List(); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	t0 := time.Now().UTC()
	for i, id := range []string{"ev_a", "ev_b"} {
		ev := &schemas.Evaluation{ID: id, Status: schemas.EvaluationQueued, CreatedAt: t0.Add(time.Duration(i) * time.Second)}
		if err := s.Save(ev); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.List()
	if err != nil || len(l) != 2 || l[0].ID != "ev_b" {
		t.Fatal(l, err)
	}
	if _, err := s.Load("ev_zz"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Load("../x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Save(&schemas.Evaluation{ID: "../bad"}); err == nil {
		t.Fatal("bad id saved")
	}
}
