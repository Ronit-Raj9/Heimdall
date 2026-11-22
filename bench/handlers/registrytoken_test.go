package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
)

func registryTokenFixture(t *testing.T) (*RegistryTokenHandler, *imagepin.Issuer) {
	t.Helper()
	dir := t.TempDir()
	if err := imagepin.GenerateTokenKey(dir, ""); err != nil {
		t.Fatal(err)
	}
	iss, err := imagepin.LoadIssuer(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("sync-password-0123456789"), bcrypt.MinCost)
	policy := &imagepin.AccessPolicy{Namespace: "library", Restricted: map[string][]string{"gaia": {"org-allowed"}}}
	resolve := func(_ context.Context, secret string) (string, error) {
		switch secret {
		case "hm_good":
			return "org-1", nil
		case "hmr_allowed":
			return "org-allowed", nil
		}
		return "", errors.New("bad")
	}
	return NewRegistryTokenHandler(schemas.ModeLive, iss, policy, resolve, "sync:"+string(hash), ""), iss
}

func tokenReq(h *RegistryTokenHandler, method, user, pass, query string, form map[string]string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(RegistryTokenPath + "?" + query)
	if user != "" || pass != "" {
		ctx.Request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	}
	if form != nil {
		args := fasthttp.AcquireArgs()
		for k, v := range form {
			args.Set(k, v)
		}
		ctx.Request.Header.SetContentType("application/x-www-form-urlencoded")
		ctx.Request.SetBody(args.QueryString())
		h.post(ctx)
		return ctx
	}
	h.get(ctx)
	return ctx
}

func grantedOf(t *testing.T, ctx *fasthttp.RequestCtx, iss *imagepin.Issuer) []imagepin.Access {
	t.Helper()
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var out RegistryTokenResponse
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	c, err := imagepin.VerifyToken(out.AccessToken, iss.CertDER())
	if err != nil {
		t.Fatal(err)
	}
	return c.Access
}

func TestRegistryTokenGrants(t *testing.T) {
	h, iss := registryTokenFixture(t)
	q := "service=heimdall-library&scope=repository:library/open/x:pull,push&scope=repository:library/gaia/y:pull"
	org := grantedOf(t, tokenReq(h, "GET", "heimdall", "hm_good", q, nil), iss)
	if len(org) != 1 || org[0].Name != "library/open/x" || len(org[0].Actions) != 1 || org[0].Actions[0] != "pull" {
		t.Fatalf("org grant %+v", org)
	}
	allowed := grantedOf(t, tokenReq(h, "GET", "x", "hmr_allowed", q, nil), iss)
	if len(allowed) != 2 {
		t.Fatalf("allowed org grant %+v", allowed)
	}
	sync := grantedOf(t, tokenReq(h, "GET", "sync", "sync-password-0123456789", q, nil), iss)
	if len(sync) != 2 || len(sync[0].Actions) != 2 {
		t.Fatalf("sync grant %+v", sync)
	}
	// containerd's OAuth2 password grant.
	post := grantedOf(t, tokenReq(h, "POST", "", "", "", map[string]string{"grant_type": "password", "username": "u", "password": "hm_good",
		"service": "heimdall-library", "scope": "repository:library/open/x:pull"}), iss)
	if len(post) != 1 {
		t.Fatalf("post grant %+v", post)
	}
	// Login (no scope): a token with no access.
	if g := grantedOf(t, tokenReq(h, "GET", "heimdall", "hm_good", "service=heimdall-library", nil), iss); len(g) != 0 {
		t.Fatalf("login token grants %+v", g)
	}
}

func TestRegistryTokenRefusals(t *testing.T) {
	h, _ := registryTokenFixture(t)
	for name, ctx := range map[string]*fasthttp.RequestCtx{
		"no credentials":   tokenReq(h, "GET", "", "", "scope=repository:library/open/x:pull", nil),
		"bad token":        tokenReq(h, "GET", "heimdall", "hm_bad", "", nil),
		"bad sync":         tokenReq(h, "GET", "sync", "wrong", "", nil),
		"sync user as org": tokenReq(h, "GET", "sync", "hm_good", "", nil),
	} {
		if ctx.Response.StatusCode() != 401 || len(ctx.Response.Header.Peek("WWW-Authenticate")) == 0 {
			t.Errorf("%s: status %d", name, ctx.Response.StatusCode())
		}
	}
	if c := tokenReq(h, "GET", "heimdall", "hm_good", "service=other", nil); c.Response.StatusCode() != 400 {
		t.Errorf("other service: %d", c.Response.StatusCode())
	}
	if c := tokenReq(h, "POST", "", "", "", map[string]string{"grant_type": "refresh_token"}); c.Response.StatusCode() != 400 {
		t.Errorf("refresh grant: %d", c.Response.StatusCode())
	}
	h.FailuresPerMinute = 3
	h.fails = map[string][]time.Time{}
	for i := 0; i < 3; i++ {
		tokenReq(h, "GET", "heimdall", "hm_bad", "", nil)
	}
	if c := tokenReq(h, "GET", "heimdall", "hm_good", "", nil); c.Response.StatusCode() != 429 {
		t.Errorf("after refused credentials: %d, want 429", c.Response.StatusCode())
	}
	off := NewRegistryTokenHandler(schemas.ModeLive, nil, nil, nil, "", "not configured")
	if c := tokenReq(off, "GET", "heimdall", "hm_good", "", nil); c.Response.StatusCode() != 503 {
		t.Errorf("off: %d", c.Response.StatusCode())
	}
}
