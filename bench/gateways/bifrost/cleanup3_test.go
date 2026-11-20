package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// cleanupFake is a small management API for cleanup paths: virtual keys,
// routing rules, teams, a2a registrations and MCP clients, with chosen
// DELETEs answering 500.
type cleanupFake struct {
	mu      sync.Mutex
	vks     []map[string]any
	rules   []map[string]any
	teams   []map[string]any
	agents  []string
	clients []map[string]any
	fail    map[string]bool // path answered 500 on DELETE
	deleted []string
	lists   []string // GET paths in order
}

func newCleanupFake(t *testing.T) (*cleanupFake, *httptest.Server) {
	t.Helper()
	f := &cleanupFake{fail: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *cleanupFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Method == http.MethodDelete {
		if f.fail[r.URL.Path] {
			reply(500, map[string]any{"error": map[string]any{"message": "boom"}})
			return
		}
		f.deleted = append(f.deleted, r.URL.Path)
		reply(200, map[string]any{})
		return
	}
	f.lists = append(f.lists, r.URL.Path)
	switch r.URL.Path {
	case "/api/governance/virtual-keys":
		reply(200, map[string]any{"virtual_keys": f.vks, "count": len(f.vks)})
	case "/api/routing/rules":
		reply(200, map[string]any{"rules": f.rules})
	case "/api/governance/teams":
		reply(200, map[string]any{"teams": f.teams})
	case "/api/agents":
		var views []map[string]any
		for _, a := range f.agents {
			views = append(views, map[string]any{"name": a})
		}
		reply(200, map[string]any{"agents": views})
	case pathMCPClients:
		reply(200, map[string]any{"clients": f.clients, "count": len(f.clients), "total_count": len(f.clients)})
	default:
		reply(404, map[string]any{})
	}
}

func (f *cleanupFake) wasDeleted(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.deleted, path)
}

// TestRevokeKeyAttemptsEveryStep (3-A-02): a failed member DELETE still
// deletes the lead's routing rule and key; the team stays recorded and is
// deleted by a retry once the member key goes.
func TestRevokeKeyAttemptsEveryStep(t *testing.T) {
	f, srv := newCleanupFake(t)
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.fail["/api/governance/virtual-keys/vk-member"] = true
	g.teams["vk-lead"] = "team-1"
	key := &schemas.RunKey{ID: "vk-lead", Name: "r_abc", RoutingRuleID: "rule-1",
		Members: []schemas.MemberKey{{ID: "vk-lead"}, {ID: "vk-member", Name: "r_abc--w"}}}
	err = g.RevokeKey(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "vk-member") {
		t.Fatalf("RevokeKey = %v, want the member error", err)
	}
	if !f.wasDeleted("/api/governance/virtual-keys/vk-lead") || !f.wasDeleted("/api/routing/rules/rule-1") {
		t.Fatalf("lead key or rule not deleted: %v", f.deleted)
	}
	if f.wasDeleted("/api/governance/teams/team-1") {
		t.Fatal("team deleted while a member key is still in it")
	}
	if g.teamFor("vk-lead") != "team-1" {
		t.Fatal("team id lost before its DELETE")
	}
	delete(f.fail, "/api/governance/virtual-keys/vk-member")
	if err := g.RevokeKey(context.Background(), key); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !f.wasDeleted("/api/governance/teams/team-1") || g.teamFor("vk-lead") != "" {
		t.Fatalf("retry did not delete the team: %v", f.deleted)
	}
}

// TestRevokeStaleTeamsAndA2A (3-A-03, 3-A-04): the sweep deletes the team
// and a2a registrations of a dead run, keeps those of an active run, never
// touches a cut or foreign name, and keeps a team whose key could not go.
func TestRevokeStaleTeamsAndA2A(t *testing.T) {
	f, srv := newCleanupFake(t)
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.vks = []map[string]any{{"id": "vk-1", "name": "r_dead"}, {"id": "vk-2", "name": "r_stuck"}}
	f.fail["/api/governance/virtual-keys/vk-2"] = true
	f.teams = []map[string]any{{"id": "t-dead", "name": "r_dead--team"}, {"id": "t-live", "name": "r_live--team"},
		{"id": "t-stuck", "name": "r_stuck--team"}, {"id": "t-user", "name": "marketing"}}
	f.agents = []string{"hb-r-dead-lead", "hb-r-dead-w1", "hb-r-live-lead", "hb-ead-w1", "user-agent"}
	_, err = g.RevokeStale(context.Background(), map[string]bool{"r_live": true})
	if err == nil || !strings.Contains(err.Error(), "vk-2") {
		t.Fatalf("RevokeStale = %v, want the stuck key error", err)
	}
	for _, p := range []string{"/api/governance/teams/t-dead", "/api/agents/hb-r-dead-lead", "/api/agents/hb-r-dead-w1"} {
		if !f.wasDeleted(p) {
			t.Errorf("%s not deleted: %v", p, f.deleted)
		}
	}
	for _, p := range []string{"/api/governance/teams/t-live", "/api/governance/teams/t-stuck", "/api/governance/teams/t-user",
		"/api/agents/hb-r-live-lead", "/api/agents/hb-ead-w1", "/api/agents/user-agent"} {
		if f.wasDeleted(p) {
			t.Errorf("%s deleted", p)
		}
	}
}

// TestRemoveStaleMCPHonoursClaims (3-A-08): the MCP sweep keeps a run that
// holds a live claim at the fault proxy, and removes nothing when the
// claims cannot be read.
func TestRemoveStaleMCPHonoursClaims(t *testing.T) {
	f, srv := newCleanupFake(t)
	proxy := newClaimProxy(t, 1)
	cfg := testConfig(srv.URL)
	cfg.Slots = []Slot{{Name: "fp-slot-1", Value: "slot-auth-1"}}
	cfg.Claims = proxy.client("host-a")
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	other := proxy.client("host-b")
	if _, err := other.ClaimSlots(context.Background(), faultproxy.ClaimRequest{Slots: []string{"fp-slot-1"}, Count: 1, TTLS: 60, RunID: "r_held"}); err != nil {
		t.Fatal(err)
	}
	f.vks = []map[string]any{{"id": "vk-m1", "name": "hm-mcp-r_held"}, {"id": "vk-m2", "name": "hm-mcp-r_gone"}}
	n, err := g.RemoveStaleMCP(context.Background(), map[string]bool{})
	if err != nil || n != 1 {
		t.Fatalf("RemoveStaleMCP = %d, %v", n, err)
	}
	if f.wasDeleted(pathVKs+"/vk-m1") || !f.wasDeleted(pathVKs+"/vk-m2") {
		t.Fatalf("deleted %v: want only the unclaimed run's key", f.deleted)
	}
	proxy.ctrl.Close()
	f.vks = append(f.vks, map[string]any{"id": "vk-m3", "name": "hm-mcp-r_other"})
	if n, err := g.RemoveStaleMCP(context.Background(), map[string]bool{}); err == nil || n != 0 || f.wasDeleted(pathVKs+"/vk-m3") {
		t.Fatalf("sweep with the proxy down = %d, %v", n, err)
	}
}

// legacyClaimer is a fault proxy client whose capabilities change: an old
// proxy that is later upgraded.
type legacyClaimer struct {
	*faultproxy.Client
	mu       sync.Mutex
	upgraded bool
	asked    int
}

func (c *legacyClaimer) Capabilities(ctx context.Context) (*faultproxy.Health, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked++
	if !c.upgraded {
		return &faultproxy.Health{}, nil
	}
	return c.Client.Capabilities(ctx)
}

// TestOwnershipModeRevalidated (3-B-04): a gateway that first saw an old
// proxy adopts claims once the proxy is upgraded; the janitor reads the
// capabilities fresh and so sees another process's claim at once.
func TestOwnershipModeRevalidated(t *testing.T) {
	proxy := newClaimProxy(t, 1)
	lc := &legacyClaimer{Client: proxy.client("host-a")}
	f, srv := newCleanupFake(t)
	cfg := testConfig(srv.URL)
	cfg.Slots = []Slot{{Name: "fp-slot-1", Value: "slot-auth-1"}}
	cfg.Claims = lc
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if mode, _ := g.ownershipMode(ctx); mode != OwnershipProcessLocal {
		t.Fatalf("mode %q, want process-local", mode)
	}
	lc.mu.Lock()
	lc.upgraded = true
	lc.mu.Unlock()
	if _, err := proxy.client("host-b").ClaimSlots(ctx, faultproxy.ClaimRequest{Slots: []string{"fp-slot-1"}, Count: 1, TTLS: 60, RunID: "r_b"}); err != nil {
		t.Fatal(err)
	}
	f.vks = []map[string]any{{"id": "vk-b", "name": "r_b"}}
	if n, err := g.RevokeStale(ctx, map[string]bool{}); err != nil || n != 0 || f.wasDeleted(pathVKs+"/vk-b") {
		t.Fatalf("janitor on a stale legacy mode = %d, %v; deleted %v", n, err, f.deleted)
	}
	old := legacyModeTTL
	legacyModeTTL = 0
	t.Cleanup(func() { legacyModeTTL = old })
	if mode, _ := g.ownershipMode(ctx); mode != OwnershipFaultProxy {
		t.Fatalf("mode %q after upgrade, want fault proxy", mode)
	}
}

// TestSlotModelsRereadUnderClaim (3-A-07): two processes share a slot
// through claims. Each mint reads the provider key's models again, so a
// later owner keeps the models an earlier one added, and an earlier owner
// re-adds a model a stale cache would have skipped.
func TestSlotModelsRereadUnderClaim(t *testing.T) {
	fastClaims(t)
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 1)
	a, _ := claimGateway(t, f, 1, proxy.client("host-a"), nil)
	b, _ := claimGateway(t, f, 1, proxy.client("host-b"), nil)
	ctx := context.Background()
	models := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []string
		for _, m := range f.keys[0]["models"].([]any) {
			out = append(out, fmt.Sprint(m))
		}
		return out
	}
	mint := func(g *Gateway, run, model string) {
		t.Helper()
		k, err := g.MintKey(ctx, runContext(run, model))
		if err != nil {
			t.Fatal(err)
		}
		if err := g.RevokeKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	mint(a, "r_a1", "model-b")
	mint(b, "r_b1", "model-c")
	if got := models(); !slices.Contains(got, "model-b") || !slices.Contains(got, "model-c") {
		t.Fatalf("models after B = %v, B dropped A's model", got)
	}
	// Someone rewrites the slot without model-b; A's cache still says yes.
	f.mu.Lock()
	f.keys[0]["models"] = []any{testModel}
	f.mu.Unlock()
	mint(a, "r_a2", "model-b")
	if got := models(); !slices.Contains(got, "model-b") {
		t.Fatalf("models after A's second mint = %v, stale cache skipped the update", got)
	}
}

// TestAudioWitness (3-B-08): audio traffic is checked on both sides.
func TestAudioWitness(t *testing.T) {
	speech := &parsedRow{Req: schemas.GatewayRequest{ID: "a1", Kind: kindAudio}, Upstream: true, Object: "speech"}
	turn := &parsedRow{Req: schemas.GatewayRequest{ID: "t1", Kind: kindAudio}, Object: "realtime.turn"}
	audio := schemas.LedgerEntry{Kind: kindAudio, Path: "/v1/audio/speech", Action: "pass", Status: 200}
	session := schemas.LedgerEntry{Kind: kindAudio, Path: "/v1/realtime", Action: "pass", Status: 101}
	cases := []struct {
		name     string
		rows     []*parsedRow
		ledger   []schemas.LedgerEntry
		complete bool
	}{
		{"speech row without witness", []*parsedRow{speech}, nil, false},
		{"witness without row", nil, []schemas.LedgerEntry{audio}, false},
		{"speech matched", []*parsedRow{speech}, []schemas.LedgerEntry{audio}, true},
		{"realtime session without turns", nil, []schemas.LedgerEntry{session}, false},
		{"realtime turns without session", []*parsedRow{turn, turn}, nil, false},
		{"realtime matched per session", []*parsedRow{turn, turn}, []schemas.LedgerEntry{session}, true},
	}
	for _, c := range cases {
		j := joinLedger(c.rows, c.ledger, "")
		if j.complete() != c.complete {
			t.Errorf("%s: complete %t (%s)", c.name, j.complete(), j.reason())
		}
	}
}

// TestFetchRequestsDirectSlowPoll (3-B-12): a poll that crosses the settle
// time must not end the wait on a repeat of a snapshot taken before it.
func TestFetchRequestsDirectSlowPoll(t *testing.T) {
	f := newFakeBifrost(t)
	for _, name := range []string{"chat-json.json", "responses-stream-tools.json", "retry-fail.json"} {
		f.rows = append(f.rows, json.RawMessage(readFixture(t, "openai/"+name)))
	}
	f.hideLists = 2
	g, err := New(multiConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	g.settle = 50 * time.Millisecond
	key := &schemas.RunKey{ID: "vk-direct", KeySlot: "oa-slot-1", Provider: "openai"}
	// Every poll interval is longer than the settle time: the second poll
	// is already past it while both snapshots are empty.
	log, err := g.FetchRequests(context.Background(), key, schemas.FetchExpectation{Timeout: 5 * time.Second, PollEvery: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !log.Complete || len(log.Requests) != 3 {
		t.Fatalf("direct log complete %t reason %q rows %d", log.Complete, log.Reason, len(log.Requests))
	}
}

// orderClaimer records when the claims are read among the fake's listings.
type orderClaimer struct {
	*faultproxy.Client
	f *cleanupFake
}

func (c *orderClaimer) Claims(ctx context.Context) ([]faultproxy.SlotClaim, error) {
	c.f.mu.Lock()
	c.f.lists = append(c.f.lists, "claims")
	c.f.mu.Unlock()
	return c.Client.Claims(ctx)
}

// TestRevokeStaleListsBeforeClaims (3-B-03): every cleanup candidate is
// listed before the claims are read, so a key minted by another process
// after the claim read cannot be among the candidates.
func TestRevokeStaleListsBeforeClaims(t *testing.T) {
	f, srv := newCleanupFake(t)
	proxy := newClaimProxy(t, 1)
	cfg := testConfig(srv.URL)
	cfg.Slots = []Slot{{Name: "fp-slot-1", Value: "slot-auth-1"}}
	cfg.Claims = &orderClaimer{Client: proxy.client("host-a"), f: f}
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RevokeStale(context.Background(), map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	at := slices.Index(f.lists, "claims")
	for _, p := range []string{"/api/routing/rules", pathVKs, "/api/governance/teams", "/api/agents"} {
		if i := slices.Index(f.lists, p); i < 0 || i > at {
			t.Fatalf("%s listed at %d, claims read at %d (%v)", p, i, at, f.lists)
		}
	}
}
