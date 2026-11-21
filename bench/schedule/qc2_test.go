package schedule

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestDaemonReservesBeforeRun is QC2-15-10: the approved cap is in the
// ledger while the run goes, so a crash mid-run leaves it counted, and the
// settlement replaces it with the run's cost.
func TestDaemonReservesBeforeRun(t *testing.T) {
	clock := newFakeClock(start)
	s := Schedule{Name: "res", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: 10}
	var during float64
	r := &fakeRunner{clock: clock, result: Result{CostUSD: 1.5}}
	d, state := newTestDaemon(t, s, 4, r, nil)
	r.during = func() { during, _ = SpentToday(state, s, clock.Now()) }
	rec, err := d.Trigger(context.Background(), "res")
	if err != nil {
		t.Fatal(err)
	}
	if during != 4 {
		t.Fatalf("ledger during the run = %.2f, want the 4.00 reservation", during)
	}
	if rec.Status != StatusOK || rec.SpentTodayUSD != 1.5 {
		t.Fatalf("settled record %+v, want 1.50", rec)
	}
}

// TestRunnerSeesApproval is QC2-15-05: the Runner gets the approved cap and
// refuses an experiment file whose cap grew after the check.
func TestRunnerSeesApproval(t *testing.T) {
	clock := newFakeClock(start)
	s := Schedule{Name: "ap", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: 5}
	var got *Approval
	r := &fakeRunner{clock: clock}
	d, _ := newTestDaemon(t, s, 1, r, nil)
	d.o.Runner = approvalRunner{fakeRunner: r, seen: &got}
	if _, err := d.Trigger(context.Background(), "ap"); err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Limited || got.CapUSD != 1 {
		t.Fatalf("approval %+v, want limited at 1.00", got)
	}
	if err := got.CheckExperiment(&schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: 10}}); err == nil {
		t.Fatal("a replaced file with a 10.00 cap passed a 1.00 approval")
	}
	if err := got.CheckExperiment(&schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: 1}}); err != nil {
		t.Fatal(err)
	}
	// A confirmation-style reservation past the day's cap is refused.
	if why := got.Reserve(5.5); why == "" {
		t.Fatal("reservation past max_usd_per_day accepted")
	}
}

type approvalRunner struct {
	*fakeRunner
	seen **Approval
}

func (a approvalRunner) Run(ctx context.Context, s Schedule) (*Result, error) {
	*a.seen = s.Approved
	return a.fakeRunner.Run(ctx, s)
}

// TestNonFiniteDailyCapRefused is QC2-15-09.
func TestNonFiniteDailyCapRefused(t *testing.T) {
	for _, c := range []float64{math.NaN(), math.Inf(1)} {
		s := Schedule{Name: "nf", Experiment: "x.yaml", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: c}
		d := &Daemon{}
		if _, why := d.approve(s, "2026-11-15"); !strings.Contains(why, "finite") {
			t.Errorf("cap %v: refusal %q", c, why)
		}
		if err := s.Validate(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "finite") {
			t.Errorf("cap %v: validate %v", c, err)
		}
	}
}
