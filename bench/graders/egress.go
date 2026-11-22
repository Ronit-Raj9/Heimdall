package graders

import (
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/schemas"
)

// egressVerifier lets the verifier of a task whose egress service sets
// verifier (recorded internet, docs/egress.md) reach that service: it joins
// the service's internal run network and gets the proxy variables, so a
// test script that downloads at verify time is answered from the recorded
// cache. The verifier gets no other route out.
func egressVerifier(rc *schemas.RunContext, in *gradeInputs) error {
	if rc.Task == nil {
		return nil
	}
	var want *schemas.ServiceSpec
	for i := range rc.Task.Services {
		if sv := &rc.Task.Services[i]; sv.Role == schemas.ServiceRoleEgress && sv.Verifier {
			want = sv
		}
	}
	if want == nil {
		return nil
	}
	for _, h := range rc.Services {
		if h.Spec.ID != want.ID || h.Network == "" {
			continue
		}
		if !slices.Contains(in.networks, h.Network) {
			in.networks = append(in.networks, h.Network)
		}
		env := make(map[string]string, len(in.env)+16)
		for k, v := range egress.ClientEnv(egress.ProxyURL(want.ID), nil) {
			env[k] = v
		}
		for k, v := range in.env { // the task's own variables win
			env[k] = v
		}
		in.env = env
		return nil
	}
	return fmt.Errorf("the verifier downloads through egress service %s, but this run has it not up; a grade needs the run's live services (a regrade after the run was cleaned up cannot reach it)", want.ID)
}
