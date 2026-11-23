package handlers

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// N242: Connect "Prove it" and First look never pick an Add benchmark
// staging draft or a benchmark without a ready task.
func TestConnectPickSkipsStagingAndUnready(t *testing.T) {
	old := evalCompat
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		return true, nil, ""
	}
	defer func() { evalCompat = old }()
	fast := []*schemas.Task{{ID: "a", Timeouts: schemas.TaskTimeouts{AgentS: 10}}}
	slow := []*schemas.Task{{ID: "b", Timeouts: schemas.TaskTimeouts{AgentS: 300}}}
	draft := "draft-20261122-204611-197a49"
	suites := &fakeSuites{ids: []string{draft, "unready", "realqa"}, suites: map[string]*schemas.Suite{
		draft: {ID: draft, TaskIDs: []string{"a"}, Tasks: fast},
		// Locked, but its one task never passed the readiness checks.
		"unready": {ID: "unready", TaskIDs: []string{"a"}, Tasks: fast, Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"a": {Status: "pending"}}}},
		"realqa":  {ID: "realqa", TaskIDs: []string{"b"}, Tasks: slow},
	}}
	h := &connectHandler{env: OrgEnv{Mode: schemas.Mode("live"), Services: Services{Suites: suites}}}
	cfg := &schemas.AgentConfig{ID: "bot"}
	p, err := h.pick(context.Background(), cfg)
	if err != nil || p.Benchmark != "realqa" {
		t.Fatalf("pick %+v %v, want realqa (the draft and the unready benchmark are faster but never picked)", p, err)
	}
	for _, b := range h.benchmarkInfos(context.Background(), cfg) {
		if b.ID == draft {
			t.Errorf("the staging draft is listed: %+v", b)
		}
		if b.ID == "unready" && b.Tasks != 0 {
			t.Errorf("unready benchmark counts %d tasks, want 0 ready", b.Tasks)
		}
	}
}
