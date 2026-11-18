package graders

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: the verifier of a GPU task (docs/gpu-tasks.md). It loads the agent's
// model or kernel in the clean room on the same GPUs the run held, passed by
// UUID (gpu.ContainerArgs), never through privileged mode; the preparation
// step that applies the diff never gets them. A GPU task whose run holds no
// GPU is not graded on the CPU instead: the grade is refused, so the run
// reads invalid_evaluator rather than a slow fail.

// verifierGPUArgs are the docker arguments that give the verifier the run's
// GPUs; none for a task without a GPU request.
func verifierGPUArgs(rc *schemas.RunContext) ([]string, error) {
	need := rc.Task.GPUCount()
	switch {
	case need == 0:
		return nil, nil
	case rc.GPU == nil:
		return nil, fmt.Errorf("task %s needs %d GPU(s) for its verifier but the run holds none", rc.Task.ID, need)
	case len(rc.GPU.GPUs) < need:
		return nil, fmt.Errorf("task %s needs %d GPU(s) for its verifier but the run holds %d", rc.Task.ID, need, len(rc.GPU.GPUs))
	}
	return gpu.ContainerArgs(rc.GPU)
}

// gpuVerifierMemory is the verifier's memory limit when it holds a GPU: a
// CUDA context with PyTorch loaded takes most of the usual 2 GiB by itself,
// and a verifier that times the agent's code in child processes holds
// several.
const gpuVerifierMemory = "8g"

// verifierMemory is the verifier container's memory limit: the usual 2 GiB,
// or gpuVerifierMemory for the verifier of a GPU task.
func verifierMemory(in *gradeInputs) string {
	if len(in.gpuArgs) > 0 {
		return gpuVerifierMemory
	}
	return "2g"
}
