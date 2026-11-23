package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// inspectImageEnv is a task image env that also inspects images, as the
// docker workspace and the sandbox driver do: without an explicit builder
// the planner falls back to the docker CLI for it.
type inspectImageEnv struct{ imageEnv }

func (inspectImageEnv) ImageID(context.Context, string) (string, error) { return "sha256:x", nil }

// The shared worker plans with WithoutHostAgentBuilds: an environment that
// only inspects images no longer falls back to a policy-less docker build
// (SEC-A); the plan is refused with a plain reason instead.
func TestAgentImageFallbackRefusedOnSharedWorker(t *testing.T) {
	b, _ := taskImageBench(t)
	b.reg.Environments = []schemas.Environment{inspectImageEnv{imageEnv{fakeScenarioEnv: fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, &scenarioWorld{}}}}}
	// Local CLI: the fallback is still the docker CLI.
	if _, ok := NewPlanner(b.reg, b.root, nil).agentImageBuilder(b.suite).(DockerAgentImages); !ok {
		t.Fatal("local planner lost its docker fallback")
	}
	_, err := NewPlanner(b.reg, b.root, nil, WithoutHostAgentBuilds()).Plan(context.Background(), testExperiment())
	if err == nil || !strings.Contains(err.Error(), "agent image bench/agent:1-coding-core-t01: no image builder for this worker") {
		t.Fatalf("plan error %v", err)
	}
	// An explicit builder still wins, in either option order.
	fb := &fakeAgentBuilder{}
	for _, opts := range [][]PlannerOption{{WithoutHostAgentBuilds(), WithAgentImageBuilder(fb)}, {WithAgentImageBuilder(fb), WithoutHostAgentBuilds()}} {
		before := len(fb.built)
		if _, err := NewPlanner(b.reg, b.root, nil, opts...).Plan(context.Background(), testExperiment()); err != nil {
			t.Fatal(err)
		}
		if len(fb.built) == before {
			t.Fatal("explicit builder built nothing")
		}
	}
	if got := NewPlanner(b.reg, b.root, nil, WithAgentImageBuilder(fb), WithoutHostAgentBuilds()).AgentImageBuilder(b.suite); got != AgentImageBuilder(fb) {
		t.Fatalf("AgentImageBuilder %T", got)
	}
}
