package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Security review of group F (Bifrost features): the rules below had no
// test of their own.

// gwRecorder is a gateway that counts every request it gets.
func gwRecorder(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Write([]byte(`{"agents":[{"name":"a","card_url":"https://a.example.com/card","enabled":true}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// TestGatewaySourceHostedRefusesPrivateAddressAtUse: a saved source that
// names a local address (saved before the server was hosted, or written by
// hand) is never dialled on a hosted server: the address check holds at
// every read, not only when the source is saved.
func TestGatewaySourceHostedRefusesPrivateAddressAtUse(t *testing.T) {
	srv, seen := gwRecorder(t)
	root := t.TempDir()
	if err := gwfeature.SaveSource(root, gwfeature.SourceConfig{GatewayURL: srv.URL, SavedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Hosted: true, OrgID: "o1"}}
	get := gatewaySourceGet(context.Background(), env)
	if get == nil {
		t.Fatal("a saved source gives a reader")
	}
	var v string
	if err := get(context.Background(), "/api/version", &v); err == nil {
		t.Fatal("a hosted read of a loopback source succeeded")
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("the hosted server dialled a local address: %v", got)
	}
}

// TestBifrostSettingNeverCallsAGateway: "Use this in Bifrost" only returns
// text. Neither the run gateway nor the org's saved Bifrost gets a request,
// for a feature test or for a plain comparison.
func TestBifrostSettingNeverCallsAGateway(t *testing.T) {
	srv, seen := gwRecorder(t)
	root := t.TempDir()
	if err := gwfeature.SaveSource(root, gwfeature.SourceConfig{GatewayURL: srv.URL, SavedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root}
	cfg.Gateway.URL = srv.URL
	src := gwShapeSource()
	plain := src.manifests["e_plain"]
	plain.Arms = map[string]schemas.ArmManifest{"A": settingArm("m1", "anthropic", 0), "B": settingArm("m2", "openai", 0)}
	dsrc := settingSource{gwTestSource: src, decisions: map[string]schemas.Decision{"e_plain": schemas.DecisionImproved}}
	r := router.New()
	h := newBifrostSettingHandler(OrgEnv{Cfg: cfg, Mode: testMode})
	h.source = func() leaderboard.Source { return dsrc }
	h.routes(r)
	for _, id := range []string{"e_shape", "e_plain"} {
		st, out := secretsCall(r.Handler, "GET", APIPrefix+"/experiments/"+id+"/bifrost-setting", "")
		if st != 200 {
			t.Fatalf("%s: %d %s", id, st, out)
		}
		if a := gwDecode(t, st, out); a["setting"] == nil {
			t.Fatalf("%s: no setting: %s", id, out)
		}
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("the setting route called a gateway: %v", got)
	}
}

// TestGatewayAgentsReadOnlyTheOrgsOwnSource: on a hosted server the
// registered agents routes read the organization's own saved Bifrost only.
// With no saved source they answer 409 and never fall back to the shared
// run gateway; another organization's saved source is not used either.
func TestGatewayAgentsReadOnlyTheOrgsOwnSource(t *testing.T) {
	runGW, runSeen := gwRecorder(t)
	otherRoot := t.TempDir()
	if err := gwfeature.SaveSource(otherRoot, gwfeature.SourceConfig{GatewayURL: "https://other-org.example.com", SavedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: t.TempDir()}
	cfg.Gateway.URL = runGW.URL
	r := router.New()
	newGatewayAgentsHandler(OrgEnv{Cfg: cfg, Mode: testMode, Services: Services{Hosted: true, OrgID: "o1"}}).routes(r)
	if st, out := secretsCall(r.Handler, "GET", APIPrefix+"/gateway/agents", ""); st != 409 {
		t.Fatalf("list without a source: %d %s", st, out)
	}
	if st, out := secretsCall(r.Handler, "POST", APIPrefix+"/gateway/agents/a/import", `{"model":"m","provider":"p"}`); st != 409 {
		t.Fatalf("import without a source: %d %s", st, out)
	}
	if got := runSeen(); len(got) != 0 {
		t.Fatalf("the hosted agents routes read the shared run gateway: %v", got)
	}
}
