package environments

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestServiceSecretPerRun(t *testing.T) {
	spec := schemas.ServiceSpec{ID: "a", Env: map[string]string{"T": ServiceSecretVar, "U": "plain"}}
	rc1 := &schemas.RunContext{Run: &schemas.Run{RunID: "r1"}}
	rc2 := &schemas.RunContext{Run: &schemas.Run{RunID: "r2"}}
	a, b := serviceEnv(rc1, spec)["T"], serviceEnv(rc1, schemas.ServiceSpec{ID: "b", Env: spec.Env})["T"]
	if a == ServiceSecretVar || len(a) != 40 || a != b {
		t.Fatalf("same run must share one secret: %q %q", a, b)
	}
	if serviceEnv(rc2, spec)["T"] == a {
		t.Fatal("another run must get another secret")
	}
	if serviceEnv(rc1, spec)["U"] != "plain" {
		t.Fatal("plain values stay")
	}
}
