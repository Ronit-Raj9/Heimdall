package suite

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: GPU requests in lint (gpu.go).

func TestLintGPUTask(t *testing.T) {
	s := &schemas.Suite{Root: "/s", Track: schemas.TrackMLEngineering}
	task := func(r *schemas.Resources, team bool) *schemas.Task {
		tk := &schemas.Task{ID: "t", Dir: "/s/tasks/t", Resources: r}
		if team {
			tk.Team = []schemas.TeamMember{{ID: "a"}, {ID: "b"}}
		}
		return tk
	}
	gpu := func(n, mem int) *schemas.Resources {
		return &schemas.Resources{GPU: &schemas.GPURequest{Count: n, MinVRAMMB: mem}}
	}
	cases := []struct {
		t    *schemas.Task
		sev  string
		want string
	}{
		{task(gpu(1, 2048), false), "", ""},
		{task(nil, false), SeverityWarning, "task runs without a GPU"},
		{task(gpu(0, 0), false), SeverityError, "1 to 8 whole GPUs"},
		{task(gpu(9, 0), false), SeverityError, "1 to 8 whole GPUs"},
		{task(gpu(1, -1), false), SeverityError, "is negative"},
		{task(gpu(1, 0), true), SeverityError, "team"},
	}
	for i, c := range cases {
		got := lintGPUTask(s, c.t)
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%d: %v", i, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Severity != c.sev || !strings.Contains(got[0].Message, c.want) {
			t.Errorf("%d: %v, want %s %q", i, got, c.sev, c.want)
		}
	}
	if got := lintGPUTask(&schemas.Suite{Root: "/s"}, task(nil, false)); len(got) != 0 {
		t.Errorf("a task of another track needs no GPU: %v", got)
	}
}

func TestHoldGPURefusesWithoutPool(t *testing.T) {
	a := &admitter{t: &schemas.Task{ID: "t", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}}
	if _, err := a.holdGPU(t.Context()); err == nil || !strings.Contains(err.Error(), "no GPU pool") {
		t.Errorf("admission without a pool: %v", err)
	}
	plan := &schemas.RunPlan{MemGB: 1, CPUs: 1, WallS: 120}
	a.gpuControlBudget(plan)
	if plan.MemGB != 1 {
		t.Error("no GPU held: the budget stays")
	}
	a.gpu = &schemas.GPUAssignment{}
	a.gpuControlBudget(plan)
	if plan.MemGB != gpuControlMemGB || plan.WallS != gpuControlWallS || plan.CPUs != gpuControlCPUs {
		t.Errorf("GPU control budget %+v", plan)
	}
}
