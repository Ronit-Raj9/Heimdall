package handlers

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// p02-4: First look never picks a benchmark without a lock: the planner
// refuses it, so the evaluation would fail at once.
func TestFirstLookSkipsUnlockedSuite(t *testing.T) {
	old := evalCompat
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		return true, nil, ""
	}
	defer func() { evalCompat = old }()
	fast := []*schemas.Task{{ID: "a", Timeouts: schemas.TaskTimeouts{AgentS: 10}}}
	slow := []*schemas.Task{{ID: "b", Timeouts: schemas.TaskTimeouts{AgentS: 300}}}
	suites := &fakeSuites{ids: []string{"unlocked", "locked"}, suites: map[string]*schemas.Suite{
		"unlocked": {ID: "unlocked", TaskIDs: []string{"a"}, Tasks: fast},
		"locked":   {ID: "locked", TaskIDs: []string{"b"}, Tasks: slow, Lock: w4b05Locked("b")},
	}}
	h := &connectHandler{env: OrgEnv{Mode: schemas.Mode("live"), Services: Services{Suites: suites}}}
	cfg := &schemas.AgentConfig{ID: "bot"}
	p, err := h.pick(context.Background(), cfg)
	if err != nil || p.Benchmark != "locked" {
		t.Fatalf("pick %+v %v, want locked (the faster benchmark has no lock)", p, err)
	}
	seen := false
	for _, b := range h.benchmarkInfos(context.Background(), cfg) {
		if b.ID == "unlocked" {
			seen = true
			if b.Compatible || b.Reason != "it is not ready yet: run its readiness check on the benchmark page" {
				t.Errorf("unlocked benchmark listed as %+v", b)
			}
		}
	}
	if !seen {
		t.Error("the unlocked benchmark is not listed")
	}
	if p.Note != "First look: 1 tasks. Too small for a verdict; run the full benchmark for one." {
		t.Errorf("note %q", p.Note)
	}
}

// w4b05Locked is a lock that admits the given tasks, for fixtures of
// benchmarks First look may pick (an unlocked suite is never picked).
func w4b05Locked(tasks ...string) *schemas.SuiteLock {
	l := &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{}}
	for _, id := range tasks {
		l.Tasks[id] = schemas.TaskLock{Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}}
	}
	return l
}
