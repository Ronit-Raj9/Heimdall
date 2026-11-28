package engine

import (
	"context"
	"testing"
)

// K7: a plan whose per-task agent builds are deferred records the same
// per-task tags (so the same manifest hash once the hosted ids are adopted)
// and builds nothing.
func TestPlanDeferredTaskAgentBuildsRecordTagsOnly(t *testing.T) {
	b, fb := taskImageBench(t)
	p := NewPlanner(b.reg, b.root, nil, WithAgentImageBuilder(fb))
	built, err := p.Plan(context.Background(), testExperiment())
	if err != nil {
		t.Fatal(err)
	}
	fb.built = nil
	deferred, err := p.Plan(WithDeferredTaskAgentBuilds(context.Background()), testExperiment())
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.built) != 0 {
		t.Fatalf("a deferred plan built %d images", len(fb.built))
	}
	a, d := built.Arms["A"], deferred.Arms["A"]
	if d.TaskImages["t01"] == "" || d.TaskImages["t01"] != a.TaskImages["t01"] {
		t.Fatalf("deferred tags %v, built tags %v", d.TaskImages, a.TaskImages)
	}
	if len(d.TaskImageIDs) != 0 {
		t.Fatalf("a deferred plan recorded ids %v", d.TaskImageIDs)
	}
}
