package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: GPU scheduling and plan-time refusals (gpu.go).

func gpuTestSuite() *schemas.Suite {
	return &schemas.Suite{ID: "s", Tasks: []*schemas.Task{
		{ID: "cpu"},
		{ID: "small", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1, MinVRAMMB: 2048}}},
		{ID: "big", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1, MinVRAMMB: 16000}}},
		{ID: "two", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 2}}},
	}}
}

func gtxPool(t *testing.T) *gpu.Pool {
	t.Helper()
	p, err := gpu.NewPool("cdi", []schemas.GPUInfo{{Index: 0, UUID: "GPU-a", Name: "NVIDIA GeForce GTX 1650", MemoryMB: 4096, ComputeCap: "7.5"}}, -1, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckGPURefusesWithTheReason(t *testing.T) {
	s := gpuTestSuite()
	conds := func(ids ...string) []condPlan { return []condPlan{{id: "clean", tasks: ids, trials: 1}} }
	with := &Planner{reg: &schemas.Registry{GPUs: gtxPool(t)}}
	without := &Planner{reg: &schemas.Registry{}}
	cases := []struct {
		p    *Planner
		e    *schemas.Experiment
		ids  []string
		want string // "" means planned
	}{
		{without, nil, []string{"cpu"}, ""},
		{with, nil, []string{"cpu", "small"}, ""},
		{without, nil, []string{"small"}, "task small needs a GPU but GPU tasks are off on this machine"},
		{with, nil, []string{"big"}, "task big needs 1 GPU(s) with at least 16000 MiB each, but GPU 0 NVIDIA GeForce GTX 1650 has 4096 MiB"},
		{with, nil, []string{"two"}, "task two needs 2 GPU(s), but this machine has 1 GPU(s)"},
		{with, &schemas.Experiment{Environment: &schemas.EnvironmentChoice{Driver: "kubernetes"}}, []string{"small"}, "runs on the Docker driver only, not kubernetes"},
	}
	for _, c := range cases {
		err := c.p.checkGPU(c.e, s, conds(c.ids...))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: refused: %v", c.ids, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: err %v, want %q", c.ids, err, c.want)
		}
	}
	skip := &Planner{reg: &schemas.Registry{}}
	WithoutGPUCheck()(skip)
	if err := skip.checkGPU(nil, s, conds("big")); err != nil {
		t.Errorf("a worker replan must not run the GPU check: %v", err)
	}
	team := &schemas.Suite{Tasks: []*schemas.Task{{ID: "t", Team: []schemas.TeamMember{{ID: "a"}, {ID: "b"}}, Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}}}
	if err := with.checkGPU(nil, team, conds("t")); err == nil || !strings.Contains(err.Error(), "team") {
		t.Errorf("GPU team task: %v", err)
	}
	// Audit 2-B-03: a team declared by the arm, on a GPU task with no team of
	// its own, must be refused the same way.
	solo := &schemas.Suite{Tasks: []*schemas.Task{{ID: "t", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}}}
	armTeam := schemas.ArmManifest{Config: schemas.AgentConfig{Team: []schemas.TeamMember{{ID: "a"}, {ID: "b"}}}}
	if err := with.checkGPU(nil, solo, conds("t"), schemas.ArmManifest{}, armTeam); err == nil || !strings.Contains(err.Error(), "team") {
		t.Errorf("GPU task with an arm team: %v", err)
	}
	if err := with.checkGPU(nil, solo, conds("t"), schemas.ArmManifest{}); err != nil {
		t.Errorf("GPU task with a single arm: %v", err)
	}
}

func TestSlotQueueKinds(t *testing.T) {
	s := gpuTestSuite()
	q := newSlotQueue([]schemas.Slot{{SlotID: "1", Task: "small", OrderIndex: 0}, {SlotID: "2", Task: "cpu", OrderIndex: 1}, {SlotID: "3", Task: "small", OrderIndex: 2}})
	isGPU := func(sl *schemas.Slot) bool { return slotNeedsGPU(s, sl) }
	isCPU := func(sl *schemas.Slot) bool { return !slotNeedsGPU(s, sl) }
	ctx := context.Background()
	if sl, ok := q.nextWhere(ctx, isCPU, false); !ok || sl.SlotID != "2" {
		t.Fatalf("a CPU worker must skip GPU slots: %v %v", sl, ok)
	}
	if _, ok := q.nextWhere(ctx, isCPU, false); ok {
		t.Fatal("a CPU worker took a GPU slot")
	}
	if sl, ok := q.takeWhere(isGPU); !ok || sl.SlotID != "1" {
		t.Fatalf("remote take of a GPU slot: %v %v", sl, ok)
	}
	// With a remote slot out and waitOthers set, a CPU worker waits; when the
	// remote slot finishes and only a GPU slot is left, it still waits for a
	// remote GPU worker, until that one takes it.
	done := make(chan bool, 1)
	go func() {
		_, ok := q.nextWhere(ctx, isCPU, true)
		done <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	q.finish()
	select {
	case <-done:
		t.Fatal("the CPU worker left while a GPU slot was pending for remote workers")
	case <-time.After(30 * time.Millisecond):
	}
	if sl, ok := q.takeWhere(isGPU); !ok || sl.SlotID != "3" {
		t.Fatalf("second GPU slot: %v %v", sl, ok)
	}
	q.finish()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("no CPU slot was left")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the CPU worker never left")
	}
	if gpuSlots(&schemas.Manifest{Slots: []schemas.Slot{{Task: "small"}, {Task: "cpu"}, {Task: "two"}}}, s) != 2 {
		t.Error("gpuSlots")
	}
}

func TestRemoteGPUAccept(t *testing.T) {
	s := gpuTestSuite()
	gtx := []schemas.GPUInfo{{UUID: "GPU-a", MemoryMB: 4096}}
	free := remoteGPUAccept(gtx, nil)
	busy := remoteGPUAccept(gtx, map[string]bool{"GPU-a": true})
	none := remoteGPUAccept(nil, nil)
	for _, c := range []struct {
		accept func(*schemas.Task) bool
		task   string
		want   bool
	}{
		{free, "cpu", true}, {free, "small", true}, {free, "big", false}, {free, "two", false},
		{busy, "cpu", true}, {busy, "small", false},
		{none, "cpu", true}, {none, "small", false},
	} {
		if got := c.accept(s.TaskByID(c.task)); got != c.want {
			t.Errorf("accept %s = %v, want %v", c.task, got, c.want)
		}
	}
	if !free(nil) {
		t.Error("a slot whose task is unknown here carries no GPU request")
	}
}

func TestGPUWorkersAndCapabilities(t *testing.T) {
	pool := gtxPool(t)
	reg := &schemas.Registry{GPUs: pool}
	if gpuWorkers(reg, 5) != 1 || gpuWorkers(reg, 0) != 0 || gpuWorkers(&schemas.Registry{}, 5) != 0 {
		t.Error("one GPU worker per GPU, none without GPU slots or a pool")
	}
	if c := WorkerCapabilities(reg); c == nil || len(c.GPUs) != 1 {
		t.Errorf("capabilities %+v", c)
	}
	if WorkerCapabilities(&schemas.Registry{}) != nil {
		t.Error("a worker without a pool registers no capabilities")
	}
}

func TestHoldGPU(t *testing.T) {
	pool := gtxPool(t)
	p := &Pipeline{reg: &schemas.Registry{GPUs: pool}, logger: schemas.NoopLogger{}}
	s := gpuTestSuite()
	in, release := p.holdGPU(context.Background(), SlotInput{Suite: s, Slot: &schemas.Slot{Task: "small"}})
	if in.GPU == nil || pool.Busy() != 1 {
		t.Fatalf("holdGPU: %+v busy %d", in.GPU, pool.Busy())
	}
	release()
	if pool.Busy() != 0 {
		t.Error("release")
	}
	in, release = p.holdGPU(context.Background(), SlotInput{Suite: s, Slot: &schemas.Slot{Task: "cpu"}})
	release()
	if in.GPU != nil {
		t.Error("a CPU task holds no GPU")
	}
	held := &schemas.GPUAssignment{Mode: "cdi"}
	in, release = p.holdGPU(context.Background(), SlotInput{Suite: s, Slot: &schemas.Slot{Task: "small"}, GPU: held})
	release()
	if in.GPU != held || pool.Busy() != 0 {
		t.Error("an input that holds GPUs keeps them and takes no more")
	}
}

func TestRunGPUSlotHoldsOneGPUPerRun(t *testing.T) {
	pool := gtxPool(t)
	r := &schedRun{s: &Scheduler{reg: &schemas.Registry{GPUs: pool}}, suite: gpuTestSuite()}
	a, release, err := pool.Acquire(context.Background(), &schemas.GPURequest{Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	r.gpuMu.Lock()
	r.gpus = map[string]*schemas.GPUAssignment{"x": a}
	r.gpuMu.Unlock()
	if r.gpuFor("x") != a || r.gpuFor("y") != nil {
		t.Error("gpuFor")
	}
	if in := r.input(&schemas.Slot{SlotID: "x"}, 1); in.GPU != a {
		t.Error("the slot input carries the GPU worker's assignment")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	got := make(chan struct{})
	go func() {
		defer wg.Done()
		_, r2, _ := pool.Acquire(context.Background(), &schemas.GPURequest{Count: 1})
		close(got)
		r2()
	}()
	select {
	case <-got:
		t.Fatal("a second run got the GPU while the first held it")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	wg.Wait()
}

func TestSchedulerRunsGPUSlotsOnGPUWorkers(t *testing.T) {
	b := newBench(t)
	b.suite.Tasks[0].Resources = &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment()); err == nil || !strings.Contains(err.Error(), "task t01 needs a GPU but GPU tasks are off") {
		t.Fatalf("plan without a GPU pool: %v", err)
	}
	pool := gtxPool(t)
	b.reg.GPUs = pool
	m := b.plan(t, testExperiment())
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 2})
	if err != nil || sum.Sealed != 4 {
		t.Fatalf("run: %+v %v", sum, err)
	}
	if pool.Busy() != 0 {
		t.Error("every GPU must be back in the pool after the run")
	}
	for _, slot := range m.Slots {
		recs, err := b.store.SlotRuns(context.Background(), m.ExpID, slot.SlotID)
		if err != nil || len(recs) != 1 {
			t.Fatalf("slot %s: %v %v", slot.SlotID, recs, err)
		}
		h := recs[0].Run.Hardware
		switch slot.Task {
		case "t01":
			if h == nil || len(h.GPUs) != 1 || h.GPUs[0].UUID != "GPU-a" || h.Mode != "cdi" || h.Devices[0] != "nvidia.com/gpu=GPU-a" {
				t.Errorf("GPU slot %s hardware %+v", slot.SlotID, h)
			}
		default:
			if h != nil {
				t.Errorf("CPU slot %s holds hardware %+v", slot.SlotID, h)
			}
		}
	}
	// A resume on a machine without a pool refuses rather than leave the GPU slots pending.
	b.reg.GPUs = nil
	b2 := NewScheduler(b.reg, b.root, nil, nil)
	if _, err := b2.Run(context.Background(), m, RunOptions{Resume: true}); err == nil || !strings.Contains(err.Error(), "no GPU pool") {
		t.Errorf("resume without a pool: %v", err)
	}
}

func TestRemoteLeasesGPUSlotsOnlyToFreeGPUs(t *testing.T) {
	cs := newRemoteSide(t)
	cs.b.suite.Tasks[0].Resources = &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}
	cs.b.reg.GPUs = gtxPool(t)
	c, err := NewCoordinator(cs.b.reg, RemoteOptions{Token: remoteTestToken, StagingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rw := &remoteWorld{coord: cs, c: c, url: serveWorkers(t, c), m: cs.b.plan(t, testExperiment()), run: RunOptions{Remote: c}}
	done := rw.attach(t)
	defer done()
	worker := func(id string, side *remoteSide) *Worker {
		w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
			WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: id, Version: BenchVersion, Slots: 2, ScratchDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.register(context.Background()); err != nil {
			t.Fatal(err)
		}
		return w
	}
	cpu := worker("cpu", newRemoteSide(t))
	for i := 0; i < 2; i++ {
		l := lease(t, cpu)
		if l == nil || l.Slot.Task != "t02" {
			t.Fatalf("a worker without GPUs must get only CPU slots: %+v", l)
		}
	}
	if l := lease(t, cpu); l != nil {
		t.Fatalf("no CPU slot is left for the CPU worker: %+v", l)
	}
	gpuSide := newRemoteSide(t)
	gpuSide.b.reg.GPUs = gtxPool(t)
	g := worker("gpu", gpuSide)
	if l := lease(t, g); l == nil || l.Slot.Task != "t01" || len(l.GPUs) != 1 || l.GPUs[0] != gpuSide.b.reg.GPUs.Devices()[0].UUID {
		t.Fatalf("the GPU worker gets a GPU slot naming its reserved device (QC2-10-09): %+v", l)
	}
	if l := lease(t, g); l != nil {
		t.Fatalf("its one GPU is held, so the second GPU slot must wait: %+v", l)
	}
	var listed bool
	for _, w := range c.Workers() {
		if w.ID == "gpu" && len(w.GPUs) == 1 {
			listed = true
		}
	}
	if !listed {
		t.Error("GET /workers lists the GPUs a worker registered")
	}
}

// TestRemoteGPUAcceptMatchesFreeDevices is 2-C-06: with a 16 GiB and a 4 GiB
// device and the 16 GiB one held, a second 16 GiB slot must not be leased,
// while a 4 GiB slot still fits the spare device.
func TestRemoteGPUAcceptMatchesFreeDevices(t *testing.T) {
	gpus := []schemas.GPUInfo{{UUID: "gpu-large", MemoryMB: 16384}, {UUID: "gpu-small", MemoryMB: 4096}}
	large := &schemas.Task{ID: "large", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1, MinVRAMMB: 16000}}}
	small := &schemas.Task{ID: "small", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1, MinVRAMMB: 2000}}}
	held := map[string]bool{"gpu-large": true}
	accept := remoteGPUAccept(gpus, held)
	if accept(large) {
		t.Fatal("second 16 GiB slot accepted while the only 16 GiB device is held")
	}
	if !accept(small) {
		t.Fatal("4 GiB slot refused while the 4 GiB device is free")
	}
	if got := pickRemoteGPUs(gpus, held, small.Resources.GPU); len(got) != 1 || got[0] != "gpu-small" {
		t.Fatalf("picked %v", got)
	}
	if !remoteGPUAccept(gpus, nil)(large) {
		t.Fatal("16 GiB slot refused with every device free")
	}
}
