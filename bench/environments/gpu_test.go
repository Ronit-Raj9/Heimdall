package environments

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: the agent container's GPU arguments (gpu.go).

func TestAgentGPUArgs(t *testing.T) {
	gpuTask := &schemas.Task{ID: "t", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}
	held := &schemas.GPUAssignment{Mode: schemas.GPUModeCDI, GPUs: []schemas.GPUInfo{{UUID: "GPU-a"}}}
	if got, err := agentGPUArgs(&schemas.RunContext{Task: &schemas.Task{ID: "c"}}); got != nil || err != nil {
		t.Errorf("a CPU task gets no GPU arguments: %v %v", got, err)
	}
	if _, err := agentGPUArgs(&schemas.RunContext{Task: gpuTask}); err == nil || !strings.Contains(err.Error(), "holds none") {
		t.Errorf("a GPU task without GPUs must be refused before the agent starts: %v", err)
	}
	got, err := agentGPUArgs(&schemas.RunContext{Task: gpuTask, GPU: held})
	if err != nil || !reflect.DeepEqual(got, []string{"--device", "nvidia.com/gpu=GPU-a"}) {
		t.Errorf("args %v %v", got, err)
	}
	two := &schemas.Task{ID: "t", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 2}}}
	if _, err := agentGPUArgs(&schemas.RunContext{Task: two, GPU: held}); err == nil {
		t.Error("a run holding fewer GPUs than asked must be refused")
	}
	// The GPU arguments land in the agent's docker create line, which keeps
	// every capability dropped and never asks for privileged mode.
	plan := &schemas.RunPlan{Image: "img", Argv: []string{"true"}}
	args := agentCreateArgs("bench-r", nil, plan, &schemas.EnvironmentHandle{Volume: "v", AuxVolume: "a"}, "none", "/env", &launch{}, got, false)
	line := strings.Join(args, " ")
	if !strings.Contains(line, "--device nvidia.com/gpu=GPU-a") || strings.Contains(line, "--privileged") || !strings.Contains(line, "--cap-drop ALL") {
		t.Errorf("agent create line: %s", line)
	}
}
