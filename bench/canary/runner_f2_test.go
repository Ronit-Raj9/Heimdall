package canary

import (
	"context"
	"strings"
	"testing"
	"time"
)

// capPlanExec records the per-run cap each plan was built with.
type capPlanExec struct {
	*fakeExec
	caps []float64
}

func (c *capPlanExec) Plan(ctx context.Context, d *Definition, inject string) (*Plan, error) {
	c.caps = append(c.caps, d.Budget.PerRunUSD)
	return c.fakeExec.Plan(ctx, d, inject)
}

// PH5-6: per day equal to per check, a cheap baseline first, then Check
// now must run (with the day's remaining amount as its cap), not be refused.
func TestCheckRunsWhenDailyLimitEqualsCheckLimit(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good)
	ex.cost = 0.0002
	cx := &capPlanExec{fakeExec: ex}
	r := &Runner{Root: t.TempDir(), Exec: cx, Notifier: &fakeNotifier{}, Now: clock.now}
	d := testDef()
	d.Budget = Budget{PerRunUSD: 0.30, PerDayUSD: 0.30}
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || len(recs) == 0 || recs[0].Outcome != OutcomeClear {
		t.Fatalf("check after baseline = %+v, %v", recs, err)
	}
	if got := cx.caps[len(cx.caps)-1]; got > 0.30-0.0002+1e-9 || got <= 0 {
		t.Fatalf("check cap %v, want the day's remaining amount", got)
	}
	if recs[0].Runs < MinRunsToTell && !strings.Contains(recs[0].Reason, "Too few runs to tell") { // PH5-7
		t.Fatalf("a look on %d runs says %q", recs[0].Runs, recs[0].Reason)
	}
	if d.Budget.PerRunUSD != 0.30 {
		t.Fatalf("the definition's own cap changed: %v", d.Budget.PerRunUSD)
	}
}
