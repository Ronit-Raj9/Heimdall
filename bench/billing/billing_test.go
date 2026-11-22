package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
)

const whsec = "whsec_test_secret"

// fakeStripe is an httptest Stripe that records requests and answers the
// calls billing makes.
type fakeStripe struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	calls    []string
	forms    map[string]url.Values
	subs     map[string]map[string]any
	seatsSet map[string]int
	fail     string // path prefix answered 500
}

func newFakeStripe(t *testing.T) *fakeStripe {
	f := &fakeStripe{t: t, forms: map[string]url.Values{}, subs: map[string]map[string]any{}, seatsSet: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStripe) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer sk_test_123" {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"Invalid API Key provided"}}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))
	if r.Method == http.MethodGet {
		form = r.URL.Query()
	}
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.forms[r.Method+" "+r.URL.Path] = form
	fail := f.fail
	f.mu.Unlock()
	if fail != "" && strings.HasPrefix(r.URL.Path, fail) {
		w.WriteHeader(500)
		io.WriteString(w, `{"error":{"type":"api_error","message":"boom"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/customers":
		fmt.Fprintf(w, `{"id":"cus_1","name":%q}`, form.Get("name"))
	case r.Method == "POST" && r.URL.Path == "/v1/checkout/sessions":
		io.WriteString(w, `{"id":"cs_1","url":"https://checkout.stripe.test/cs_1"}`)
	case r.Method == "POST" && r.URL.Path == "/v1/billing_portal/sessions":
		io.WriteString(w, `{"url":"https://billing.stripe.test/p_1"}`)
	case r.Method == "GET" && r.URL.Path == "/v1/invoices":
		io.WriteString(w, `{"data":[{"id":"in_1","number":"A-0001","status":"paid","amount_due":7800,"amount_paid":7800,"currency":"usd","created":1763800000,"hosted_invoice_url":"https://pay.stripe.test/in_1","invoice_pdf":"https://pay.stripe.test/in_1.pdf"}]}`)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/subscriptions/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/subscriptions/")
		f.mu.Lock()
		sub, ok := f.subs[id]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":{"message":"No such subscription"}}`)
			return
		}
		json.NewEncoder(w).Encode(sub)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/subscription_items/"):
		var n int
		fmt.Sscan(form.Get("quantity"), &n)
		f.mu.Lock()
		f.seatsSet[strings.TrimPrefix(r.URL.Path, "/v1/subscription_items/")] = n
		f.mu.Unlock()
		io.WriteString(w, `{}`)
	default:
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"message":"unknown route"}}`)
	}
}

func (f *fakeStripe) addSub(id, status string, seats int, org string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id] = map[string]any{"id": id, "status": status, "customer": "cus_1", "metadata": map[string]string{"org_id": org},
		"items": map[string]any{"data": []any{map[string]any{"id": "si_1", "quantity": seats, "current_period_end": 1766000000, "price": map[string]any{"id": "price_team"}}}}}
}

func (f *fakeStripe) form(key string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forms[key]
}

// fakeOrgs is the platform view billing reads.
type fakeOrgs struct {
	members, invited int
	known            map[string]bool
}

func (o *fakeOrgs) OrgName(_ context.Context, id string) (string, error) {
	if o.known != nil && !o.known[id] {
		return "", errors.New("not found")
	}
	return "Acme " + id, nil
}
func (o *fakeOrgs) Seats(context.Context, string) (int, int, error)    { return o.members, o.invited, nil }
func (o *fakeOrgs) OwnerEmail(context.Context, string) (string, error) { return "owner@acme.test", nil }

type fakeData struct{}

func (fakeData) RunsByDay(context.Context, string, time.Time, time.Time) (map[string]int, error) {
	return map[string]int{"2026-11-20": 4, "2026-11-21": 2}, nil
}
func (fakeData) EvaluationsByDay(context.Context, string, time.Time, time.Time) (map[string]int, error) {
	return map[string]int{"2026-11-21": 1}, nil
}
func (fakeData) StorageBytes(context.Context, string) (int64, error) { return 12345, nil }

var testNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T, enabled bool, fs *fakeStripe) (*Service, *fakeOrgs) {
	t.Helper()
	d := dbtest.OpenSQLite(t)
	cfg := Config{Enabled: enabled, SecretKey: "sk_test_123", WebhookSecret: whsec, TeamPrice: "price_team", APIBase: DefaultAPIBase}
	if fs != nil {
		cfg.APIBase = fs.srv.URL
	}
	orgs := &fakeOrgs{members: 2, known: map[string]bool{"org1": true, "org2": true}}
	st := NewStore(d)
	st.Now = func() time.Time { return testNow }
	svc := &Service{Cfg: cfg, Store: st, Stripe: NewStripe(cfg), Orgs: orgs, Data: fakeData{}, Now: func() time.Time { return testNow }}
	return svc, orgs
}

func TestPlansAndConfig(t *testing.T) {
	if PlanByID("nope").ID != PlanFree || !PlanByID(PlanTeam).Has(FeatureCI) || PlanByID(PlanFree).Has(FeatureCI) {
		t.Fatal("plans")
	}
	for _, p := range Plans {
		if !p.OwnKey {
			t.Fatalf("%s: every plan uses the org's own key", p.ID)
		}
	}
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	c := ConfigFrom(get)
	if c.Enabled || c.APIBase != DefaultAPIBase || c.Mode() != "" || !errors.Is(c.StripeReady(), ErrBillingOff) {
		t.Fatalf("defaults: %+v", c)
	}
	env[EnabledEnv] = "on"
	if c = ConfigFrom(get); !c.Enabled || !errors.Is(c.StripeReady(), ErrNoStripe) {
		t.Fatalf("on without stripe: %+v", c)
	}
	env[SecretKeyEnv], env[TeamPriceEnv] = "sk_test_x", "price_x"
	if c = ConfigFrom(get); c.Mode() != ModeTest || c.StripeReady() != nil {
		t.Fatalf("test key: %v", c.StripeReady())
	}
	env[SecretKeyEnv] = "sk_live_x"
	if c = ConfigFrom(get); c.Mode() != ModeLive || !errors.Is(c.StripeReady(), ErrLiveKey) {
		t.Fatalf("live key refused by default: %v", c.StripeReady())
	}
	env[AllowLiveEnv] = "1"
	if c = ConfigFrom(get); c.StripeReady() != nil {
		t.Fatalf("live allowed: %v", c.StripeReady())
	}
	if err := c.WebhookReady(); err == nil {
		t.Fatal("webhook without secret")
	}
}

func TestEffectivePlan(t *testing.T) {
	cases := map[Account]string{
		{Plan: PlanTeam, Status: "active"}:   PlanTeam,
		{Plan: PlanTeam, Status: "past_due"}: PlanTeam,
		{Plan: PlanTeam, Status: "canceled"}: PlanFree,
		{Plan: PlanTeam, Status: ""}:         PlanFree,
		{Plan: PlanTeam, Status: "manual"}:   PlanTeam,
		{Plan: PlanEnterprise}:               PlanEnterprise,
		{Plan: PlanFree, Status: "active"}:   PlanFree,
	}
	for a, want := range cases {
		if got := a.EffectivePlan(); got != want {
			t.Errorf("%+v: %s, want %s", a, got, want)
		}
	}
}

func TestStoreAccountsAndMeters(t *testing.T) {
	svc, _ := newService(t, false, nil)
	ctx := context.Background()
	a, err := svc.Store.Account(ctx, "org1")
	if err != nil || a.Plan != PlanFree {
		t.Fatalf("no row is free: %+v %v", a, err)
	}
	if err := svc.Store.PutAccount(ctx, Account{OrgID: "org1", Plan: "gold"}); err == nil {
		t.Fatal("unknown plan stored")
	}
	a = Account{OrgID: "org1", Plan: PlanTeam, Status: "active", CustomerID: "cus_9", SubscriptionID: "sub_9", Seats: 4, PeriodEnd: testNow.Add(time.Hour).Truncate(time.Millisecond)}
	if err := svc.Store.PutAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store.Account(ctx, "org1")
	if got.Seats != 4 || got.CustomerID != "cus_9" || !got.PeriodEnd.Equal(a.PeriodEnd) {
		t.Fatalf("round trip: %+v", got)
	}
	if by, ok, _ := svc.Store.AccountByCustomer(ctx, "cus_9"); !ok || by.OrgID != "org1" {
		t.Fatal("by customer")
	}
	day := testNow
	if err := svc.RecordLease(ctx, "org1", day.Add(-30*time.Minute), day, 1); err != nil {
		t.Fatal(err)
	}
	_ = svc.RecordLease(ctx, "org1", day.Add(-15*time.Minute), day, 2)
	_ = svc.RecordLease(ctx, "org1", day, day.Add(-time.Minute), 1)                     // backwards: nothing
	_ = svc.RecordLease(ctx, "org1", day.Add(-100*time.Hour), day.AddDate(0, -1, 0), 1) // last month, capped
	m, err := svc.SharedMinutesThisMonth(ctx, "org1")
	if err != nil || m != 60 {
		t.Fatalf("minutes this month: %v %v", m, err)
	}
	_ = svc.Store.SetMeter(ctx, "org1", day, MeterStorageBytes, 10)
	_ = svc.Store.SetMeter(ctx, "org1", day, MeterStorageBytes, 20)
	ms, _ := svc.Store.Meters(ctx, "org1", day, day)
	if ms[day.Format(DayLayout)][MeterStorageBytes] != 20 {
		t.Fatalf("gauge keeps the newest reading: %v", ms)
	}
	if fresh, _ := svc.Store.ClaimEvent(ctx, "evt_1", "x"); !fresh {
		t.Fatal("first claim")
	}
	if fresh, _ := svc.Store.ClaimEvent(ctx, "evt_1", "x"); fresh {
		t.Fatal("second claim is a redelivery")
	}
}

func TestUsageReport(t *testing.T) {
	svc, _ := newService(t, false, nil)
	ctx := context.Background()
	_ = svc.RecordLease(ctx, "org1", testNow.Add(-10*time.Minute), testNow, 1)
	rep, err := svc.Usage(ctx, "org1", testNow.AddDate(0, 0, -2), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Days) != 3 || rep.Totals.Runs != 6 || rep.Totals.Evaluations != 1 || rep.Totals.SharedWorkerMinutes != 10 {
		t.Fatalf("report: %+v", rep)
	}
	last := rep.Days[2]
	if last.Day != "2026-11-22" || last.StorageBytes == nil || *last.StorageBytes != 12345 || rep.StorageBytes == nil {
		t.Fatalf("today's storage reading: %+v", last)
	}
	if _, err := svc.Usage(ctx, "org1", testNow, testNow.AddDate(0, 0, -1)); err == nil {
		t.Fatal("reversed range")
	}
}

func TestLimitsOnlyWhenBillingOn(t *testing.T) {
	ctx := context.Background()
	off, orgs := newService(t, false, nil)
	orgs.members, orgs.invited = 3, 0
	_ = off.RecordLease(ctx, "org1", testNow.Add(-6*time.Hour), testNow, 1)
	if off.CheckFeature(ctx, "org1", FeatureCI) != nil || off.CheckSeats(ctx, "org1") != nil || off.CheckSharedMinutes(ctx, "org1") != nil {
		t.Fatal("billing off must never refuse")
	}
	v, err := off.View(ctx, "org1")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range v.Meters {
		if m.Enforced {
			t.Fatalf("%s enforced with billing off", m.ID)
		}
		if m.ID == MeterSharedWorkerMinutes && (!m.Reached || m.Limit != 300 || m.Used != 360) {
			t.Fatalf("limit shown even when off: %+v", m)
		}
	}
	if v.CheckoutReady || v.CheckoutBlocker == "" {
		t.Fatal("checkout refused while off")
	}

	on, orgs := newService(t, true, nil)
	orgs.members, orgs.invited = 2, 1
	_ = on.RecordLease(ctx, "org1", testNow.Add(-6*time.Hour), testNow, 1)
	for _, err := range []error{on.CheckFeature(ctx, "org1", FeaturePrivateBenchmarks), on.CheckSeats(ctx, "org1"), on.CheckSharedMinutes(ctx, "org1")} {
		var le *LimitError
		if !errors.Is(err, ErrPlanLimit) || !errors.As(err, &le) || le.Upgrade != PlanTeam || le.Plan != PlanFree {
			t.Fatalf("free plan refusal: %v", err)
		}
	}
	if _, err := on.SetPlan(ctx, "org1", PlanTeam, "admin@x"); err != nil {
		t.Fatal(err)
	}
	if on.CheckFeature(ctx, "org1", FeaturePrivateBenchmarks) != nil || on.CheckSeats(ctx, "org1") != nil || on.CheckSharedMinutes(ctx, "org1") != nil {
		t.Fatal("team plan covers these")
	}
	if _, err := on.SetPlan(ctx, "org1", PlanEnterprise, "admin@x"); err != nil {
		t.Fatal(err)
	}
	_ = on.RecordLease(ctx, "org1", testNow.Add(-47*time.Hour), testNow, 64)
	if on.CheckSharedMinutes(ctx, "org1") != nil {
		t.Fatal("enterprise has no minutes limit")
	}
	if _, err := on.SetPlan(ctx, "org1", "gold", "admin@x"); err == nil {
		t.Fatal("unknown plan")
	}
}

func TestSignature(t *testing.T) {
	payload := []byte(`{"id":"evt_1","type":"x"}`)
	ts := testNow.Unix()
	good := SignatureFor(payload, whsec, ts)
	if err := VerifySignature(payload, good, whsec, testNow, SignatureTolerance); err != nil {
		t.Fatalf("good: %v", err)
	}
	// Stripe sends several v1 values while a secret is rolled.
	multi := fmt.Sprintf("t=%d,v1=%s,%s", ts, strings.Repeat("ab", 32), strings.TrimPrefix(good, fmt.Sprintf("t=%d,", ts)))
	if err := VerifySignature(payload, multi, whsec, testNow, SignatureTolerance); err != nil {
		t.Fatalf("rolled secret: %v", err)
	}
	bad := []struct{ name, header, secret string }{
		{"wrong secret", SignatureFor(payload, "whsec_other", ts), whsec},
		{"stale", SignatureFor(payload, whsec, ts-int64(10*time.Minute/time.Second)), whsec},
		{"no v1", fmt.Sprintf("t=%d", ts), whsec},
		{"no t", "v1=" + strings.Repeat("00", 32), whsec},
		{"empty", "", whsec},
		{"no secret", good, ""},
	}
	for _, b := range bad {
		if err := VerifySignature(payload, b.header, b.secret, testNow, SignatureTolerance); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", b.name, err)
		}
	}
	if err := VerifySignature([]byte(`{"id":"evt_2"}`), good, whsec, testNow, SignatureTolerance); !errors.Is(err, ErrSignature) {
		t.Error("tampered body accepted")
	}
}

func deliver(t *testing.T, svc *Service, id, typ string, created int64, object any) (*WebhookResult, error) {
	t.Helper()
	obj, _ := json.Marshal(object)
	payload := []byte(fmt.Sprintf(`{"id":%q,"type":%q,"created":%d,"data":{"object":%s}}`, id, typ, created, obj))
	return svc.HandleWebhook(context.Background(), payload, SignatureFor(payload, whsec, testNow.Unix()))
}

func TestCheckoutWebhookAndPortal(t *testing.T) {
	fs := newFakeStripe(t)
	svc, orgs := newService(t, true, fs)
	orgs.members = 3
	ctx := context.Background()

	u, err := svc.Checkout(ctx, "org1", "user1", "https://heimdall.test/")
	if err != nil || u != "https://checkout.stripe.test/cs_1" {
		t.Fatalf("checkout: %q %v", u, err)
	}
	cf := fs.form("POST /v1/customers")
	if cf.Get("metadata[org_id]") != "org1" || cf.Get("email") != "owner@acme.test" {
		t.Fatalf("customer form: %v", cf)
	}
	f := fs.form("POST /v1/checkout/sessions")
	if f.Get("mode") != "subscription" || f.Get("customer") != "cus_1" || f.Get("line_items[0][price]") != "price_team" ||
		f.Get("line_items[0][quantity]") != "3" || f.Get("client_reference_id") != "org1" ||
		f.Get("subscription_data[metadata][org_id]") != "org1" || f.Get("success_url") != "https://heimdall.test/settings?tab=billing&checkout=done" {
		t.Fatalf("checkout form: %v", f)
	}
	if a, _ := svc.Store.Account(ctx, "org1"); a.CustomerID != "cus_1" || a.EffectivePlan() != PlanFree {
		t.Fatalf("customer kept, still free until paid: %+v", a)
	}

	fs.addSub("sub_1", "active", 3, "org1")
	res, err := deliver(t, svc, "evt_1", "checkout.session.completed", testNow.Unix()-60,
		map[string]any{"id": "cs_1", "mode": "subscription", "client_reference_id": "org1", "customer": "cus_1", "subscription": "sub_1"})
	if err != nil || !res.Applied {
		t.Fatalf("checkout completed: %+v %v", res, err)
	}
	a, _ := svc.Store.Account(ctx, "org1")
	if a.EffectivePlan() != PlanTeam || a.Seats != 3 || a.SubscriptionID != "sub_1" || a.PeriodEnd.IsZero() {
		t.Fatalf("team after checkout: %+v", a)
	}
	if res, _ := deliver(t, svc, "evt_1", "checkout.session.completed", testNow.Unix()-60, map[string]any{}); res == nil || res.Applied || res.Note != "already applied" {
		t.Fatalf("redelivery: %+v", res)
	}
	if _, err := svc.Checkout(ctx, "org1", "user1", "https://heimdall.test"); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("second checkout: %v", err)
	}

	// Seats follow the member count.
	orgs.members = 5
	ss, err := svc.SyncSeats(ctx, "org1")
	if err != nil || !ss.Changed || ss.Seats != 5 || fs.seatsSet["si_1"] != 5 {
		t.Fatalf("seat sync: %+v %v %v", ss, err, fs.seatsSet)
	}
	if f := fs.form("POST /v1/subscription_items/si_1"); f.Get("proration_behavior") != "create_prorations" {
		t.Fatalf("prorated: %v", f)
	}

	pu, err := svc.Portal(ctx, "org1", "https://heimdall.test")
	if err != nil || pu != "https://billing.stripe.test/p_1" || fs.form("POST /v1/billing_portal/sessions").Get("return_url") != "https://heimdall.test/settings?tab=billing" {
		t.Fatalf("portal: %q %v", pu, err)
	}
	inv, err := svc.Invoices(ctx, "org1")
	if err != nil || len(inv) != 1 || inv[0].AmountPaid != 78 || inv[0].Number != "A-0001" {
		t.Fatalf("invoices: %+v %v", inv, err)
	}
	if inv, err := svc.Invoices(ctx, "org2"); err != nil || len(inv) != 0 {
		t.Fatalf("no customer, no invoices: %v %v", inv, err)
	}
	if _, err := svc.Portal(ctx, "org2", "https://heimdall.test"); !errors.Is(err, ErrNoCustomer) {
		t.Fatalf("portal without customer: %v", err)
	}

	sub := func(status string) map[string]any {
		return map[string]any{"id": "sub_1", "status": status, "customer": "cus_1", "metadata": map[string]string{"org_id": "org1"},
			"items": map[string]any{"data": []any{map[string]any{"id": "si_1", "quantity": 5}}}}
	}
	if _, err := deliver(t, svc, "evt_2", "customer.subscription.deleted", testNow.Unix(), sub("canceled")); err != nil {
		t.Fatal(err)
	}
	if a, _ := svc.Store.Account(ctx, "org1"); a.EffectivePlan() != PlanFree || a.Status != "canceled" {
		t.Fatalf("canceled is free: %+v", a)
	}
	// An older update that arrives late does not bring the plan back.
	res, err = deliver(t, svc, "evt_3", "customer.subscription.updated", testNow.Unix()-30, sub("active"))
	if err != nil || res.Applied {
		t.Fatalf("late event: %+v %v", res, err)
	}
	if a, _ := svc.Store.Account(ctx, "org1"); a.EffectivePlan() != PlanFree {
		t.Fatalf("late event applied: %+v", a)
	}
	// A subscription with no org metadata is found by its customer.
	noMeta := sub("active")
	delete(noMeta, "metadata")
	if res, err := deliver(t, svc, "evt_4", "customer.subscription.updated", testNow.Unix()+1, noMeta); err != nil || !res.Applied {
		t.Fatalf("by customer: %+v %v", res, err)
	}
	if a, _ := svc.Store.Account(ctx, "org1"); a.EffectivePlan() != PlanTeam {
		t.Fatalf("reactivated: %+v", a)
	}
	// Unknown orgs and event types are acknowledged, not applied.
	if res, err := deliver(t, svc, "evt_5", "checkout.session.completed", testNow.Unix(), map[string]any{"client_reference_id": "ghost", "customer": "cus_x"}); err != nil || res.Applied {
		t.Fatalf("unknown org: %+v %v", res, err)
	}
	if res, err := deliver(t, svc, "evt_6", "invoice.created", testNow.Unix(), map[string]any{}); err != nil || res.Applied {
		t.Fatalf("other type: %+v %v", res, err)
	}
}

func TestWebhookFailureIsRetried(t *testing.T) {
	fs := newFakeStripe(t)
	svc, _ := newService(t, true, fs)
	fs.fail = "/v1/subscriptions/"
	obj := map[string]any{"mode": "subscription", "client_reference_id": "org1", "customer": "cus_1", "subscription": "sub_1"}
	if _, err := deliver(t, svc, "evt_9", "checkout.session.completed", testNow.Unix(), obj); err == nil {
		t.Fatal("stripe failure must fail the delivery")
	}
	fs.fail = ""
	fs.addSub("sub_1", "trialing", 1, "org1")
	if res, err := deliver(t, svc, "evt_9", "checkout.session.completed", testNow.Unix(), obj); err != nil || !res.Applied {
		t.Fatalf("retry applied: %+v %v", res, err)
	}
	if a, _ := svc.Store.Account(context.Background(), "org1"); a.EffectivePlan() != PlanTeam || a.Status != "trialing" {
		t.Fatalf("trialing is team: %+v", a)
	}
}

func TestWebhookRefusedWhenOffOrUnsigned(t *testing.T) {
	off, _ := newService(t, false, nil)
	payload := []byte(`{"id":"evt_1","type":"invoice.created","data":{"object":{}}}`)
	if _, err := off.HandleWebhook(context.Background(), payload, SignatureFor(payload, whsec, testNow.Unix())); !errors.Is(err, ErrBillingOff) {
		t.Fatalf("billing off: %v", err)
	}
	on, _ := newService(t, true, nil)
	if _, err := on.HandleWebhook(context.Background(), payload, SignatureFor(payload, "whsec_wrong", testNow.Unix())); !errors.Is(err, ErrSignature) {
		t.Fatalf("bad signature: %v", err)
	}
	if fresh, _ := on.Store.ClaimEvent(context.Background(), "evt_1", "x"); !fresh {
		t.Fatal("an unverified delivery must not be recorded")
	}
}

func TestStripeErrorAndKey(t *testing.T) {
	fs := newFakeStripe(t)
	s := &Stripe{Key: "sk_test_wrong", Base: fs.srv.URL}
	_, err := s.CreatePortal(context.Background(), "cus_1", "https://x")
	var se *StripeError
	if !errors.As(err, &se) || se.Status != 401 || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Fatalf("stripe error: %v", err)
	}
	if _, err := (&Stripe{}).CreatePortal(context.Background(), "cus_1", "x"); !errors.Is(err, ErrNoStripe) {
		t.Fatalf("no key: %v", err)
	}
}

func TestPricing(t *testing.T) {
	var s *Service
	p := s.Pricing()
	if p.BillingEnabled || len(p.Plans) != 3 || p.Plans[1].PriceUSD != TeamSeatUSD || p.Currency != "usd" {
		t.Fatalf("pricing: %+v", p)
	}
	on, _ := newService(t, true, nil)
	p = on.Pricing()
	if !p.BillingEnabled {
		t.Fatal("enabled")
	}
	b, _ := json.Marshal(p)
	for _, secret := range []string{"sk_test", "whsec", "price_team"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("pricing leaks %s", secret)
		}
	}
}
