package main

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schedule"
)

// TestWatchBudgetStopsSecondModel: with two new models in one check and a
// daily quota of two, the second starts only while the spending the daemon
// approved for this trigger covers both budgets. Here it covers one
// (approved 5 USD, 4 USD per model): the second is recorded failed with
// the plain reason and named in the check's summary, and nothing over the
// approval is launched.
func TestWatchBudgetStopsSecondModel(t *testing.T) {
	f := newWatchFixture(t)
	f.s.NewModels.MaxPerDay = 2
	f.s.Approved = &schedule.Approval{Limited: true, CapUSD: 5}
	f.gw.set()
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	t0 := watchNow()
	f.gw.set(newestModel{Name: "gpt-old", Provider: "openai", Created: t0.Add(-2 * time.Hour).Unix()},
		newestModel{Name: "gpt-new", Provider: "openai", Created: t0.Add(-time.Hour).Unix()})
	res, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.launched) != 1 || f.launched[0].Arms[1].Model != "gpt-new" || res.CostUSD != 4 {
		t.Fatalf("launched %d, result %+v", len(f.launched), res)
	}
	const why = "the spending approved for this check is used up"
	it := f.state().Seen["openai/gpt-old"]
	if it.State != schedule.WatchFailed || !strings.Contains(it.Reason, why) {
		t.Fatalf("second item %+v", it)
	}
	if !strings.Contains(res.Summary, "New model openai/gpt-old found; not started: "+why) {
		t.Fatalf("summary %q", res.Summary)
	}
	// Run now alone never starts a model the watch already recorded; the
	// reason names the retry action that does.
	if !strings.Contains(it.Reason, "press Try again for this model on the Gateway page, then Run now") || strings.Contains(it.Reason, "; press Run now") {
		t.Fatalf("reason %q does not name the retry action", it.Reason)
	}
}
