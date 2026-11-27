package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// TestHostedGitHubStatusTellsTruth (round 2, Q-RG1): through the real hosted
// dispatcher and two organizations, GET /github/status says connected only
// when the org's own saved token can be read. acme's token reads (connected,
// its own login); beta's saved token cannot be read (needs_reconnect, the
// reason, and the repos route answers 409 without calling GitHub). No answer
// carries either token or names the other org's login; a user of no org and
// a caller naming another org are refused.
func TestHostedGitHubStatusTellsTruth(t *testing.T) {
	t.Setenv(keystore.MasterKeyEnv, strings.Repeat("ab", 32))
	t.Setenv("RG_TEST_GH_ID", "client-id")
	t.Setenv("RG_TEST_GH_SECRET", "client-secret")
	f := newHostedFixtureTweak(t, false, func(a *app) {
		a.cfg.Auth.GitHub.ClientIDEnv, a.cfg.Auth.GitHub.ClientSecretEnv = "RG_TEST_GH_ID", "RG_TEST_GH_SECRET"
	})
	master, err := keystore.MasterKey()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := keystore.Open(f.d, master)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const tokA, tokB = "ghp_acme_secret_token", "ghp_beta_secret_token"
	if _, err := keys.Create(ctx, f.acme.ID, keystore.KindSecret, keystore.Put{Provider: auth.GitHubRepoProvider, Label: "acme-octo", Value: tokA}, f.users["alice"].ID); err != nil {
		t.Fatal(err)
	}
	kb, err := keys.Create(ctx, f.beta.ID, keystore.KindSecret, keystore.Put{Provider: auth.GitHubRepoProvider, Label: "beta-octo", Value: tokB}, f.users["bob"].ID)
	if err != nil {
		t.Fatal(err)
	}
	// beta's saved secret no longer opens (as after a master key change).
	if _, err := f.d.Exec(ctx, `UPDATE model_keys SET value_enc = ? WHERE org_id = ? AND id = ?`, "not-a-sealed-value", f.beta.ID, kb.ID); err != nil {
		t.Fatal(err)
	}
	clean := func(who, body string) {
		t.Helper()
		if strings.Contains(body, tokA) || strings.Contains(body, tokB) {
			t.Fatalf("%s: answer carries a token: %s", who, trim(body))
		}
	}
	const status = "/api/bench/github/status"
	for _, who := range []string{"alice", "vera"} {
		r := f.do("GET", status, who, nil, nil)
		clean(who, r.body)
		if r.status != 200 || !strings.Contains(r.body, `"connected":true`) || !strings.Contains(r.body, `"state":"connected"`) || !strings.Contains(r.body, "acme-octo") || strings.Contains(r.body, "beta-octo") {
			t.Fatalf("%s status: %d %s", who, r.status, trim(r.body))
		}
	}
	r := f.do("GET", status, "bob", nil, nil)
	clean("bob", r.body)
	if r.status != 200 || !strings.Contains(r.body, `"connected":false`) || !strings.Contains(r.body, `"state":"needs_reconnect"`) || !strings.Contains(r.body, "cannot be read") || strings.Contains(r.body, "acme-octo") {
		t.Fatalf("bob status: %d %s", r.status, trim(r.body))
	}
	r = f.do("GET", "/api/bench/github/repos", "bob", nil, nil)
	clean("bob repos", r.body)
	if r.status != 409 || !strings.Contains(r.body, "reconnect") {
		t.Fatalf("bob repos: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", status, "mallory", nil, nil); r.status < 400 || strings.Contains(r.body, "octo") {
		t.Fatalf("mallory: %d %s", r.status, trim(r.body))
	}
	for _, hdr := range []map[string]string{nil, {tenancy.OrgHeader: "acme"}} {
		path := status
		if hdr == nil {
			path += "?" + tenancy.OrgQueryParam + "=acme"
		}
		if r := f.do("GET", path, "bob", hdr, nil); (r.status != 403 && r.status != 404) || strings.Contains(r.body, "acme-octo") {
			t.Fatalf("bob naming acme: %d %s", r.status, trim(r.body))
		}
	}
}
