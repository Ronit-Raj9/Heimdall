package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeKube is the fake environment as a Kubernetes driver that refuses
// fixture probes, as the real one does (k8s/refusals.go).
type fakeKube struct {
	fakeEnv
}

func (fakeKube) Driver() string { return environments.DriverKubernetes }

func (fakeKube) Refusals(s environments.DriverSlot) []string {
	if s.Probe != nil && s.Probe.Layer == schemas.LayerFixture {
		return []string{"probe layer fixture is not supported"}
	}
	return nil
}

func TestPlanDriverDockerKeepsTheManifest(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, testExperiment())
	if m.Experiment.Environment != nil {
		t.Fatalf("a Docker plan froze a driver: %+v", m.Experiment.Environment)
	}
}

func TestPlanDriverKubernetes(t *testing.T) {
	b := newBench(t)
	b.reg.Environments = []schemas.Environment{fakeKube{fakeEnv{part{"docker_workspace"}, b.w}}}
	m := b.plan(t, testExperiment())
	if m.Experiment.Environment == nil || m.Experiment.Environment.Driver != environments.DriverKubernetes {
		t.Fatalf("the kubernetes driver is not frozen: %+v", m.Experiment.Environment)
	}
	gw := testExperiment(schemas.ConditionSpec{ID: "model-outage", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1})
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), gw); err != nil {
		t.Fatalf("a gateway probe must plan: %v", err)
	}
	stale := testExperiment(schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1})
	_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), stale)
	if err == nil || !strings.Contains(err.Error(), "the kubernetes driver cannot run this experiment: probe layer fixture") {
		t.Fatalf("a fixture probe must be refused at plan time: %v", err)
	}
}

func TestPlanDriverMismatch(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Environment = &schemas.EnvironmentChoice{Driver: environments.DriverKubernetes}
	_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
	if err == nil || !strings.Contains(err.Error(), "asks for the kubernetes driver") {
		t.Fatalf("an experiment for another driver must be refused: %v", err)
	}
}
