package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// G01: the lease credentials route delivers the org secrets the leased slot
// names, and only those, only from the lease's org.
func TestLeaseCredentialsSecrets(t *testing.T) {
	ks := credsStore(t) // org_a has secret GH
	ctx := context.Background()
	for _, s := range []struct{ org, name, value string }{
		{"org_a", "SEARCH_KEY", "a-search"},
		{"org_a", "UNNAMED", "a-unnamed"},
		{"org_b", "SEARCH_KEY", "b-search"},
		{"org_b", "AGENT_TOKEN", "b-token"},
	} {
		if _, err := ks.Create(ctx, s.org, keystore.KindSecret, keystore.Put{Provider: "secret", Label: s.name, Value: s.value}, "u"); err != nil {
			t.Fatal(err)
		}
	}
	holders := fakeHolders{"l1": {WorkerID: "w1", TokenID: "rtk_a1", OrgID: "org_a", ExpID: "exp_1",
		SecretNames: []string{"AGENT_TOKEN", "SEARCH_KEY"}}}
	h := &leaseCredsHandler{mode: schemas.ModeLive, orgID: "org_a", keys: ks,
		holders: func(string) LeaseHolders { return holders }, now: time.Now}
	rc := callCreds(h, &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a", TokenID: "rtk_a1"}, "l1")
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out schemas.LeaseCredentials
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Secrets) != 1 || out.Secrets["SEARCH_KEY"] != "a-search" {
		t.Fatalf("secrets %v", out.Secrets)
	}
	body := string(rc.Response.Body())
	for _, leak := range []string{"b-search", "b-token", "a-unnamed", "ghp_secret"} {
		if strings.Contains(body, leak) {
			t.Errorf("answer leaks %s", leak)
		}
	}
	// A runner token of another org gets nothing.
	if got := callCreds(h, &auth.Principal{Kind: auth.KindRunner, OrgID: "org_b", TokenID: "rtk_b1"}, "l1").Response.StatusCode(); got != 403 {
		t.Fatalf("other org runner status %d", got)
	}
}
