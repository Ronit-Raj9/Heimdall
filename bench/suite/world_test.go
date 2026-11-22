package suite

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLintWorldTask(t *testing.T) {
	s := &schemas.Suite{Root: "/s", Services: []schemas.ServiceSpec{{ID: "shop"}}}
	task := &schemas.Task{ID: "t1", Dir: "/s/tasks/t1", Sessions: []schemas.SessionSpec{{ID: "a"}, {ID: "b"}},
		World: &schemas.WorldSpec{StartDate: "2026-01-05", SessionGap: "7d", Events: []schemas.WorldEvent{{AfterSession: 1, Service: "shop", Action: "price_change"}}}}
	if p := lintWorldTask(s, task); len(p) != 0 {
		t.Fatalf("good world: %+v", p)
	}
	task.World.Events[0].Service = "bank"
	if p := lintWorldTask(s, task); len(p) != 1 || p[0].Severity != SeverityError {
		t.Fatalf("unknown service: %+v", p)
	}
	if p := lintWorldTask(s, &schemas.Task{}); p != nil {
		t.Fatalf("no world: %+v", p)
	}
}

// Z17: the effective run clock is part of the task's fixtures hash, and a
// bad clock is a lint error.
func TestClockHashAndLint(t *testing.T) {
	s := &schemas.Suite{Root: t.TempDir()}
	task := &schemas.Task{ID: "t", Dir: s.Root}
	p, err := scenarioParts(s, task)
	if err != nil || p.fixtures["clock"] != "" {
		t.Fatalf("no clock: %v %v", p.fixtures, err)
	}
	s.Clock = &schemas.ClockSpec{Start: "2026-03-14"}
	p1, _ := scenarioParts(s, task)
	task.Clock = &schemas.ClockSpec{Start: "2026-03-15"}
	p2, _ := scenarioParts(s, task)
	if p1.fixtures["clock"] == "" || p1.fixtures["clock"] == p2.fixtures["clock"] {
		t.Fatalf("clock hash %q %q", p1.fixtures["clock"], p2.fixtures["clock"])
	}
	task.Clock = &schemas.ClockSpec{Start: "next week"}
	if len(lintClockTask(s, task)) != 1 {
		t.Fatal("bad clock not reported")
	}
}
