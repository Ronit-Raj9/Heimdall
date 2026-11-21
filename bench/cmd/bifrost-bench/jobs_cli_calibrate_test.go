package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// orgCalGateway is calGateway plus the org key hooks; MintKey refuses a key
// not bound to the org, as the real gateway would fall back to env slots.
type orgCalGateway struct {
	calGateway
	mu    sync.Mutex
	creds []schemas.ProviderCredential
	bound map[string]string
}

func (g *orgCalGateway) ApplyOrgCredentials(_ context.Context, org string, creds []schemas.ProviderCredential) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.creds = append(g.creds, creds...)
	return nil
}

func (g *orgCalGateway) BindExperimentOrg(exp, org string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bound == nil {
		g.bound = map[string]string{}
	}
	g.bound[exp] = org
}

func (g *orgCalGateway) MintKey(ctx context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	g.mu.Lock()
	org := g.bound[rc.Run.ExpID]
	g.mu.Unlock()
	if org == "" {
		return nil, errors.New("key not bound to an org: would use env keys")
	}
	return g.calGateway.MintKey(ctx, rc)
}

type testJobLog struct{ strings.Builder }

func (l *testJobLog) Printf(f string, a ...any) { fmt.Fprintf(&l.Builder, f+"\n", a...) }
func (l *testJobLog) Warnf(f string, a ...any)  { fmt.Fprintf(&l.Builder, "warn: "+f+"\n", a...) }
func (l *testJobLog) Errorf(f string, a ...any) { fmt.Fprintf(&l.Builder, "error: "+f+"\n", a...) }

func hostedCalibrateFixture(t *testing.T) (jobEnv, keystore.Store, *keystore.Key, *orgCalGateway, *httptest.Server) {
	t.Helper()
	a, _, s := judgedFixture(t)
	if _, err := calibration.Append(s.Root, seedLabels(s)); err != nil {
		t.Fatal(err)
	}
	pp := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(pp, []byte(`{"id":"t","source":"test","captured_at":"2026-11-14T00:00:00Z","models":{"judge-model":{"input_per_mtok":3,"output_per_mtok":15,"cache_read_per_mtok":0.3,"cache_write_per_mtok":3.75}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pt, err := evidence.LoadPrices(pp)
	if err != nil {
		t.Fatal(err)
	}
	a.prices = pt
	ks := keystore.NewMemory()
	key, err := ks.Create(context.Background(), "org1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "mine", Value: "sk-org-own"}, "u")
	if err != nil {
		t.Fatal(err)
	}
	gw := &orgCalGateway{}
	srv := fakeChat()
	t.Cleanup(srv.Close)
	old := calibrateGatewayFor
	calibrateGatewayFor = func(context.Context, jobEnv) (schemas.Gateway, string, func(), error) {
		return gw, srv.URL, func() {}, nil
	}
	t.Cleanup(func() { calibrateGatewayFor = old })
	oldLoad := calibrateLoadSuite
	calibrateLoadSuite = func(context.Context, *app, string) (*schemas.Suite, error) { return s, nil }
	t.Cleanup(func() { calibrateLoadSuite = oldLoad })
	env := jobEnv{App: a, OrgID: "org1", ProjectRoot: a.cfg.Root, Hosted: true, Services: handlers.Services{OrgID: "org1", Hosted: true, Keys: ks}}
	return env, ks, key, gw, srv
}

func runCal(env jobEnv, params string) (any, string, error) {
	log := &testJobLog{}
	res, err := runCalibrateJob(context.Background(), env, &jobs.Job{Params: json.RawMessage(params)}, log, nil)
	return res, log.String(), err
}

func TestHostedCalibrateRefusesWithoutYes(t *testing.T) {
	env, _, _, gw, _ := hostedCalibrateFixture(t)
	if _, _, err := validateCalibrateRun(context.Background(), env, json.RawMessage(`{"suite":"qa"}`)); err == nil || !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "yes") {
		t.Fatalf("validate without yes: %v", err)
	}
	if _, _, err := runCal(env, `{"suite":"qa"}`); err == nil {
		t.Fatal("run without yes must fail")
	}
	// estimate is allowed without yes and spends nothing.
	res, log, err := runCal(env, `{"suite":"qa","estimate":true}`)
	if err != nil {
		t.Fatalf("estimate: %v\n%s", err, log)
	}
	r := res.(*calibrateResult)
	if gw.minted != 0 || r.Calls != 8 || r.ForecastUSD == nil || r.RecordID != "" || r.KeyID == "" {
		t.Fatalf("estimate result %+v minted %d", r, gw.minted)
	}
}

func TestHostedCalibrateUsesOrgKey(t *testing.T) {
	env, ks, key, gw, _ := hostedCalibrateFixture(t)
	t.Setenv("OPENAI_API_KEY", "sk-env-server")
	res, log, err := runCal(env, `{"suite":"qa","yes":true}`)
	if err != nil {
		t.Fatalf("calibrate: %v\n%s", err, log)
	}
	r := res.(*calibrateResult)
	if r.RecordID == "" || r.Human == nil || r.SeedItems != 2 || r.KeyID != key.ID || r.SpentUSD <= 0 {
		t.Fatalf("result %+v\n%s", r, log)
	}
	if len(gw.creds) != 1 || gw.creds[0].Value != "sk-org-own" || gw.creds[0].KeyID != key.ID || gw.bound["calibration-org1"] != "org1" || gw.minted != 1 {
		t.Fatalf("gateway creds %+v bound %v minted %d", gw.creds, gw.bound, gw.minted)
	}
	k, _ := ks.Get(context.Background(), "org1", key.ID)
	if k.MonthSpentUSD <= 0 || !strings.Contains(log, "month spend") {
		t.Fatalf("spend not added: %+v\n%s", k, log)
	}
	if strings.Contains(log, "sk-org-own") {
		t.Fatal("key value in the job log")
	}

	// No key for the judge provider: refused with the add-key link, nothing minted.
	ks2 := keystore.NewMemory()
	env.Services.Keys = ks2
	_, _, err = runCal(env, `{"suite":"qa","yes":true}`)
	if err == nil || !strings.Contains(err.Error(), "/models?add=openai") || gw.minted != 1 {
		t.Fatalf("no key: %v minted %d", err, gw.minted)
	}
}

func TestHostedCalibrateCapsCalls(t *testing.T) {
	env, _, _, gw, _ := hostedCalibrateFixture(t)
	old := hostedCalibrateMaxCalls
	hostedCalibrateMaxCalls = 4
	t.Cleanup(func() { hostedCalibrateMaxCalls = old })
	_, _, err := runCal(env, `{"suite":"qa","yes":true}`)
	if err == nil || !strings.Contains(err.Error(), "over the cap of 4") || gw.minted != 0 {
		t.Fatalf("over cap: %v minted %d", err, gw.minted)
	}
	// A sample of one item fits (4 calls).
	res, log, err := runCal(env, `{"suite":"qa","yes":true,"sample":1}`)
	if err != nil {
		t.Fatalf("sampled: %v\n%s", err, log)
	}
	if r := res.(*calibrateResult); r.Calls != 4 || r.Items != 1 {
		t.Fatalf("sampled result %+v", r)
	}
	// The transport stops at the cap even when the forecast was wrong.
	m := &spendMeter{base: http.DefaultTransport, maxCalls: 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := &http.Client{Transport: m}
	if resp, err := c.Get(srv.URL); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if _, err := c.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "cap reached") {
		t.Fatalf("second call past the cap: %v", err)
	}
}
