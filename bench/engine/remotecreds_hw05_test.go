package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// wrapGateway hides its inner gateway's extra methods the way
// sandbox.Gateway does: only the schemas.Gateway interface is promoted.
type wrapGateway struct{ schemas.Gateway }

func (g *wrapGateway) UnwrapGateway() schemas.Gateway { return g.Gateway }

// TestApplyLeaseCredentialsThroughSandboxGateway covers N216 fix 1: a hosted
// lease on a runner whose gateway is wrapped (the Modal registry) reaches the
// inner gateway's org keys instead of refusing every lease.
func TestApplyLeaseCredentialsThroughSandboxGateway(t *testing.T) {
	b := newBench(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(schemas.LeaseCredentials{OrgID: "org_a", LeaseID: "l1", ExpID: "exp_1",
			Credentials: []schemas.ProviderCredential{{KeyID: "k1", Provider: "openai", Value: "sk-x"}}})
	}))
	defer srv.Close()
	inner := &credGateway{fakeGateway: b.gw, bound: map[string]string{}}
	reg := *b.reg
	reg.Gateway = &wrapGateway{Gateway: inner}
	w, err := NewWorker(&reg, NewPipeline(&reg, b.root, nil), NewPlanner(&reg, b.root, nil).Plan,
		WorkerOptions{Server: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, why := w.applyLeaseCredentials(context.Background(), &schemas.WorkerLease{LeaseID: "l1", ExpID: "exp_1"}, true)
	if why != "" {
		t.Fatalf("lease refused: %q", why)
	}
	if len(inner.applied) != 1 || inner.bound["exp_1"] != "org_a" {
		t.Fatalf("inner gateway got keys %d, bound %v", len(inner.applied), inner.bound)
	}
}
