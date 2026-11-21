package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// credGateway is a fake gateway that can serve organization keys.
type credGateway struct {
	*fakeGateway
	applied []schemas.ProviderCredential
	bound   map[string]string
}

func (g *credGateway) ApplyOrgCredentials(_ context.Context, _ string, creds []schemas.ProviderCredential) error {
	g.applied = append(g.applied, creds...)
	return nil
}

func (g *credGateway) BindExperimentOrg(expID, orgID string) { g.bound[expID] = orgID }

// TestApplyLeaseCredentialsNoFallback covers X01: a hosted lease never runs
// on the runner's own provider keys. A failed key fetch refuses the lease, an
// org with no keys is still bound (so the gateway refuses paid runs), and a
// local lease keeps the old fallback.
func TestApplyLeaseCredentialsNoFallback(t *testing.T) {
	b := newBench(t)
	cases := []struct {
		name      string
		status    int
		body      any
		hosted    bool
		wantWhy   string
		wantBound bool
		wantKeys  int
	}{
		{"hosted fetch fails", http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"message": "key store down"}}, true, "never use this runner's own provider keys", false, 0},
		{"local fetch fails", http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"message": "key store down"}}, false, "", false, 0},
		{"hosted org without keys", http.StatusOK, schemas.LeaseCredentials{OrgID: "org_a", LeaseID: "l1", ExpID: "exp_1"}, true, "", true, 0},
		{"local org without keys", http.StatusOK, schemas.LeaseCredentials{OrgID: "org_a", LeaseID: "l1", ExpID: "exp_1"}, false, "", false, 0},
		{"hosted org with a key", http.StatusOK, schemas.LeaseCredentials{OrgID: "org_a", LeaseID: "l1", ExpID: "exp_1",
			Credentials: []schemas.ProviderCredential{{KeyID: "k1", Provider: "openai", Value: "sk-x"}}}, true, "", true, 1},
		{"hosted lease with no org", http.StatusOK, schemas.LeaseCredentials{LeaseID: "l1", ExpID: "exp_1"}, true, "named no organization", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/leases/l1/credentials") {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()
			gw := &credGateway{fakeGateway: b.gw, bound: map[string]string{}}
			reg := *b.reg
			reg.Gateway = gw
			w, err := NewWorker(&reg, NewPipeline(&reg, b.root, nil), NewPlanner(&reg, b.root, nil).Plan,
				WorkerOptions{Server: srv.URL, Token: "t"})
			if err != nil {
				t.Fatal(err)
			}
			_, why := w.applyLeaseCredentials(context.Background(), &schemas.WorkerLease{LeaseID: "l1", ExpID: "exp_1"}, tc.hosted)
			if tc.wantWhy == "" && why != "" || tc.wantWhy != "" && !strings.Contains(why, tc.wantWhy) {
				t.Fatalf("why = %q, want %q", why, tc.wantWhy)
			}
			if _, bound := gw.bound["exp_1"]; bound != tc.wantBound {
				t.Errorf("bound = %v, want %v", bound, tc.wantBound)
			}
			if len(gw.applied) != tc.wantKeys {
				t.Errorf("applied %d keys, want %d", len(gw.applied), tc.wantKeys)
			}
		})
	}
}
