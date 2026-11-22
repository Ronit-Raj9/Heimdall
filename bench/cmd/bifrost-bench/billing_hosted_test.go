package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/billing"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// withBillingConfig makes the next hosted fixtures build billing with cfg.
func withBillingConfig(t *testing.T, cfg billing.Config) {
	t.Helper()
	prev := billingConfig
	billingConfig = func() billing.Config { return cfg }
	t.Cleanup(func() {
		billingConfig = prev
		tenancy.SetRouteGate(nil)
	})
}

// TestHostedBillingOff: billing off (the default) shows the plan and
// limits, meters the shared worker, and never refuses.
func TestHostedBillingOff(t *testing.T) {
	withBillingConfig(t, billing.Config{APIBase: billing.DefaultAPIBase})
	f := newHostedFixture(t)
	r := f.do("GET", "/api/bench/billing", "alice", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, `"enabled":false`) || !strings.Contains(r.body, `"id":"free"`) ||
		!strings.Contains(r.body, `"shared_worker_minutes"`) {
		t.Fatalf("billing: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/public/pricing", "", nil, nil); r.status != 200 || !strings.Contains(r.body, `"enterprise"`) {
		t.Fatalf("public pricing: %d %s", r.status, r.body)
	}
	if r := f.do("POST", "/api/bench/billing/checkout", "alice", nil, nil); r.status != 503 {
		t.Fatalf("checkout while off: %d %s", r.status, r.body)
	}
	if r := f.do("POST", "/api/bench/billing/checkout", "vera", nil, nil); r.status != 403 {
		t.Fatalf("viewer checkout: %d", r.status)
	}
	if r := f.do("GET", "/api/bench/billing", "", nil, nil); r.status != 401 && r.status != 403 {
		t.Fatalf("anonymous billing read: %d", r.status)
	}
	// Billing is off: a Free org still creates its own benchmarks.
	if r := f.do("POST", "/api/bench/suites", "alice", nil, map[string]any{"id": "own-bench"}); r.status == 402 {
		t.Fatalf("gated while billing is off: %s", r.body)
	}

	// Only shared worker leases are metered.
	m := billingMeter{env: f.env}
	end := time.Now().UTC()
	m.LeaseEnded(engine.LeaseUsage{OrgID: f.acme.ID, Pool: schemas.SharedRunnerPool, Start: end.Add(-12 * time.Minute), End: end, Slots: 1})
	m.LeaseEnded(engine.LeaseUsage{OrgID: f.acme.ID, Pool: schemas.DefaultRunnerPool, Start: end.Add(-50 * time.Minute), End: end, Slots: 1})
	svc := billingOf(f.env)
	mins, err := svc.SharedMinutesThisMonth(context.Background(), f.acme.ID)
	if err != nil || mins < 11.9 || mins > 12.1 {
		t.Fatalf("shared minutes %v %v", mins, err)
	}
	day := end.Format("2006-01-02")
	r = f.do("GET", "/api/bench/billing/usage?from="+day+"&to="+day, "vera", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, `"shared_worker_minutes":12`) || !strings.Contains(r.body, `"storage_bytes"`) {
		t.Fatalf("usage: %d %s", r.status, r.body)
	}
	// The imported runs of the fixture are counted by their start day.
	runs, err := (&billingData{env: f.env}).RunsByDay(context.Background(), f.acme.ID, time.Unix(0, 0).UTC(), end)
	total := 0
	for _, n := range runs {
		total += n
	}
	if err != nil || total != len(f.aRuns) {
		t.Fatalf("runs by day: %v (%d, want %d) %v", runs, total, len(f.aRuns), err)
	}
	seats, invited, err := (billingOrgs{env: f.env}).Seats(context.Background(), f.acme.ID)
	if err != nil || seats != 2 || invited != 0 {
		t.Fatalf("seats %d %d %v", seats, invited, err)
	}
	if email, _ := (billingOrgs{env: f.env}).OwnerEmail(context.Background(), f.acme.ID); email != "alice@example.com" {
		t.Fatalf("owner email %q", email)
	}
	if b, err := (&billingData{env: f.env}).StorageBytes(context.Background(), f.acme.ID); err != nil || b <= 0 {
		t.Fatalf("storage %d %v", b, err)
	}
}

// TestHostedBillingOn: billing on, an org on Free is refused its own
// benchmarks and seats past three with 402 plan_limit; a server admin can
// set Team, which lifts them.
func TestHostedBillingOn(t *testing.T) {
	withBillingConfig(t, billing.Config{Enabled: true, APIBase: billing.DefaultAPIBase})
	f := newHostedFixture(t)
	r := f.do("POST", "/api/bench/suites", "alice", nil, map[string]any{"id": "own-bench"})
	if r.status != 402 || !strings.Contains(r.body, `"plan_limit"`) || !strings.Contains(r.body, "/settings?tab=billing") {
		t.Fatalf("free plan suites: %d %s", r.status, r.body)
	}
	inv := func(email string) resp {
		return f.do("POST", "/api/bench/orgs/"+f.acme.ID+"/invitations", "alice", nil, map[string]any{"email": email, "role": "member"})
	}
	if r := inv("carol@example.com"); r.status >= 300 {
		t.Fatalf("third seat: %d %s", r.status, r.body)
	}
	if r := inv("dave@example.com"); r.status != 402 || !strings.Contains(r.body, "seats") {
		t.Fatalf("fourth seat: %d %s", r.status, r.body)
	}
	if _, err := billingOf(f.env).SetPlan(context.Background(), f.acme.ID, billing.PlanTeam, "root"); err != nil {
		t.Fatal(err)
	}
	if r := inv("dave@example.com"); r.status >= 300 {
		t.Fatalf("team seat: %d %s", r.status, r.body)
	}
	if r := f.do("POST", "/api/bench/suites", "alice", nil, map[string]any{"id": "own-bench"}); r.status == 402 {
		t.Fatalf("team suites gated: %s", r.body)
	}
	// Shared worker minutes: used up on Free, the launch caps refuse while
	// runs go to the shared worker.
	if _, err := billingOf(f.env).SetPlan(context.Background(), f.acme.ID, billing.PlanFree, "root"); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	billingMeter{env: f.env}.LeaseEnded(engine.LeaseUsage{OrgID: f.acme.ID, Pool: schemas.SharedRunnerPool, Start: end.Add(-301 * time.Minute), End: end, Slots: 1})
	if err := withSharedMinutes(f.env, f.acme.ID, nil)(context.Background()); err == nil || !strings.Contains(err.Error(), "shared worker minutes") {
		t.Fatalf("launch with minutes used up: %v", err)
	}
	if err := sharedMinutesGate(f.env, f.beta.ID)(context.Background()); err != nil {
		t.Fatalf("other org: %v", err)
	}
}
