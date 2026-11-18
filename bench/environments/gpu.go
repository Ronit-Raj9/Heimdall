package environments

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: GPU passthrough and sampling for the agent container (docs/gpu-tasks.md).
//
// A run of a task with a GPU request holds whole GPUs from the scheduler's
// pool (RunContext.GPU). Only the agent container gets them here, and the
// clean-room verifier in graders/cleanroom.go; services, observers, helpers,
// the tool proxy and the event collector never do. The GPUs are passed by
// UUID as CDI devices or one docker device request (gpu.ContainerArgs),
// never through privileged mode or an added capability. While the agent
// runs, a host-side nvidia-smi loop samples them into gpu.samples.jsonl and
// the run's hardware record (Run.Hardware.Sampler).

// agentGPUArgs are the docker arguments that give the agent container the
// run's GPUs. A task that asks for a GPU but whose run holds none is refused
// before the container exists: running it on the CPU instead would score
// the agent on the wrong machine.
func agentGPUArgs(rc *schemas.RunContext) ([]string, error) {
	need := rc.Task.GPUCount()
	switch {
	case need == 0 && rc.GPU == nil:
		return nil, nil
	case need > 0 && rc.GPU == nil:
		return nil, fmt.Errorf("task %s needs %d GPU(s) but the run holds none: GPU slots come from the scheduler's GPU pool (bifrost-bench gpu check)", rc.Task.ID, need)
	case need > 0 && len(rc.GPU.GPUs) < need:
		return nil, fmt.Errorf("task %s needs %d GPU(s) but the run holds %d", rc.Task.ID, need, len(rc.GPU.GPUs))
	}
	return gpu.ContainerArgs(rc.GPU)
}

// startGPUSampler starts the host-side sampler for one agent session; nil
// when the run holds no GPU or the sampler is off.
func startGPUSampler(ctx context.Context, rc *schemas.RunContext) *gpu.Sampler {
	if rc.GPU == nil || rc.Dir == "" {
		return nil
	}
	return gpu.StartSampler(ctx, rc.GPU, rc.Dir, rc.SessionIndex+1)
}

// stopGPUSampler ends the session's sampling and adds it to the run's
// hardware record, which run.json seals.
func stopGPUSampler(rc *schemas.RunContext, s *gpu.Sampler) {
	sum := s.Stop()
	if sum == nil || rc.Run == nil {
		return
	}
	if rc.Run.Hardware == nil {
		rc.Run.Hardware = gpu.Hardware(rc.GPU)
	}
	rc.Run.Hardware.Sampler = gpu.Merge(rc.Run.Hardware.Sampler, sum)
}
