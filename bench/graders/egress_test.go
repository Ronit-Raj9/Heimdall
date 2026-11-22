package graders

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEgressVerifier(t *testing.T) {
	spec := schemas.ServiceSpec{ID: "egress", Role: schemas.ServiceRoleEgress, Verifier: true}
	rc := &schemas.RunContext{Task: &schemas.Task{Services: []schemas.ServiceSpec{spec}}}
	in := &gradeInputs{env: map[string]string{"A": "1"}}
	if err := egressVerifier(rc, in); err == nil || !strings.Contains(err.Error(), "not up") {
		t.Fatalf("missing service: %v", err)
	}
	rc.Services = []schemas.ServiceHandle{{Spec: spec, Network: "bench-snet-r1"}}
	if err := egressVerifier(rc, in); err != nil {
		t.Fatal(err)
	}
	if len(in.networks) != 1 || in.networks[0] != "bench-snet-r1" || in.env["HTTPS_PROXY"] != "http://egress:3128" || in.env["A"] != "1" {
		t.Fatalf("inputs = %+v", in)
	}
	plain := &gradeInputs{}
	rc2 := &schemas.RunContext{Task: &schemas.Task{Services: []schemas.ServiceSpec{{ID: "egress", Role: schemas.ServiceRoleEgress}}}}
	if err := egressVerifier(rc2, plain); err != nil || len(plain.networks) != 0 || plain.env != nil {
		t.Fatalf("verifier false: %v %+v", err, plain)
	}
}
