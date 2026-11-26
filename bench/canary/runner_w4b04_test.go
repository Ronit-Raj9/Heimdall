package canary

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestPlanErrorHasOnePrefix checks that a plan refusal is said once, with a
// plain prefix, on the baseline and on a later look (wave 4, p13-8).
func TestPlanErrorHasOnePrefix(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good, good)
	r, _ := newTestRunner(t, ex, clock)
	d := testDef()
	check := func(what, reason string) {
		t.Helper()
		if !strings.HasPrefix(reason, "Could not plan the check: ") || strings.Contains(reason, "plan: ") {
			t.Errorf("%s reason = %q", what, reason)
		}
	}
	ex.planErr = errors.New("plan e_x: arm A: boom")
	rec, err := r.Baseline(context.Background(), d, BaselineOptions{})
	if err == nil || rec == nil {
		t.Fatalf("baseline = %v, %v", rec, err)
	}
	check("baseline", rec.Reason)
	ex.planErr = nil
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	ex.planErr = errors.New("plan e_x: arm A: boom")
	recs, _ := r.Run(context.Background(), d, RunOptions{})
	if len(recs) == 0 {
		t.Fatal("look left no record")
	}
	check("look", recs[len(recs)-1].Reason)
}
