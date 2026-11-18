package environments

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDockerDriverIsTheDockerWorkspace(t *testing.T) {
	d := NewDockerDriver(DockerWorkspaceConfig{})
	w := newDockerWorkspace(DockerWorkspaceConfig{})
	if d.GetName() != w.GetName() || d.GetVersion() != w.GetVersion() || d.Driver() != DriverDocker {
		t.Fatalf("name %s version %s driver %s", d.GetName(), d.GetVersion(), d.Driver())
	}
	var env schemas.Environment = d
	if _, ok := env.(schemas.ServiceEnvironment); !ok {
		t.Error("services lost behind the seam")
	}
	if _, ok := env.(schemas.SessionEnvironment); !ok {
		t.Error("sessions lost behind the seam")
	}
	if _, ok := env.(schemas.SnapshotEnvironment); !ok {
		t.Error("snapshots lost behind the seam")
	}
	if _, ok := env.(schemas.AnswerEnvironment); !ok {
		t.Error("answers lost behind the seam")
	}
	if _, ok := env.(schemas.ArtifactEnvironment); !ok {
		t.Error("artifacts lost behind the seam")
	}
	if _, ok := env.(schemas.TeamEnvironment); !ok {
		t.Error("teams lost behind the seam")
	}
	if _, ok := env.(schemas.AdapterFaultEnvironment); !ok {
		t.Error("adapter faults lost behind the seam")
	}
	if _, ok := env.(interface{ ImageRoles() map[string]string }); !ok {
		t.Error("image roles lost behind the seam")
	}
	if r := d.Refusals(DriverSlot{}); r != nil {
		t.Errorf("the Docker driver refuses %v", r)
	}
	if DriverOf(w) != DriverDocker || DriverOf(d) != DriverDocker {
		t.Error("DriverOf")
	}
}

func TestDockerDriverRefusesAKubernetesManifest(t *testing.T) {
	d := NewDockerDriver(DockerWorkspaceConfig{})
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Manifest: &schemas.Manifest{ExpID: "e_1",
		Experiment: schemas.Experiment{Environment: &schemas.EnvironmentChoice{Driver: DriverKubernetes}}}}
	_, err := d.Create(context.Background(), rc)
	if err == nil || !strings.Contains(err.Error(), "planned for the kubernetes driver") {
		t.Fatalf("create: %v", err)
	}
	if err := CheckFrozenDriver(&schemas.RunContext{Manifest: &schemas.Manifest{}}, DriverDocker); err != nil {
		t.Fatalf("a manifest with no driver is a Docker one: %v", err)
	}
	if FrozenDriver(nil) != DriverDocker {
		t.Fatal("nil manifest")
	}
}

// fakeDriver refuses what its reasons say, for PlanDriver.
type fakeDriver struct {
	*dockerWorkspace
	name    string
	reasons map[string][]string // task id -> reasons
}

func (f *fakeDriver) Driver() string { return f.name }

func (f *fakeDriver) Refusals(s DriverSlot) []string {
	if s.Experiment == nil {
		return []string{"slot without its experiment"}
	}
	return f.reasons[s.Task.ID]
}

func TestPlanDriver(t *testing.T) {
	suite := &schemas.Suite{ID: "s", Environment: DockerWorkspaceType}
	docker := []schemas.Environment{NewDockerDriver(DockerWorkspaceConfig{})}
	exp := &schemas.Experiment{}
	slots := []DriverSlot{{Task: &schemas.Task{ID: "t01"}}, {Task: &schemas.Task{ID: "t02"}}}
	if got, err := PlanDriver(docker, suite, exp, slots); err != nil || got != nil {
		t.Fatalf("docker with no choice must freeze nothing (hash unchanged): %v %v", got, err)
	}
	asked := &schemas.Experiment{Environment: &schemas.EnvironmentChoice{Driver: DriverKubernetes}}
	if _, err := PlanDriver(docker, suite, asked, slots); err == nil || !strings.Contains(err.Error(), "asks for the kubernetes driver") {
		t.Fatalf("an experiment asking for another driver: %v", err)
	}
	explicit := &schemas.Experiment{Environment: &schemas.EnvironmentChoice{Driver: DriverDocker}}
	if got, err := PlanDriver(docker, suite, explicit, slots); err != nil || got == nil || got.Driver != DriverDocker {
		t.Fatalf("an explicit choice is kept: %v %v", got, err)
	}
	k := []schemas.Environment{&fakeDriver{dockerWorkspace: newDockerWorkspace(DockerWorkspaceConfig{}), name: DriverKubernetes}}
	if got, err := PlanDriver(k, suite, exp, slots); err != nil || got == nil || got.Driver != DriverKubernetes {
		t.Fatalf("a kubernetes plan freezes its driver: %v %v", got, err)
	}
	refusing := []schemas.Environment{&fakeDriver{dockerWorkspace: newDockerWorkspace(DockerWorkspaceConfig{}), name: DriverKubernetes,
		reasons: map[string][]string{"t01": {"b reason", "a reason"}, "t02": {"a reason"}}}}
	_, err := PlanDriver(refusing, suite, exp, slots)
	if err == nil || err.Error() != "the kubernetes driver cannot run this experiment: a reason; b reason" {
		t.Fatalf("refusals listed once each, sorted: %v", err)
	}
}

func TestOutputWriterCapsAndRedacts(t *testing.T) {
	var b strings.Builder
	w := NewOutputWriter(&b, 20, "sk-secret")
	w.Write([]byte("key sk-sec"))
	w.Write([]byte("ret end and much more text"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !w.Truncated() || strings.Contains(b.String(), "sk-secret") {
		t.Fatalf("out %q truncated %v", b.String(), w.Truncated())
	}
}
