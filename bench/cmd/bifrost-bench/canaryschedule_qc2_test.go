package main

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/schedule"
)

// TestCanaryConfirmationFitsScheduleCap is QC2-15-04: a scheduled canary's
// confirmation look reserves its per-run cap under the schedule's daily cap
// first, so a schedule cap below the canary's own cannot be passed.
func TestCanaryConfirmationFitsScheduleCap(t *testing.T) {
	d := &canary.Definition{Name: "watch", Budget: canary.Budget{PerRunUSD: 6, PerDayUSD: 20}}
	day := 6.0 // the daemon's reservation of the first look
	ap := &schedule.Approval{CapUSD: 6, Limited: true, Reserve: func(usd float64) string {
		if day+usd > 10 {
			return "daily cap"
		}
		day += usd
		return ""
	}}
	opts, err := canaryScheduleOptions(d, ap)
	if err != nil {
		t.Fatal(err)
	}
	if why := opts.Allow(0); why != "" {
		t.Fatalf("first look refused: %s", why)
	}
	if why := opts.Allow(6); why == "" {
		t.Fatalf("confirmation admitted: schedule day total would be %.2f past its 10.00 cap", day+6)
	}
	// QC2-15-05: a definition whose per-run cap grew after the check.
	d.Budget.PerRunUSD = 12
	if _, err := canaryScheduleOptions(d, ap); err == nil {
		t.Fatal("a canary with a grown per-run cap passed the approval")
	}
}

// TestCanaryRejectsNonFiniteCaps is QC2-15-09.
func TestCanaryRejectsNonFiniteCaps(t *testing.T) {
	for _, c := range []float64{math.NaN(), math.Inf(1)} {
		d := &canary.Definition{Name: "w", Budget: canary.Budget{PerRunUSD: 1, PerDayUSD: c}}
		if err := d.Validate(); err == nil {
			t.Errorf("per_day_usd %v accepted", c)
		}
	}
}

// TestScheduledLivePullHasNoJudge is QC2-15-06: the daemon's livescore-pull
// job never calls the paid judge, whose spend no cap or ledger would see.
func TestScheduledLivePullHasNoJudge(t *testing.T) {
	s := &livescore.Scorer{Judge: struct{ livescore.Judge }{}}
	if !unattendedScorer(s) || s.Judge != nil {
		t.Fatal("scheduled scorer kept its judge")
	}
}
