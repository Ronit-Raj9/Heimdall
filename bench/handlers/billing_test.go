package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/billing"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

type billOrgs struct{ members, invited int }

func (billOrgs) OrgName(_ context.Context, id string) (string, error) {
	if id != "org1" {
		return "", errors.New("no such org")
	}
	return "Acme", nil
}
func (o billOrgs) Seats(context.Context, string) (int, int, error)  { return o.members, o.invited, nil }
func (billOrgs) OwnerEmail(context.Context, string) (string, error) { return "o@acme.test", nil }

const billWhsec = "whsec_handler_test"

func billService(t *testing.T, enabled bool, stripeURL string, orgs billOrgs) *billing.Service {
	t.Helper()
	cfg := billing.Config{Enabled: enabled, SecretKey: "sk_test_h", WebhookSecret: billWhsec, TeamPrice: "price_team", APIBase: stripeURL}
	return &billing.Service{Cfg: cfg, Store: billing.NewStore(dbtest.OpenSQLite(t)), Stripe: billing.NewStripe(cfg), Orgs: orgs}
}

func billServe(t *testing.T, h *BillingHandler, p *auth.Principal, method, path, body string, hdr map[string]string) (int, string) {
	t.Helper()
	r := router.New()
	h.RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	if body != "" {
		ctx.Request.SetBodyString(body)
	}
	for k, v := range hdr {
		ctx.Request.Header.Set(k, v)
	}
	if p != nil {
		auth.SetPrincipal(ctx, p)
	}
	r.Handler(ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

var (
	billAdmin  = &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleAdmin, Email: "a@acme.test"}
	billViewer = &auth.Principal{Kind: auth.KindUser, UserID: "u2", OrgID: "org1", Role: auth.RoleViewer, Email: "v@acme.test"}
)

func TestBillingRoutesOff(t *testing.T) {
	h := NewBillingHandler(schemas.ModeLive, billService(t, false, "http://127.0.0.1:1", billOrgs{members: 3}), "https://h.test", nil)
	code, body := billServe(t, h, billViewer, "GET", "/api/bench/billing", "", nil)
	if code != 200 {
		t.Fatalf("view: %d %s", code, body)
	}
	var v billing.View
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if v.Enabled || v.Plan.ID != billing.PlanFree || v.CheckoutReady || len(v.Meters) == 0 || len(v.Features) != len(billing.AllFeatures) {
		t.Fatalf("view off: %+v", v)
	}
	for _, m := range v.Meters {
		if m.ID == "seats" && (m.Used != 3 || m.Limit != 3 || !m.Reached || m.Enforced) {
			t.Fatalf("seats shown, not enforced: %+v", m)
		}
	}
	if code, body := billServe(t, h, billAdmin, "POST", "/api/bench/billing/checkout", "", nil); code != 503 || !strings.Contains(body, billing.EnabledEnv) {
		t.Fatalf("checkout off: %d %s", code, body)
	}
	if code, _ := billServe(t, h, billViewer, "POST", "/api/bench/billing/checkout", "", nil); code != 403 {
		t.Fatalf("viewer checkout: %d", code)
	}
	if code, _ := billServe(t, h, nil, "GET", "/api/bench/billing", "", nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	if code, _ := billServe(t, h, nil, "POST", "/api/bench/billing/webhook", `{}`, nil); code != 503 {
		t.Fatalf("webhook off: %d", code)
	}
	code, body = billServe(t, h, nil, "GET", PublicPricingPath, "", nil)
	if code != 200 || !strings.Contains(body, `"team"`) || strings.Contains(body, "sk_test") {
		t.Fatalf("pricing: %d %s", code, body)
	}
	if code, body := billServe(t, h, billViewer, "GET", "/api/bench/billing/usage?from=2026-11-01&to=2026-11-03", "", nil); code != 200 || !strings.Contains(body, `"days"`) {
		t.Fatalf("usage: %d %s", code, body)
	}
	if code, _ := billServe(t, h, billViewer, "GET", "/api/bench/billing/usage?from=bad", "", nil); code != 400 {
		t.Fatalf("bad usage range: %d", code)
	}
	// Billing off: the gate never refuses.
	gate := PlanGate(h.svc)
	ctx := &fasthttp.RequestCtx{}
	if !gate(ctx, schemas.ModeLive, "org1", "POST", APIPrefix+"/suites") {
		t.Fatal("gate refused while billing is off")
	}
}

func TestBillingRoutesOn(t *testing.T) {
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/customers":
			fmt.Fprint(w, `{"id":"cus_h"}`)
		case "/v1/checkout/sessions":
			fmt.Fprint(w, `{"id":"cs_h","url":"https://checkout.stripe.test/cs_h"}`)
		case "/v1/billing_portal/sessions":
			fmt.Fprint(w, `{"url":"https://billing.stripe.test/p"}`)
		case "/v1/invoices":
			fmt.Fprint(w, `{"data":[]}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"message":"no route"}}`)
		}
	}))
	defer stripe.Close()
	svc := billService(t, true, stripe.URL, billOrgs{members: 2, invited: 1})
	var audited []string
	h := NewBillingHandler(schemas.ModeLive, svc, "https://h.test", func(_ *fasthttp.RequestCtx, _, action, _, _ string, _ map[string]any) {
		audited = append(audited, action)
	}).WithAdminCheck(func(p *auth.Principal) bool { return p.Email == "root@h.test" })

	if code, body := billServe(t, h, billAdmin, "POST", "/api/bench/billing/portal", "", nil); code != 409 || !strings.Contains(body, "upgrade to Team first") {
		t.Fatalf("portal before checkout: %d %s", code, body)
	}
	code, body := billServe(t, h, billAdmin, "POST", "/api/bench/billing/checkout", "", nil)
	if code != 200 || !strings.Contains(body, "https://checkout.stripe.test/cs_h") {
		t.Fatalf("checkout: %d %s", code, body)
	}
	if code, body := billServe(t, h, billAdmin, "POST", "/api/bench/billing/portal", "", nil); code != 200 || !strings.Contains(body, "billing.stripe.test") {
		t.Fatalf("portal: %d %s", code, body)
	}
	if code, body := billServe(t, h, billAdmin, "GET", "/api/bench/billing/invoices", "", nil); code != 200 || !strings.Contains(body, `"invoices":[]`) {
		t.Fatalf("invoices: %d %s", code, body)
	}
	if code, _ := billServe(t, h, billViewer, "GET", "/api/bench/billing/invoices", "", nil); code != 403 {
		t.Fatalf("viewer invoices: %d", code)
	}
	if len(audited) != 2 || audited[0] != "billing.checkout" {
		t.Fatalf("audit: %v", audited)
	}

	// Webhook: unsigned and badly signed deliveries are refused; a signed one applies.
	payload := `{"id":"evt_h1","type":"customer.subscription.updated","created":` + fmt.Sprint(time.Now().Unix()) +
		`,"data":{"object":{"id":"sub_h","status":"active","customer":"cus_h","metadata":{"org_id":"org1"},"items":{"data":[{"id":"si_h","quantity":2}]}}}}`
	if code, _ := billServe(t, h, nil, "POST", "/api/bench/billing/webhook", payload, nil); code != 400 {
		t.Fatalf("unsigned webhook: %d", code)
	}
	bad := billing.SignatureFor([]byte(payload), "whsec_other", time.Now().Unix())
	if code, _ := billServe(t, h, nil, "POST", "/api/bench/billing/webhook", payload, map[string]string{billing.SignatureHeader: bad}); code != 400 {
		t.Fatalf("bad signature: %d", code)
	}
	good := billing.SignatureFor([]byte(payload), billWhsec, time.Now().Unix())
	if code, body := billServe(t, h, nil, "POST", "/api/bench/billing/webhook", payload, map[string]string{billing.SignatureHeader: good}); code != 200 || !strings.Contains(body, `"applied":true`) {
		t.Fatalf("signed webhook: %d %s", code, body)
	}
	code, body = billServe(t, h, billViewer, "GET", "/api/bench/billing", "", nil)
	if code != 200 || !strings.Contains(body, `"id":"team"`) || !strings.Contains(body, `"portal_ready":true`) {
		t.Fatalf("team after webhook: %d %s", code, body)
	}

	// Admin plan route.
	if code, _ := billServe(t, h, billAdmin, "PUT", "/api/bench/admin/billing/org1", `{"plan":"enterprise"}`, nil); code != 403 {
		t.Fatalf("org admin is not a server admin: %d", code)
	}
	root := &auth.Principal{Kind: auth.KindUser, UserID: "r", Email: "root@h.test"}
	if code, _ := billServe(t, h, root, "PUT", "/api/bench/admin/billing/ghost", `{"plan":"enterprise"}`, nil); code != 404 {
		t.Fatalf("unknown org: %d", code)
	}
	if code, _ := billServe(t, h, root, "PUT", "/api/bench/admin/billing/org1", `{"plan":"gold"}`, nil); code != 400 {
		t.Fatalf("unknown plan: %d", code)
	}
	if code, body := billServe(t, h, root, "PUT", "/api/bench/admin/billing/org1", `{"plan":"free"}`, nil); code != 200 || !strings.Contains(body, `"effective_plan":"free"`) {
		t.Fatalf("set free: %d %s", code, body)
	}

	// Free with billing on: the gate answers 402 plan_limit with the Billing link.
	gate := PlanGate(svc)
	for _, k := range []string{"POST " + APIPrefix + "/suites", "POST " + APIPrefix + "/github/ci-install", "PUT " + APIPrefix + "/schedules", SeatGate} {
		method, pat, _ := strings.Cut(k, " ")
		ctx := &fasthttp.RequestCtx{}
		if gate(ctx, schemas.ModeLive, "org1", method, pat) {
			t.Fatalf("%s passed on Free", k)
		}
		if ctx.Response.StatusCode() != 402 || !strings.Contains(string(ctx.Response.Body()), `"type":"plan_limit"`) ||
			!strings.Contains(string(ctx.Response.Body()), BillingLink) {
			t.Fatalf("%s: %d %s", k, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	if !gate(&fasthttp.RequestCtx{}, schemas.ModeLive, "org1", "GET", APIPrefix+"/suites") {
		t.Fatal("reads are never gated")
	}
	if !gate(&fasthttp.RequestCtx{}, schemas.ModeLive, "org1", "POST", APIPrefix+"/evaluations") {
		t.Fatal("evaluations are not a plan feature")
	}
	if _, err := svc.SetPlan(context.Background(), "org1", billing.PlanTeam, "root"); err != nil {
		t.Fatal(err)
	}
	if !gate(&fasthttp.RequestCtx{}, schemas.ModeLive, "org1", "POST", APIPrefix+"/suites") {
		t.Fatal("team includes private benchmarks")
	}
}

// TestPlanLimitThroughDepError: a launch refused by the plan answers 402.
func TestPlanLimitThroughDepError(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	sendDepError(ctx, schemas.ModeLive, "start", &billing.LimitError{Limit: billing.MeterSharedWorkerMinutes, Plan: "free", Upgrade: "team", Msg: "used up"})
	if ctx.Response.StatusCode() != 402 || !strings.Contains(string(ctx.Response.Body()), "plan_limit") {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

// TestPlanGatesArePolicyRoutes: every gated route is a real policy entry,
// so a renamed route can not silently drop out of the gate.
func TestPlanGatesArePolicyRoutes(t *testing.T) {
	pol := tenancyPolicy()
	for k := range PlanGates {
		method, pat, _ := strings.Cut(k, " ")
		if !pol(method, pat) {
			t.Errorf("gated route %s has no policy entry", k)
		}
	}
	if m, p, _ := strings.Cut(SeatGate, " "); !pol(m, p) {
		t.Errorf("seat gate %s has no policy entry", SeatGate)
	}
}

func tenancyPolicy() func(method, pattern string) bool {
	pol := tenancy.DefaultPolicy()
	return func(method, pattern string) bool {
		if _, ok := pol.PlatformRule(method, pattern); ok {
			return true
		}
		_, ok := pol.OrgRule(method, pattern)
		return ok
	}
}
