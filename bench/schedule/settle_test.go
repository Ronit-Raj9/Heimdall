package schedule

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/alerts"
)

func TestSettleScheduledEvaluation(t *testing.T) {
	clock := newFakeClock(start)
	n := &fakeNotifier{}
	d, state := newTestDaemon(t, Schedule{Name: "nightly", Cron: "0 2 * * *", Timezone: "UTC", MaxUSDPerDay: 10, Evaluation: "ev_spec"}, 2, &fakeRunner{clock: clock}, n)
	rec, err := d.Settle(context.Background(), "nightly", start, &Result{EvaluationID: "ev_abc", Decision: "regressed", Policy: "block", CostUSD: 1.5, Runs: 16,
		Summary: "codex v7 scored 61% (-6 points vs previous run)"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusFailed {
		t.Fatalf("status %s", rec.Status)
	}
	if got := n.events(); !slices.Equal(got, []string{alerts.EventVerdictRegressed, alerts.EventPolicyBlock}) {
		t.Fatalf("events %v", got)
	}
	if n.sent[0].Link != "/evaluations/ev_abc" || n.sent[0].Message != "codex v7 scored 61% (-6 points vs previous run)" {
		t.Fatalf("payload %+v", n.sent[0])
	}
	b, err := os.ReadFile(filepath.Join(state, RecordsFile))
	if err != nil || !strings.Contains(string(b), `"evaluation_id":"ev_abc"`) || !strings.Contains(string(b), `"status":"failed"`) {
		t.Fatalf("record not written: %s %v", b, err)
	}
	if _, err := d.Settle(context.Background(), "other", start, &Result{}); err == nil {
		t.Fatal("unknown schedule settled")
	}
	ok, err := d.Settle(context.Background(), "nightly", start, &Result{EvaluationID: "ev_def", Decision: "inconclusive"})
	if err != nil || ok.Status != StatusOK || len(n.events()) != 2 {
		t.Fatalf("clean outcome %+v %v %v", ok, err, n.events())
	}
}
