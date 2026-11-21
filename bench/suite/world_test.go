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
