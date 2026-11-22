package engine

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func streamManifest(st *schemas.StreamSpec) *schemas.Manifest {
	return &schemas.Manifest{ExpID: "e_s", Experiment: schemas.Experiment{Seed: 7, Stream: st, Arms: map[string]schemas.ArmSpec{"A": {}, "B": {}}}}
}

func streamSlots() []schemas.Slot {
	var out []schemas.Slot
	for _, task := range []string{"t1", "t2", "t3"} {
		for _, arm := range []string{"A", "B"} {
			out = append(out, schemas.Slot{SlotID: arm + task, Arm: arm, Task: task, Condition: "clean", Trial: 1, Counted: true, OrderIndex: len(out)})
		}
	}
	out = append(out, schemas.Slot{SlotID: "compat", Arm: CompatArm, Task: "t1", Condition: "clean", Trial: 1, OrderIndex: len(out)})
	return out
}

func TestPlanStreamChains(t *testing.T) {
	conds := []condPlan{{id: "clean", tasks: []string{"t1", "t2", "t3"}, trials: 1}}
	if got := planStream(streamManifest(nil), conds, streamSlots()); got[1].StreamIndex != 0 {
		t.Fatal("no stream must leave slots alone")
	}
	slots := planStream(streamManifest(&schemas.StreamSpec{Memory: "carry", Replay: 1}), conds, streamSlots())
	by := map[string]schemas.Slot{}
	for _, s := range slots {
		by[s.SlotID] = s
	}
	if s := by["At1"]; s.StreamIndex != 1 || s.StreamPrev != "" {
		t.Fatalf("head %+v", s)
	}
	if s := by["At3"]; s.StreamIndex != 3 || s.StreamPrev != "At2" {
		t.Fatalf("third %+v", s)
	}
	if s := by["Bt2"]; s.StreamPrev != "Bt1" {
		t.Fatalf("arm B chain %+v", s)
	}
	if by["compat"].StreamIndex != 0 {
		t.Fatal("compatibility slot must stay outside chains")
	}
	if len(slots) != len(streamSlots())+2 {
		t.Fatalf("want one replay per chain, got %d slots", len(slots))
	}
	for _, s := range slots[len(slots)-2:] {
		if !s.StreamReplay || s.Counted || s.Task != "t1" || s.StreamIndex != 4 || by[s.StreamPrev].Task != "t3" {
			t.Fatalf("replay %+v", s)
		}
	}
}

func TestPlanStreamSeededOrderShared(t *testing.T) {
	conds := []condPlan{{id: "clean", tasks: []string{"t1", "t2", "t3"}, trials: 1}}
	slots := planStream(streamManifest(&schemas.StreamSpec{Memory: "carry", Order: "seeded"}), conds, streamSlots())
	order := map[string]map[int]string{}
	for _, s := range slots {
		if s.StreamIndex == 0 {
			continue
		}
		if order[s.Arm] == nil {
			order[s.Arm] = map[int]string{}
		}
		order[s.Arm][s.StreamIndex] = s.Task
	}
	for i := 1; i <= 3; i++ {
		if order["A"][i] != order["B"][i] {
			t.Fatalf("arms see different orders: %v", order)
		}
	}
}

func TestCheckStream(t *testing.T) {
	suite := &schemas.Suite{Tasks: []*schemas.Task{{ID: "t1"}, {ID: "team", Team: []schemas.TeamMember{{ID: "a"}}}}}
	e := &schemas.Experiment{Arms: map[string]schemas.ArmSpec{"A": {}}, Stream: &schemas.StreamSpec{Memory: "carry"}}
	if err := checkStream(e, suite, []condPlan{{id: "clean", tasks: []string{"t1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := checkStream(e, suite, []condPlan{{id: "clean", tasks: []string{"team"}}}); err == nil {
		t.Fatal("team task must be refused")
	}
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive}
	if err := checkStream(e, suite, []condPlan{{id: "clean", tasks: []string{"t1"}}}); err == nil {
		t.Fatal("adaptive trials must be refused")
	}
}

func TestSlotQueueStreamOrder(t *testing.T) {
	q := newSlotQueue([]schemas.Slot{
		{SlotID: "a1", OrderIndex: 0, StreamIndex: 1},
		{SlotID: "a2", OrderIndex: 1, StreamIndex: 2, StreamPrev: "a1"},
		{SlotID: "x", OrderIndex: 2},
	})
	ctx := context.Background()
	s, _ := q.next(ctx)
	if s.SlotID != "a1" {
		t.Fatalf("first %s", s.SlotID)
	}
	s, _ = q.next(ctx)
	if s.SlotID != "x" {
		t.Fatalf("a2 must wait for a1, got %s", s.SlotID)
	}
	if _, ok := q.takeWhere(nil); ok {
		t.Fatal("a stream slot must never go to a remote worker")
	}
	got := make(chan string, 1)
	go func() {
		s, ok := q.next(ctx)
		if ok {
			got <- s.SlotID
		}
	}()
	select {
	case id := <-got:
		t.Fatalf("a2 ran before a1 finished: %s", id)
	case <-time.After(50 * time.Millisecond):
	}
	q.markDone("x")
	q.markDone("a1")
	select {
	case id := <-got:
		if id != "a2" {
			t.Fatalf("got %s", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a2 never released")
	}
	q.markDone("a2")
	if _, ok := q.next(ctx); ok {
		t.Fatal("queue must be empty")
	}
}

func TestSlotQueueOrphanChainReleased(t *testing.T) {
	q := newSlotQueue([]schemas.Slot{{SlotID: "b2", StreamIndex: 2, StreamPrev: "gone"}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s, ok := q.next(ctx); !ok || s.SlotID != "b2" {
		t.Fatal("a slot whose previous slot never reaches the queue must not hang")
	}
}

func TestBudgetEnv(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	rc := &schemas.RunContext{Experiment: &schemas.Experiment{}, Agent: &schemas.AgentConfig{Budgets: schemas.Budgets{USD: 1.5, WallS: 600, MaxToolCalls: 40}}}
	if env := budgetEnv(rc, &schemas.RunPlan{}, now); env != nil {
		t.Fatalf("no disclosure: %v", env)
	}
	rc.Experiment.Budget.Disclose = true
	env := budgetEnv(rc, &schemas.RunPlan{WallS: 300}, now)
	if env[schemas.EnvBudgetUSD] != "1.5000" || env[schemas.EnvDeadline] != "2026-11-22T10:05:00Z" || env[schemas.EnvMaxToolCalls] != "40" {
		t.Fatalf("env %v", env)
	}
	rc.Agent.Budgets = schemas.Budgets{}
	rc.Experiment.Budget.TotalUSD = 10
	rc.Manifest = &schemas.Manifest{Slots: make([]schemas.Slot, 4)}
	env = budgetEnv(rc, &schemas.RunPlan{}, now)
	if env[schemas.EnvBudgetUSD] != "2.5000" || env[schemas.EnvDeadline] != "" {
		t.Fatalf("share of the total: %v", env)
	}
}

func TestProcessRule(t *testing.T) {
	rule := &schemas.ProcessFault{AtModelCall: 2}
	st := &runState{rc: &schemas.RunContext{Probe: &schemas.ProbeSpec{Layer: schemas.LayerProcess, Apply: schemas.ProbeApply{Process: rule}}}}
	if processRule(st, nil) != rule {
		t.Fatal("single session run must match")
	}
	s2 := &schemas.SessionSpec{ID: "s2"}
	st.rc.SessionIndex = 1
	if processRule(st, s2) != nil {
		t.Fatal("empty session means the first session only")
	}
	rule.Session = "s2"
	if processRule(st, s2) != rule {
		t.Fatal("named session must match")
	}
	st.killed = true
	if processRule(st, s2) != nil {
		t.Fatal("a run is killed once")
	}
	st.killed = false
	st.rc.Probe.Layer = schemas.LayerGateway
	if processRule(st, s2) != nil {
		t.Fatal("other layers never kill")
	}
}

func TestStreamCarriesEnv(t *testing.T) {
	rc := &schemas.RunContext{Task: &schemas.Task{ID: "t"}, Slot: &schemas.Slot{Arm: "A", StreamIndex: 2},
		Experiment: &schemas.Experiment{Stream: &schemas.StreamSpec{Memory: "carry", FreshArm: "B"}}}
	env, err := scenarioEnv(rc)
	if err != nil || env["BENCH_MEMORY_DIR"] != "/memory" {
		t.Fatalf("carry run env %v %v", env, err)
	}
	rc.Slot.Arm = "B"
	env, _ = scenarioEnv(rc)
	if _, ok := env["BENCH_MEMORY_DIR"]; ok {
		t.Fatal("fresh arm must not get the memory folder")
	}
}

func TestWorldDateEnv(t *testing.T) {
	task := &schemas.Task{ID: "t", Sessions: []schemas.SessionSpec{{ID: "a"}, {ID: "b"}}, World: &schemas.WorldSpec{StartDate: "2026-01-05", SessionGap: "7d"}}
	rc := &schemas.RunContext{Task: task, Session: &task.Sessions[1], SessionIndex: 1}
	env, err := scenarioEnv(rc)
	if err != nil || env[schemas.EnvDate] != "2026-01-12T00:00:00Z" {
		t.Fatalf("env %v %v", env, err)
	}
}

// Z17: the agent sees the task's run clock.
func TestClockEnvAgent(t *testing.T) {
	rc := &schemas.RunContext{Task: &schemas.Task{ID: "t", Clock: &schemas.ClockSpec{Start: "2026-03-14T09:00:00Z", Rate: 2}}}
	env, err := scenarioEnv(rc)
	if err != nil || env[schemas.EnvNow] != "2026-03-14T09:00:00Z" || env[schemas.EnvClockRate] != "2" {
		t.Fatalf("env %v %v", env, err)
	}
}
