package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// X7: GPU scheduling (docs/gpu-tasks.md).
//
// A slot of a task with a GPU request (Task.Resources.GPU) never runs on a
// CPU worker. The scheduler starts one GPU worker per GPU in the machine's
// pool (Registry.GPUs) beside its CPU workers; a GPU worker takes only GPU
// slots, holds whole GPUs from the pool for the slot's run, from before the
// agent starts until its verifier is done, and gives them back after the
// run is sealed. So one run uses a GPU at a time, GPU slots never wait
// behind CPU slots or the other way round, and every host-side sample of a
// GPU belongs to exactly one run. Remote workers advertise their GPUs when
// they register (WorkerRegister.Capabilities); the coordinator leases a GPU
// slot only to a worker whose free GPUs meet the request.

// WithoutGPUCheck turns the plan-time GPU check off. A remote worker's
// replan uses it: the manifest is the coordinator's, frozen already, and the
// coordinator never leases a GPU slot to a worker that cannot run it, so a
// worker without a GPU can still run the CPU slots of a mixed experiment.
func WithoutGPUCheck() PlannerOption {
	return func(p *Planner) { p.skipGPU = true }
}

// checkGPU refuses, at plan time and with the reason, every selected task
// whose GPU request this machine cannot meet: no GPU pool, no GPU, too few
// GPUs, or too little memory. It also refuses what GPU tasks cannot be
// combined with: the Kubernetes driver and multi-container team runs, whether
// the team is the task's or an arm's (the effective team, utils.TeamOf, as
// at run time), since team members get no GPU devices. A plan without GPU
// tasks is never refused here.
func (p *Planner) checkGPU(e *schemas.Experiment, suite *schemas.Suite, conds []condPlan, arms ...schemas.ArmManifest) error {
	if p.skipGPU {
		return nil
	}
	var problems []string
	for _, id := range usedTasks(conds) {
		t := suite.TaskByID(id)
		if t == nil || t.GPUCount() == 0 {
			continue
		}
		switch {
		case e != nil && e.Environment != nil && e.Environment.Driver != "" && e.Environment.Driver != "docker":
			problems = append(problems, fmt.Sprintf("task %s needs a GPU and runs on the Docker driver only, not %s", id, e.Environment.Driver))
		case len(t.Team) > 0 || armTeamOn(t, arms):
			problems = append(problems, fmt.Sprintf("task %s needs a GPU and declares a team; GPU team runs are not supported", id))
		case p.reg.GPUs == nil:
			problems = append(problems, fmt.Sprintf("task %s needs a GPU but GPU tasks are off on this machine (gpu.mode: off, or no NVIDIA driver; bifrost-bench gpu check)", id))
		default:
			if err := p.reg.GPUs.Fits(t.Resources.GPU); err != nil {
				problems = append(problems, fmt.Sprintf("task %s %v", id, err))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("gpu: %s", strings.Join(problems, "; "))
}

// armTeamOn reports whether any arm runs a team on task t.
func armTeamOn(t *schemas.Task, arms []schemas.ArmManifest) bool {
	for i := range arms {
		if len(utils.TeamOf(&schemas.RunContext{Agent: &arms[i].Config, Task: t})) > 0 {
			return true
		}
	}
	return false
}

// gpuSlots counts the manifest's slots whose task asks for a GPU.
func gpuSlots(m *schemas.Manifest, suite *schemas.Suite) int {
	n := 0
	for i := range m.Slots {
		if slotNeedsGPU(suite, &m.Slots[i]) {
			n++
		}
	}
	return n
}

// slotNeedsGPU reports whether the slot's task asks for a GPU.
func slotNeedsGPU(suite *schemas.Suite, slot *schemas.Slot) bool {
	if suite == nil || slot == nil {
		return false
	}
	return suite.TaskByID(slot.Task).GPUCount() > 0
}

// gpuWorkers is how many GPU workers the scheduler starts: one per GPU in
// the pool, none without a pool or without GPU slots.
func gpuWorkers(reg *schemas.Registry, slots int) int {
	if reg == nil || reg.GPUs == nil || slots == 0 {
		return 0
	}
	return min(len(reg.GPUs.Devices()), slots)
}

// runGPUSlot holds the GPUs a slot's task asks for, runs the slot, and gives
// them back once its run is sealed. A request the pool can never meet is a
// slot error with the reason, never a run on the CPU.
func (r *schedRun) runGPUSlot(ctx context.Context, slot *schemas.Slot) {
	task := r.suite.TaskByID(slot.Task)
	a, release, err := r.s.reg.GPUs.Acquire(ctx, task.Resources.GPU)
	if err != nil {
		if ctx.Err() != nil {
			r.count(func(s *Summary) { s.Pending++ })
			return
		}
		r.slotError(slot, fmt.Errorf("gpu: task %s: %w", slot.Task, err))
		return
	}
	defer release()
	r.gpuMu.Lock()
	if r.gpus == nil {
		r.gpus = map[string]*schemas.GPUAssignment{}
	}
	r.gpus[slot.SlotID] = a
	r.gpuMu.Unlock()
	defer func() {
		r.gpuMu.Lock()
		delete(r.gpus, slot.SlotID)
		r.gpuMu.Unlock()
	}()
	r.process(ctx, slot)
}

// gpuFor is the assignment a GPU worker holds for the slot; nil otherwise.
func (r *schedRun) gpuFor(slotID string) *schemas.GPUAssignment {
	r.gpuMu.Lock()
	defer r.gpuMu.Unlock()
	return r.gpus[slotID]
}

// holdGPU gives a slot input the GPUs its task asks for when the caller did
// not (an agent check, a remote worker's run): from the registry's pool,
// waiting while they are held. A machine without a pool returns the input
// unchanged and the environment then refuses the run before the agent
// starts, with the reason.
func (p *Pipeline) holdGPU(ctx context.Context, in SlotInput) (SlotInput, func()) {
	none := func() {}
	if in.GPU != nil || in.Suite == nil || in.Slot == nil || p.reg == nil || p.reg.GPUs == nil {
		return in, none
	}
	t := in.Suite.TaskByID(in.Slot.Task)
	if t.GPUCount() == 0 {
		return in, none
	}
	a, release, err := p.reg.GPUs.Acquire(ctx, t.Resources.GPU)
	if err != nil {
		p.logger.Warn("gpu: no GPU for the slot; the run will be refused before its agent starts", "slot", in.Slot.SlotID, "task", t.ID, "error", err.Error())
		return in, none
	}
	in.GPU = a
	return in, release
}

// remoteGPUAccept is the lease filter for one remote worker: a slot of a
// task without a GPU request always fits; a GPU slot fits only when the
// worker registered enough GPUs with enough memory that none of its running
// leases holds. busy is how many of its GPUs its running leases hold.
func remoteGPUAccept(gpus []schemas.GPUInfo, busy int) func(*schemas.Task) bool {
	free := len(gpus) - busy
	return func(t *schemas.Task) bool {
		need := t.GPUCount()
		if need == 0 {
			return true
		}
		if need > free {
			return false
		}
		return gpu.Fits(gpus, t.Resources.GPU) == nil
	}
}

// gpusHeldLocked is how many GPUs the worker's running leases hold. The
// caller holds c.mu.
func (c *Coordinator) gpusHeldLocked(workerID string) int {
	n := 0
	for _, l := range c.leases {
		if l.worker == workerID {
			n += l.gpus
		}
	}
	return n
}

// WorkerCapabilities is what a remote worker registers beyond its slots: the
// GPUs of its pool. It is the one hook a hosted runner that reuses the
// worker code needs: give the worker's registry a GPU pool (gpu.NewPool over
// gpu.Detect, or over the GPUs the sandbox provider declares) and every
// registration carries them, the coordinator leases GPU slots by them, and
// the pipeline holds them per run. Nil without a pool, so a worker without
// GPUs registers exactly as before.
func WorkerCapabilities(reg *schemas.Registry) *schemas.WorkerCapabilities {
	if reg == nil || reg.GPUs == nil || len(reg.GPUs.Devices()) == 0 {
		return nil
	}
	return &schemas.WorkerCapabilities{GPUs: reg.GPUs.Devices()}
}
