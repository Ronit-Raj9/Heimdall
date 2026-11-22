package environments

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWithEgressEnv(t *testing.T) {
	plan := &schemas.RunPlan{Env: map[string]string{"OPENAI_BASE_URL": "http://bench-gw:4000/v1", "HTTP_PROXY": "keep-mine"}}
	none := &schemas.RunContext{Task: &schemas.Task{ID: "t"}}
	if got := withEgressEnv(none, plan); got != plan {
		t.Fatal("a run without an egress service got a new plan")
	}
	rc := &schemas.RunContext{Task: &schemas.Task{ID: "t", Services: []schemas.ServiceSpec{
		{ID: "db", Role: schemas.ServiceRoleData},
		{ID: "egress", Role: schemas.ServiceRoleEgress},
	}}}
	got := withEgressEnv(rc, plan)
	if got.Env["HTTPS_PROXY"] != "http://egress:3128" || got.Env["HTTP_PROXY"] != "keep-mine" || got.Env["SSL_CERT_FILE"] != egress.BundlePath {
		t.Fatalf("env = %v", got.Env)
	}
	np := got.Env["NO_PROXY"]
	for _, h := range []string{"bench-gw", "db", ToolProxyHost, "localhost"} {
		if !strings.Contains(np, h) {
			t.Errorf("NO_PROXY %q lacks %s", np, h)
		}
	}
	if _, ok := plan.Env["HTTPS_PROXY"]; ok {
		t.Error("the input plan was changed")
	}
}

func TestEgressServiceEnvMode(t *testing.T) {
	spec := schemas.ServiceSpec{ID: "egress", Role: schemas.ServiceRoleEgress, Env: map[string]string{egress.EnvMode: egress.ModeReplay}}
	rc := &schemas.RunContext{Manifest: &schemas.Manifest{}}
	if env := serviceEnv(rc, spec); env[egress.EnvMode] != egress.ModeReplay {
		t.Fatalf("default mode = %v", env)
	}
	rc.Manifest.Experiment.Egress = egress.ModeOffline
	if env := serviceEnv(rc, spec); env[egress.EnvMode] != egress.ModeOffline {
		t.Fatalf("experiment mode = %v", env)
	}
	other := schemas.ServiceSpec{ID: "db", Role: schemas.ServiceRoleData}
	if env := serviceEnv(rc, other); env[egress.EnvMode] != "" {
		t.Fatalf("data service got the mode: %v", env)
	}
}
