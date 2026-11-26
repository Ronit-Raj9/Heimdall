package improve_test

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

// spendExec is the scripted executor that adds up every cost it reports.
type spendExec struct {
	*improvetest.Exec
	mu    sync.Mutex
	spent float64
}

func (x *spendExec) Score(ctx context.Context, req improve.ScoreRequest) (*improve.ScoreResult, error) {
	res, err := x.Exec.Score(ctx, req)
	if res != nil {
		x.mu.Lock()
		x.spent += res.CostUSD
		x.mu.Unlock()
	}
	return res, err
}

func (x *spendExec) Improve(ctx context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	res, err := x.Exec.Improve(ctx, req)
	if res != nil {
		x.mu.Lock()
		x.spent += res.CostUSD
		x.mu.Unlock()
	}
	return res, err
}

func studySpec(total float64) *improve.Spec {
	return w4Spec(func(s *improve.Spec) {
		s.Rounds = 2
		s.Replicate = improve.ReplicationPlan{Loops: 3}
		s.Budget.TotalUSD = total
		s.Improver.BudgetUSD = 0.2
	})
}

// Lead decision on the study limit: the limit is the total for the whole
// study; each loop's hard stop is an equal share (rounded down to the
// cent); a 3-loop study never spends more than its total, for tight and
// loose totals alike.
func TestStudyNeverSpendsMoreThanItsTotal(t *testing.T) {
	for _, total := range []float64{1, 2.5, 4, 8, 1000} {
		x := &spendExec{Exec: &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, ImpUSD: 0.05}}
		r := newRunner(t, x.Exec)
		r.Exec = x
		st, err := r.CreateStudy(context.Background(), studySpec(total), "ana")
		if err != nil {
			t.Fatalf("total %v: %v", total, err)
		}
		st, err = r.RunStudy(context.Background(), st.ID)
		if err != nil {
			t.Fatalf("total %v: %v", total, err)
		}
		if x.spent > total+1e-9 {
			t.Errorf("total %v: the study spent %.4f (%s: %s)", total, x.spent, st.Status, st.Reason)
		}
		share := math.Floor(total*100/3) / 100
		for _, id := range st.Loops {
			rec, err := r.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Spec.Budget.TotalUSD != share || rec.StudyLimitUSD != total {
				t.Errorf("total %v: loop limit %v (want %v), study limit %v", total, rec.Spec.Budget.TotalUSD, share, rec.StudyLimitUSD)
			}
		}
		if total == 1000 && st.Status != improve.StatusDone {
			t.Errorf("a loose total did not finish: %s %s", st.Status, st.Reason)
		}
		if v := r.ViewStudy(st); v.Limit != improve.StudyLimitText(total, 3) {
			t.Errorf("view limit %q", v.Limit)
		}
	}
	if got := improve.StudyLimitText(10, 3); got != "Total for 3 loops: at most $10.00 ($3.33 for each loop)" {
		t.Errorf("limit text %q", got)
	}
}

// Create refuses a total whose per-loop share is below one improver round.
func TestStudyRefusesTooSmallTotal(t *testing.T) {
	r := newRunner(t, &improvetest.Exec{Behavior: improvetest.Honest, NTasks: 120})
	_, err := r.CreateStudy(context.Background(), studySpec(0.5), "ana")
	want := "A total of $0.50 for 3 loops leaves $0.16 for each loop, less than one improver round may spend ($0.20). Raise the total or use fewer loops."
	if err == nil || err.Error() != want {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(want, "each loop") {
		t.Fatal("unreachable")
	}
}

// The estimate of a study counts L loops and one study final, shows both
// limits, keeps the high figure within the total and refuses a share below
// one improver round.
func TestStudyEstimate(t *testing.T) {
	in := improve.EstimateInput{Pool: 120, Splits: improve.SplitCounts{Practice: 48, Gate: 48, HeldOut: 24}, PerRunUSD: 0.02}
	one := w4Spec(func(s *improve.Spec) { s.Rounds = 2; s.Budget.TotalUSD = 300 })
	in.Spec = one
	single := improve.DefaultEstimator.Estimate(in)
	st := studySpec(300)
	in.Spec = st
	est := improve.DefaultEstimator.Estimate(in)
	if est.Loops != 3 || est.USD.LoopLimit != 100 || est.USD.LimitText != "Total for 3 loops: at most $300.00 ($100.00 for each loop)" {
		t.Fatalf("study estimate %+v", est.USD)
	}
	loopRuns := single.Runs.Total - single.Runs.Final
	if est.Runs.Final != 4*24*st.Sealed.Trials || est.Runs.Total != 3*loopRuns+est.Runs.Final {
		t.Errorf("runs %+v, one loop without its final %d", est.Runs, loopRuns)
	}
	if est.USD.Expected <= single.USD.Expected || est.USD.High > 300 {
		t.Errorf("money %+v, one run %+v", est.USD, single.USD)
	}
	in.Spec = studySpec(0.5)
	small := improve.DefaultEstimator.Estimate(in)
	found := false
	for _, n := range small.Refusals {
		found = found || n.Code == "loop_share_small"
	}
	if small.OK || !found || small.USD.High > 0.5 {
		t.Errorf("small total: %+v", small)
	}
}
