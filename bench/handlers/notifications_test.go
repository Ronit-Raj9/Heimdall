package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/mail"
	"github.com/maximhq/bifrost/bench/notify"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func notifyCall(r *router.Router, p *auth.Principal, method, uri, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	ctx.Request.SetBodyString(body)
	if p != nil {
		auth.SetPrincipal(ctx, p)
	}
	r.Handler(ctx)
	return ctx
}

func TestEmailSettingsRoutes(t *testing.T) {
	root := t.TempDir()
	r := router.New()
	for _, a := range OrgAreas() {
		a(OrgEnv{Cfg: &lib.Config{Root: root}, Mode: schemas.ModeRecorded}, r)
	}
	p := APIPrefix + "/notifications/email"
	ana := &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Email: "ana@example.com", Role: auth.RoleViewer}

	// No hub (a local server): not available, and writes are refused.
	notify.SetDefault(nil)
	ctx := notifyCall(r, ana, "GET", p, "")
	var v EmailSettings
	_ = json.Unmarshal(ctx.Response.Body(), &v)
	if ctx.Response.StatusCode() != 200 || v.Available || v.Reason == "" || len(v.Events) != len(notify.EmailEvents) {
		t.Fatalf("no hub: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if ctx := notifyCall(r, ana, "PUT", p, `{"events":[],"digest":true}`); ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("put without hub %d", ctx.Response.StatusCode())
	}

	log := mail.NewLog(nil)
	notify.SetDefault(&notify.Hub{Mail: log, Members: func(context.Context, string) ([]notify.Member, error) { return nil, nil }})
	t.Cleanup(func() { notify.SetDefault(nil) })
	ctx = notifyCall(r, ana, "GET", p, "")
	v = EmailSettings{}
	_ = json.Unmarshal(ctx.Response.Body(), &v)
	if !v.Available || v.Delivers || v.Provider != mail.ProviderLog || v.Email != "ana@example.com" || !v.Prefs.Digest {
		t.Fatalf("get %s", ctx.Response.Body())
	}
	ctx = notifyCall(r, ana, "PUT", p, `{"events":["evaluation.regressed","release.blocked"],"digest":false}`)
	v = EmailSettings{}
	_ = json.Unmarshal(ctx.Response.Body(), &v)
	if ctx.Response.StatusCode() != 200 || len(v.Prefs.Events) != 2 || v.Prefs.Digest {
		t.Fatalf("put %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if ctx := notifyCall(r, ana, "PUT", p, `{"events":["job.failed"]}`); ctx.Response.StatusCode() != 400 {
		t.Fatalf("bad event %d", ctx.Response.StatusCode())
	}
	if ctx := notifyCall(r, ana, "PUT", p, `{"user_id":"u2"}`); ctx.Response.StatusCode() != 400 {
		t.Fatalf("unknown field %d", ctx.Response.StatusCode())
	}
	// Each member has their own choice.
	bo := &auth.Principal{Kind: auth.KindUser, UserID: "u2", OrgID: "org1", Email: "bo@example.com"}
	ctx = notifyCall(r, bo, "GET", p, "")
	v = EmailSettings{}
	_ = json.Unmarshal(ctx.Response.Body(), &v)
	if len(v.Prefs.Events) != 0 || !v.Prefs.Digest {
		t.Fatalf("bo sees ana's choice: %s", ctx.Response.Body())
	}
	ctx = notifyCall(r, ana, "POST", p+"/test", "")
	if ctx.Response.StatusCode() != 200 || len(log.Recent()) != 1 || log.Recent()[0].To[0] != "ana@example.com" {
		t.Fatalf("test %d %s %+v", ctx.Response.StatusCode(), ctx.Response.Body(), log.Recent())
	}
	// An API token has no address.
	tok := &auth.Principal{Kind: auth.KindToken, OrgID: "org1"}
	if ctx := notifyCall(r, tok, "POST", p+"/test", ""); ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("token test %d", ctx.Response.StatusCode())
	}
}
