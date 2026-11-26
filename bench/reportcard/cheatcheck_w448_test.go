package reportcard

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestReportCardCheatCheckStates: the cheat rate row is not measured, no
// twins, running, or measured with its counts and interval per arm; without
// a lookup there is no row.
func TestReportCardCheatCheckStates(t *testing.T) {
	src := newSource()
	src.add("exp_s", "shop", []string{"a", "b"}, []fixRun{pass("a", "t1", 1, nil), pass("b", "t1", 1, nil)})
	src.add("exp_tw", "shop-twins", []string{"a", "b"}, []fixRun{
		pass("a", "t1-twin", 1, nil), fail("a", "t2-twin", 1, nil),
		fail("b", "t1-twin", 1, nil), fail("b", "t2-twin", 1, nil),
	})
	ev := abEval(schemas.EvaluationChild{Benchmark: "shop", ExperimentID: "exp_s", Status: "done"})
	ev.CreatedAt = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	child := func(status string) *schemas.Evaluation {
		c := abEval(schemas.EvaluationChild{Benchmark: "shop-twins", ExperimentID: "exp_tw", Status: "done"})
		c.ID, c.Status, c.CheatCheckOf, c.CreatedAt = "ev_cc", status, ev.ID, ev.CreatedAt.Add(time.Hour)
		return c
	}
	build := func(kids []*schemas.Evaluation, twins bool) *CheatCheck {
		t.Helper()
		opt := Options{CheatChecks: func(id string) []*schemas.Evaluation {
			if id != ev.ID {
				t.Fatalf("lookup of %s", id)
			}
			return kids
		}, HasTwins: func(b string) bool { return twins && b == "shop" }}
		rep, err := Build(context.Background(), src, ev, opt)
		if err != nil {
			t.Fatal(err)
		}
		return rep.CheatCheck
	}

	if rep, _ := Build(context.Background(), src, ev, Options{}); rep.CheatCheck != nil {
		t.Fatalf("no lookup, row %+v", rep.CheatCheck)
	}
	if cc := build(nil, false); cc == nil || cc.State != CheatNoTwins {
		t.Fatalf("no twins: %+v", cc)
	}
	if cc := build(nil, true); cc == nil || cc.State != CheatNotMeasured {
		t.Fatalf("not measured: %+v", cc)
	}
	if cc := build([]*schemas.Evaluation{child(StatusRunning)}, true); cc == nil || cc.State != CheatRunning {
		t.Fatalf("running: %+v", cc)
	}
	cc := build([]*schemas.Evaluation{child(StatusDone)}, true)
	if cc == nil || cc.State != CheatMeasured || cc.Evaluation != "ev_cc" {
		t.Fatalf("measured: %+v", cc)
	}
	a, b := cc.PerArm["A"], cc.PerArm["B"]
	if a.Passed != 1 || a.Runs != 2 || a.Rate != 0.5 || len(a.PassRuns) != 1 || !(a.Low < 0.5 && a.High > 0.5) {
		t.Fatalf("arm A %+v", a)
	}
	if b.Passed != 0 || b.Runs != 2 || b.Rate != 0 || len(b.PassRuns) != 0 || b.High <= 0 {
		t.Fatalf("arm B %+v", b)
	}
	// The cheat check itself is never a score of the parent report.
	rep, _ := Build(context.Background(), src, ev, Options{CheatChecks: func(string) []*schemas.Evaluation { return []*schemas.Evaluation{child(StatusDone)} }})
	for _, bm := range rep.Benchmarks {
		if bm.Benchmark == "shop-twins" {
			t.Fatalf("twin benchmark joined the report: %+v", rep.Benchmarks)
		}
	}
}
