package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// logPolicyGateway records the prompt logging policy a lease applied.
type logPolicyGateway struct {
	schemas.Gateway
	exp, org string
	got      *schemas.PromptLogPolicy
}

func (g *logPolicyGateway) SetExperimentLogPolicy(expID, orgID string, p schemas.PromptLogPolicy) {
	g.exp, g.org, g.got = expID, orgID, &p
}

// X03: the lease's prompt logging policy reaches the gateway, normalized,
// even when the organization delivered no provider keys.
func TestLeaseAppliesPromptLogPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(schemas.LeaseCredentials{OrgID: "org_a", LeaseID: "l1", ExpID: "e1",
			PromptLogging: &schemas.PromptLogPolicy{KeepContent: false, RetentionDays: 99}})
	}))
	defer srv.Close()
	gw := &logPolicyGateway{}
	w := &Worker{reg: &schemas.Registry{Gateway: gw}, opts: WorkerOptions{Server: srv.URL, HTTP: srv.Client()}, logger: schemas.NoopLogger{}}
	if _, why := w.applyLeaseCredentials(context.Background(), &schemas.WorkerLease{LeaseID: "l1", ExpID: "e1"}, false); why != "" {
		t.Fatalf("refused: %s", why)
	}
	if gw.got == nil || gw.exp != "e1" || gw.org != "org_a" || gw.got.KeepContent || gw.got.RetentionDays != schemas.PromptLogRetentionMax {
		t.Fatalf("applied %s %s %+v", gw.exp, gw.org, gw.got)
	}
}
