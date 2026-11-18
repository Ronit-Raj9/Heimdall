package graders

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: the verifier's GPU arguments and memory (gpu.go).

func TestVerifierGPUArgs(t *testing.T) {
	gpuTask := &schemas.Task{ID: "t", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}
	if got, err := verifierGPUArgs(&schemas.RunContext{Task: &schemas.Task{}}); got != nil || err != nil {
		t.Errorf("CPU task: %v %v", got, err)
	}
	if _, err := verifierGPUArgs(&schemas.RunContext{Task: gpuTask}); err == nil {
		t.Error("a GPU task's verifier without GPUs must be refused, not run on the CPU")
	}
	held := &schemas.GPUAssignment{Mode: schemas.GPUModeDeviceRequests, GPUs: []schemas.GPUInfo{{UUID: "GPU-a"}}}
	got, err := verifierGPUArgs(&schemas.RunContext{Task: gpuTask, GPU: held})
	if err != nil || !reflect.DeepEqual(got, []string{"--gpus", "device=GPU-a"}) {
		t.Errorf("args %v %v", got, err)
	}
	if verifierMemory(&gradeInputs{}) != "2g" || verifierMemory(&gradeInputs{gpuArgs: got}) != gpuVerifierMemory {
		t.Error("verifier memory")
	}
}
