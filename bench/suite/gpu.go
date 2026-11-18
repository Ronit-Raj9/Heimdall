package suite

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxGPUCount is the most GPUs one task may ask for.
const maxGPUCount = 8

// lintGPUTask checks a task's GPU request (X7, docs/gpu-tasks.md): a count
// of 1 to 8 and a memory floor that is not negative, no team (GPU team runs
// are not supported), and, on the ml_engineering track, a request at all.
// Whether this machine can run it is the planner's question, not lint's.
func lintGPUTask(s *schemas.Suite, t *schemas.Task) []Problem {
	taskYAML := rel(s.Root, filepath.Join(t.Dir, TaskFile))
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: taskYAML, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if t.Resources == nil || t.Resources.GPU == nil {
		if s.Track == schemas.TrackMLEngineering {
			add(SeverityWarning, "resources.gpu: missing on a task of the %s track; the task runs without a GPU", schemas.TrackMLEngineering)
		}
		return out
	}
	g := t.Resources.GPU
	if g.Count < 1 || g.Count > maxGPUCount {
		add(SeverityError, "resources.gpu.count: %d; a task asks for 1 to %d whole GPUs", g.Count, maxGPUCount)
	}
	if g.MinVRAMMB < 0 {
		add(SeverityError, "resources.gpu.min_vram_mb: %d is negative", g.MinVRAMMB)
	}
	if len(t.Team) > 0 {
		add(SeverityError, "resources.gpu: a task with a team cannot ask for GPUs; GPU team runs are not supported")
	}
	return out
}

// holdGPU takes, for the whole admission of a GPU task, the GPUs it asks
// for, so every control and its verifier run on them, one control at a time.
// A task without a GPU request holds nothing; one that needs GPUs on a
// machine without a pool that fits is refused with the reason, never
// admitted on the CPU.
func (a *admitter) holdGPU(ctx context.Context) (func(), error) {
	none := func() {}
	if a.t.GPUCount() == 0 {
		return none, nil
	}
	if a.deps.GPUs == nil {
		return none, fmt.Errorf("admit task %s: it needs %d GPU(s) and this machine has no GPU pool (gpu.mode off, or no NVIDIA driver; bifrost-bench gpu check)", a.t.ID, a.t.GPUCount())
	}
	got, release, err := a.deps.GPUs.Acquire(ctx, a.t.Resources.GPU)
	if err != nil {
		return none, fmt.Errorf("admit task %s: %w", a.t.ID, err)
	}
	a.gpu = got
	return release, nil
}

// GPU control budgets: a control of a GPU task (a reference that trains a
// model, say) loads PyTorch and a CUDA context, which do not fit the usual
// 1 GiB and 120 seconds of a script control.
const (
	gpuControlMemGB = 6
	gpuControlCPUs  = 2
	gpuControlWallS = 900
)

// gpuControlBudget raises a control's budgets when the task holds GPUs.
func (a *admitter) gpuControlBudget(plan *schemas.RunPlan) {
	if a.gpu == nil {
		return
	}
	plan.MemGB = max(plan.MemGB, gpuControlMemGB)
	plan.CPUs = max(plan.CPUs, gpuControlCPUs)
	plan.WallS = max(plan.WallS, gpuControlWallS)
}
