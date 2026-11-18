package graders

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestComposeVerifier(t *testing.T) {
	db := schemas.ServiceSpec{ID: "db", Compose: &schemas.ComposeService{Name: "db", Verifier: true}}
	cache := schemas.ServiceSpec{ID: "cache", Compose: &schemas.ComposeService{Name: "cache", Verifier: true}}
	other := schemas.ServiceSpec{ID: "mcp"}
	task := &schemas.Task{Services: []schemas.ServiceSpec{db, cache, other},
		Environment: &schemas.TaskEnvironment{Env: map[string]string{"DATABASE_URL": "postgres://db/app"}}}
	rc := &schemas.RunContext{Task: task, Services: []schemas.ServiceHandle{
		{Spec: db, Network: "bench-net-r_1"}, {Spec: cache, Network: "bench-net-r_1"}, {Spec: other, Network: "bench-net-r_1"},
	}}
	in := &gradeInputs{}
	if err := composeVerifier(rc, in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.networks, []string{"bench-net-r_1"}) || in.env["DATABASE_URL"] != "postgres://db/app" {
		t.Fatalf("inputs %+v", in)
	}
	if a := verifierNetworkArgs(in); !reflect.DeepEqual(a, []string{"--network", "bench-net-r_1"}) {
		t.Fatalf("network args %q", a)
	}
	if a := verifierEnvArgs(in); !reflect.DeepEqual(a, []string{"-e", "DATABASE_URL=postgres://db/app"}) {
		t.Fatalf("env args %q", a)
	}
	// A regrade after cleanup has no live services: refused with the reason.
	rc.Services = nil
	err := composeVerifier(rc, &gradeInputs{})
	if err == nil || !strings.Contains(err.Error(), "needs the run's live services") {
		t.Fatalf("regrade without services: %v", err)
	}
	// A plain task keeps --network none.
	if a := verifierNetworkArgs(&gradeInputs{}); !reflect.DeepEqual(a, []string{"--network", "none"}) {
		t.Fatalf("plain network args %q", a)
	}
}
