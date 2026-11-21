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
	"github.com/valyala/fasthttp"
)

type fakeHolders map[string]schemas.LeaseHolder

func (f fakeHolders) LeaseHolder(id string) (schemas.LeaseHolder, bool) {
	h, ok := f[id]
	return h, ok
}

func credsStore(t *testing.T) keystore.Store {
	t.Helper()
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ks.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "anthropic", Value: "sk-ant-org-a", Models: []string{"claude-sonnet-4-5"}}, "u"); err != nil {
		t.Fatal(err)
	}
	over, err := ks.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "openai", Value: "sk-oa-over", BudgetUSD: 1}, "u")
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.AddSpend(ctx, "org_a", over.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Create(ctx, "org_a", keystore.KindSecret, keystore.Put{Label: "GH", Value: "ghp_secret"}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Create(ctx, "org_b", keystore.KindProvider, keystore.Put{Provider: "anthropic", Value: "sk-ant-org-b"}, "u"); err != nil {
		t.Fatal(err)
	}
	return ks
}

func callCreds(h *leaseCredsHandler, p *auth.Principal, lease string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	if p != nil {
		auth.SetPrincipal(ctx, p)
	}
	ctx.SetUserValue("lease_id", lease)
	h.get(ctx)
	return ctx
}

func TestLeaseCredentials(t *testing.T) {
	ks := credsStore(t)
	holders := fakeHolders{
		"l_mine":  {WorkerID: "w1", TokenID: "rtk_a1", OrgID: "org_a", ExpID: "exp_1"},
		"l_other": {WorkerID: "w2", TokenID: "rtk_a2", OrgID: "org_a"},
	}
	h := &leaseCredsHandler{mode: schemas.ModeLive, orgID: "org_a", keys: ks,
		holders: func(string) LeaseHolders { return holders }, now: time.Now}
	runnerA := &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a", TokenID: "rtk_a1"}

	ctx := callCreds(h, runnerA, "l_mine")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if string(ctx.Response.Header.Peek("Cache-Control")) != "no-store" {
		t.Error("credentials answer is cacheable")
	}
	var out schemas.LeaseCredentials
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if out.OrgID != "org_a" || out.ExpID != "exp_1" || len(out.Credentials) != 1 {
		t.Fatalf("answer %+v", out)
	}
	if c := out.Credentials[0]; c.Provider != "anthropic" || c.Value != "sk-ant-org-a" || len(c.Models) != 1 {
		t.Fatalf("credential %+v", c)
	}
	if out.PromptLogging == nil || *out.PromptLogging != schemas.DefaultPromptLogPolicy() { // X03
		t.Errorf("prompt logging = %+v", out.PromptLogging)
	}
	body := string(ctx.Response.Body())
	for _, leak := range []string{"sk-ant-org-b", "ghp_secret", "sk-oa-over"} {
		if strings.Contains(body, leak) {
			t.Errorf("answer leaks %s", leak)
		}
	}

	cases := []struct {
		name  string
		p     *auth.Principal
		lease string
		want  int
	}{
		{"runner of another org", &auth.Principal{Kind: auth.KindRunner, OrgID: "org_b", TokenID: "rtk_b1"}, "l_mine", 403},
		{"owner user", &auth.Principal{Kind: auth.KindUser, OrgID: "org_a", Role: auth.RoleOwner}, "l_mine", 403},
		{"anonymous", nil, "l_mine", 403},
		{"lease of another runner", runnerA, "l_other", 403},
		{"unknown lease", runnerA, "l_gone", 404},
	}
	for _, c := range cases {
		if got := callCreds(h, c.p, c.lease).Response.StatusCode(); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}

	noKeys := &leaseCredsHandler{mode: schemas.ModeLive, orgID: "org_a", holders: h.holders, now: time.Now}
	if got := callCreds(noKeys, runnerA, "l_mine").Response.StatusCode(); got != 503 {
		t.Errorf("no key store: %d", got)
	}
	noHub := &leaseCredsHandler{mode: schemas.ModeLive, orgID: "org_a", keys: ks, now: time.Now}
	if got := callCreds(noHub, runnerA, "l_mine").Response.StatusCode(); got != 503 {
		t.Errorf("no coordinator: %d", got)
	}
}
