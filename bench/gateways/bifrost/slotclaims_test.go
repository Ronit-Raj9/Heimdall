package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// claimProxy is one real fault proxy (control API and data plane) with
// slots fp-slot-1..n whose placeholder credential is slot-auth-i.
type claimProxy struct {
	srv  *faultproxy.Server
	ctrl *httptest.Server
	data *httptest.Server
}

func newClaimProxy(t *testing.T, n int) *claimProxy {
	t.Helper()
	var slots []faultproxy.SlotConfig
	for i := 1; i <= n; i++ {
		slots = append(slots, faultproxy.SlotConfig{Name: fmt.Sprintf("fp-slot-%d", i), Auth: fmt.Sprintf("slot-auth-%d", i)})
	}
	srv, err := faultproxy.NewServer(faultproxy.Config{
		Upstream: faultproxy.UpstreamScripted, Slots: slots, LedgerPath: filepath.Join(t.TempDir(), "ledger.jsonl"),
		Getenv: func(string) string { return "" }, // never a real key
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &claimProxy{srv: srv, ctrl: httptest.NewServer(srv.ControlHandler()), data: httptest.NewServer(srv.Handler())}
	t.Cleanup(func() {
		p.ctrl.Close()
		p.data.Close()
		_ = srv.Close()
	})
	return p
}

// client is one bench process's fault proxy client: its own owner.
func (p *claimProxy) client(label string) *faultproxy.Client {
	return faultproxy.NewClient(p.ctrl.URL, faultproxy.WithToken(p.srv.ControlToken()), faultproxy.WithOwnerLabel(label))
}

// call sends one conversation request on a slot's credential.
func (p *claimProxy) call(t *testing.T, auth string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.data.URL+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-6","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", auth)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

// claimLogger records warnings and errors.
type claimLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *claimLogger) add(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, level+": "+msg)
}
func (l *claimLogger) Debug(string, ...any)      {}
func (l *claimLogger) Info(msg string, _ ...any) { l.add("info", msg) }
func (l *claimLogger) Warn(msg string, _ ...any) { l.add("warn", msg) }
func (l *claimLogger) Error(msg string, _ ...any) {
	l.add("error", msg)
}
func (l *claimLogger) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

// claimGateway is one bench process on the shared fake Bifrost: a Gateway
// with n slots, set up, claiming through claims (nil: process-local).
func claimGateway(t *testing.T, f *fakeBifrost, n int, claims SlotClaimer, tune func(*Config)) (*Gateway, *claimLogger) {
	t.Helper()
	cfg := testConfig(f.srv.URL)
	cfg.Slots = nil
	for i := 1; i <= n; i++ {
		cfg.Slots = append(cfg.Slots, Slot{Name: fmt.Sprintf("fp-slot-%d", i), Value: fmt.Sprintf("slot-auth-%d", i)})
	}
	if claims != nil {
		cfg.Claims = claims
	}
	log := &claimLogger{}
	cfg.Logger = log
	if tune != nil {
		tune(&cfg)
	}
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	return g, log
}

func fastClaims(t *testing.T) {
	t.Helper()
	oldPoll, oldClaim := slotPoll, claimPoll
	slotPoll, claimPoll = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { slotPoll, claimPoll = oldPoll, oldClaim })
}

func liveClaims(t *testing.T, c *faultproxy.Client) map[string]faultproxy.SlotClaim {
	t.Helper()
	list, err := c.Claims(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]faultproxy.SlotClaim{}
	for _, cl := range list {
		out[cl.Slot] = cl
	}
	return out
}

// TestTwoProcessesWithoutClaimsCollide is the failure F1 fixes: two
// processes reserve slots in their own memory and both take fp-slot-1.
func TestTwoProcessesWithoutClaimsCollide(t *testing.T) {
	f := newFakeBifrost(t)
	a, _ := claimGateway(t, f, 2, nil, nil)
	b, _ := claimGateway(t, f, 2, nil, nil)
	ctx := context.Background()
	ka, err := a.MintKey(ctx, runContext("r_proca", testModel))
	if err != nil {
		t.Fatal(err)
	}
	kb, err := b.MintKey(ctx, runContext("r_procb", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if ka.KeySlot != kb.KeySlot {
		t.Fatalf("without claims the processes were expected to collide, got %s and %s", ka.KeySlot, kb.KeySlot)
	}
}

// TestTwoProcessesShareOneProxy runs two Gateway instances, two bench
// processes, against one real fault proxy: the second picks another slot,
// and its plan writes can never rebind the first's scripted plan.
func TestTwoProcessesShareOneProxy(t *testing.T) {
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 3)
	ca, cb := proxy.client("host-a pid 1"), proxy.client("host-b pid 2")
	a, _ := claimGateway(t, f, 3, ca, nil)
	b, _ := claimGateway(t, f, 3, cb, nil)
	ctx := context.Background()

	ka, err := a.MintKey(ctx, runContext("r_proca", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.SetPlan(ctx, ka.KeySlot, schemas.FaultPlan{Mode: faultproxy.ModeScripted, RunID: "r_proca"}); err != nil {
		t.Fatal(err)
	}
	kb, err := b.MintKey(ctx, runContext("r_procb", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if ka.KeySlot != "fp-slot-1" || kb.KeySlot != "fp-slot-2" {
		t.Fatalf("slots = %s and %s, want fp-slot-1 and fp-slot-2", ka.KeySlot, kb.KeySlot)
	}
	if err := cb.SetPlan(ctx, kb.KeySlot, schemas.FaultPlan{RunID: "r_procb"}); err != nil {
		t.Fatal(err)
	}
	// A stale or buggy write from B on A's slot is refused, set and clear.
	if err := cb.SetPlan(ctx, ka.KeySlot, schemas.FaultPlan{RunID: "r_procb"}); !faultproxy.IsSlotHeld(err) {
		t.Fatalf("B's plan on A's slot: %v", err)
	}
	if err := cb.ClearPlan(ctx, ka.KeySlot); !faultproxy.IsSlotHeld(err) {
		t.Fatalf("B's clear on A's slot: %v", err)
	}
	// A's run is still scripted: its call is answered by the scripted model
	// and lands in its own ledger, not B's.
	proxy.call(t, "slot-auth-1")
	la, err := ca.Ledger(ctx, "r_proca")
	if err != nil || len(la) != 1 || la[0].Upstream != faultproxy.UpstreamScripted || la[0].Slot != "fp-slot-1" {
		t.Fatalf("A's ledger = %+v, %v", la, err)
	}
	if lb, _ := cb.Ledger(ctx, "r_procb"); len(lb) != 0 {
		t.Fatalf("B's ledger has A's call: %+v", lb)
	}
	claims := liveClaims(t, ca)
	if claims["fp-slot-1"].OwnerID != ca.OwnerID() || claims["fp-slot-1"].RunID != "r_proca" || claims["fp-slot-2"].OwnerID != cb.OwnerID() {
		t.Fatalf("claims = %+v", claims)
	}
	if row := a.SlotOwnership(ctx); row.Status != schemas.DoctorPass || !strings.Contains(row.Detail, "2 slots claimed now, 1 by other bench processes") {
		t.Fatalf("slot ownership row = %+v", row)
	}

	// Revoking A's key releases its claim; B may take the slot now.
	if err := ca.ClearPlan(ctx, ka.KeySlot); err != nil {
		t.Fatal(err)
	}
	if err := a.RevokeKey(ctx, ka); err != nil {
		t.Fatal(err)
	}
	if _, ok := liveClaims(t, ca)["fp-slot-1"]; ok {
		t.Fatal("RevokeKey kept the claim")
	}
	kb2, err := b.MintKey(ctx, runContext("r_procb2", testModel))
	if err != nil || kb2.KeySlot != "fp-slot-1" {
		t.Fatalf("B's second mint = %+v, %v", kb2, err)
	}
	// Close releases everything this process holds.
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if left := liveClaims(t, ca); len(left) != 0 {
		t.Fatalf("claims after Close = %+v", left)
	}
}

// TestClaimWaitThenFail: every free slot is held by another process; a
// mint waits the claim wait and fails with a reason naming the holder, and
// a mint that is still waiting when a slot frees gets it.
func TestClaimWaitThenFail(t *testing.T) {
	fastClaims(t)
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 2)
	ca := proxy.client("host-a pid 1")
	a, _ := claimGateway(t, f, 2, ca, nil)
	b, logB := claimGateway(t, f, 2, proxy.client("host-b pid 2"), func(c *Config) { c.ClaimWait = 150 * time.Millisecond })
	ctx := context.Background()
	k1, err := a.MintKey(ctx, runContext("r_hold1", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.MintKey(ctx, runContext("r_hold2", testModel)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = b.MintKey(ctx, runContext("r_starved", testModel))
	if err == nil || !strings.Contains(err.Error(), "claimed by another bench process") || !strings.Contains(err.Error(), "gave up after") ||
		!strings.Contains(err.Error(), "host-a pid 1") || !strings.Contains(err.Error(), "run r_hold1") {
		t.Fatalf("starved mint: %v", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatalf("gave up after %s, before the claim wait", time.Since(start))
	}
	if logB.count("waiting for a key slot") != 1 {
		t.Fatalf("wait warnings = %v", logB.msgs)
	}
	b.mu.Lock()
	held := len(b.inUse)
	b.mu.Unlock()
	if held != 0 {
		t.Fatalf("a failed mint holds slots: %v", b.inUse)
	}

	// A second mint waits; A's run ends and B gets its slot.
	b.cfg.ClaimWait = 5 * time.Second
	got := make(chan *schemas.RunKey, 1)
	go func() {
		k, err := b.MintKey(ctx, runContext("r_waiter", testModel))
		if err != nil {
			t.Error(err)
		}
		got <- k
	}()
	time.Sleep(50 * time.Millisecond)
	if err := a.RevokeKey(ctx, k1); err != nil {
		t.Fatal(err)
	}
	select {
	case k := <-got:
		if k == nil || k.KeySlot != k1.KeySlot {
			t.Fatalf("waiter got %+v", k)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiting mint never got the freed slot")
	}
}

// TestTeamClaimsAllOrNothing: a team run claims all its slots at the proxy
// in one call, or none, and holds none while it waits.
func TestTeamClaimsAllOrNothing(t *testing.T) {
	fastClaims(t)
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 4)
	ca, cb := proxy.client("host-a"), proxy.client("host-b")
	a, _ := claimGateway(t, f, 4, ca, nil)
	b, _ := claimGateway(t, f, 4, cb, func(c *Config) { c.ClaimWait = 5 * time.Second })
	ctx := context.Background()
	k1, err := a.MintKey(ctx, runContext("r_solo1", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.MintKey(ctx, runContext("r_solo2", testModel)); err != nil {
		t.Fatal(err)
	}
	// B's team of 3 sees 4 free slots in its own memory, but 2 are A's.
	pb := b.providers[0]
	done := make(chan error, 1)
	go func() {
		_, _, err := b.acquireRunSlots(ctx, pb, "r_team", 3)
		done <- err
	}()
	time.Sleep(60 * time.Millisecond)
	for slot, c := range liveClaims(t, ca) {
		if c.OwnerID == cb.OwnerID() {
			t.Fatalf("waiting team holds %s at the proxy", slot)
		}
	}
	b.mu.Lock()
	if len(b.inUse) != 0 {
		t.Errorf("waiting team holds slots in its process: %v", b.inUse)
	}
	b.mu.Unlock()
	if err := a.RevokeKey(ctx, k1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("team never got its slots")
	}
	mine := 0
	for _, c := range liveClaims(t, ca) {
		if c.OwnerID == cb.OwnerID() && c.RunID == "r_team" {
			mine++
		}
	}
	if mine != 3 {
		t.Fatalf("team claims = %d, want 3", mine)
	}
	// The team's unused reservations release their claims too.
	b.releaseReserved("r_team")
	mine = 0
	for _, c := range liveClaims(t, ca) {
		if c.OwnerID == cb.OwnerID() {
			mine++
		}
	}
	if mine != 1 {
		t.Fatalf("claims after releaseReserved = %d, want the lead's 1", mine)
	}
}

// TestClaimRenewalAndExpiry: renewal keeps a live process's claim past its
// TTL; a crashed process's claim (no renewal, no release) expires and the
// slot is taken by another process.
func TestClaimRenewalAndExpiry(t *testing.T) {
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 1)
	ca, cb := proxy.client("host-a"), proxy.client("host-b")
	short := func(c *Config) { c.ClaimTTL = time.Second; c.ClaimWait = 50 * time.Millisecond }
	a, _ := claimGateway(t, f, 1, ca, short)
	b, _ := claimGateway(t, f, 1, cb, func(c *Config) { c.ClaimTTL = time.Second; c.ClaimWait = 3 * time.Second })
	ctx := context.Background()
	if _, err := a.MintKey(ctx, runContext("r_alive", testModel)); err != nil {
		t.Fatal(err)
	}
	first := liveClaims(t, ca)["fp-slot-1"].ExpiresAt
	time.Sleep(1300 * time.Millisecond) // past the first TTL; renewal ran at about 333 ms and 666 ms
	c, ok := liveClaims(t, ca)["fp-slot-1"]
	if !ok || c.OwnerID != ca.OwnerID() || !c.ExpiresAt.After(first) {
		t.Fatalf("claim after one TTL = %+v (%v), first expiry %s", c, ok, first)
	}
	// A crashes: renewal stops, nothing is released.
	a.claims.stopOnce.Do(func() { close(a.claims.stop) })
	start := time.Now()
	kb, err := b.MintKey(ctx, runContext("r_next", testModel))
	if err != nil || kb.KeySlot != "fp-slot-1" {
		t.Fatalf("B after A's crash = %+v, %v", kb, err)
	}
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("B waited %s for an expired claim", time.Since(start))
	}
}

// TestClaimFallbackOldProxy: a fault proxy without the claim API is
// detected from /health; the bench warns once, keeps the process-local
// reservation, and doctor says so.
func TestClaimFallbackOldProxy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","slots":2,"plan_modes":["none","scripted"],"plan_fields":["model"]}`))
	})
	old := httptest.NewServer(mux)
	t.Cleanup(old.Close)
	f := newFakeBifrost(t)
	g, log := claimGateway(t, f, 2, faultproxy.NewClient(old.URL, faultproxy.WithToken("tok")), nil)
	ctx := context.Background()
	k1, err := g.MintKey(ctx, runContext("r_old1", testModel))
	if err != nil {
		t.Fatal(err)
	}
	k2, err := g.MintKey(ctx, runContext("r_old2", testModel))
	if err != nil || k1.KeySlot == k2.KeySlot {
		t.Fatalf("process-local mints = %+v %+v, %v", k1, k2, err)
	}
	if _, err := g.MintKey(ctx, runContext("r_old3", testModel)); err == nil || !strings.Contains(err.Error(), "no free key slot for anthropic") {
		t.Fatalf("third mint on two slots: %v", err)
	}
	if n := log.count("process-local only (fault proxy too old)"); n != 1 {
		t.Fatalf("old proxy warnings = %d: %v", n, log.msgs)
	}
	if row := g.SlotOwnership(ctx); row.Status != schemas.DoctorWarn || row.Detail != "process-local only (fault proxy too old)" {
		t.Fatalf("slot ownership row = %+v", row)
	}
	if n, err := g.RevokeStale(ctx, map[string]bool{}); err != nil || n != 2 {
		t.Fatalf("janitor on an old proxy = %d, %v", n, err)
	}
	if err := g.RevokeKey(ctx, k1); err != nil {
		t.Fatal(err)
	}
}

// TestClaimDirectProviderStaysLocal: a direct provider has no fault proxy
// plan, so its slots are never claimed.
func TestClaimDirectProviderStaysLocal(t *testing.T) {
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 1)
	c := proxy.client("host-a")
	log := &claimLogger{}
	cfg := multiConfig(f.srv.URL)
	cfg.Claims, cfg.Logger = c, log
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	rc := runContext("r_direct", testOpenAIModel)
	rc.Agent.Gateway.Provider = "openai"
	k, err := g.MintKey(ctx, rc)
	if err != nil || k.Provider != "openai" {
		t.Fatalf("direct mint = %+v, %v", k, err)
	}
	if claims := liveClaims(t, c); len(claims) != 0 {
		t.Fatalf("a direct slot was claimed: %+v", claims)
	}
	if log.count("direct provider is process-local") != 1 {
		t.Fatalf("direct note = %v", log.msgs)
	}
	if row := g.SlotOwnership(ctx); row.Status != schemas.DoctorPass || !strings.Contains(row.Detail, "direct providers openai stay process-local") {
		t.Fatalf("slot ownership row = %+v", row)
	}
	direct, err := New(Config{URL: f.srv.URL, SetupToken: testToken, Claims: c,
		Getenv:    func(string) string { return "sk-real" },
		Providers: []ProviderSetup{{Name: "openai", Mode: ModeDirect, KeyEnv: "OPENAI_API_KEY", Model: testOpenAIModel, Slots: []Slot{{Name: "oa-slot-1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if row := direct.SlotOwnership(ctx); row.Status != schemas.DoctorWarn || !strings.Contains(row.Detail, "process-local only (direct mode, no fault proxy)") {
		t.Fatalf("direct-only row = %+v", row)
	}
}

// TestJanitorRespectsLiveClaims: another process's janitor never deletes a
// key whose run holds a live claim, and deletes it once the claim is gone.
// When the claims cannot be read, nothing is revoked.
func TestJanitorRespectsLiveClaims(t *testing.T) {
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 2)
	ca, cb := proxy.client("host-a"), proxy.client("host-b")
	a, _ := claimGateway(t, f, 2, ca, nil)
	b, _ := claimGateway(t, f, 2, cb, nil)
	ctx := context.Background()
	if _, err := a.MintKey(ctx, runContext("r_live", testModel)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.vks = append(f.vks, map[string]any{"id": "vk-orphan", "name": "r_orphan", "value": "sk-bf-x"})
	f.mu.Unlock()
	n, err := b.RevokeStale(ctx, map[string]bool{}) // B knows nothing of A's run
	if err != nil || n != 1 {
		t.Fatalf("B's sweep = %d, %v; want only the orphan", n, err)
	}
	names := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []string
		for _, vk := range f.vks {
			out = append(out, fmt.Sprint(vk["name"]))
		}
		return out
	}
	if got := names(); len(got) != 1 || got[0] != "r_live" {
		t.Fatalf("keys after B's sweep = %v", got)
	}
	// A exits (claims released, key left behind): now it is stale.
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := b.RevokeStale(ctx, map[string]bool{}); err != nil || n != 1 || len(names()) != 0 {
		t.Fatalf("B's sweep after A's exit = %d, %v, keys %v", n, err, names())
	}
	// The proxy is unreachable: the janitor revokes nothing.
	f.mu.Lock()
	f.vks = append(f.vks, map[string]any{"id": "vk-y", "name": "r_unknown", "value": "sk-bf-y"})
	f.mu.Unlock()
	proxy.ctrl.Close()
	if n, err := b.RevokeStale(ctx, map[string]bool{}); err == nil || n != 0 || !strings.Contains(err.Error(), "nothing revoked") {
		t.Fatalf("sweep with the proxy down = %d, %v", n, err)
	}
	if got := names(); len(got) != 1 {
		t.Fatalf("keys after a blind sweep = %v", got)
	}
}

// TestTeamMintClaimsAcrossProcesses runs a whole team mint (run key, then
// member keys) beside another process: a team that cannot get all its slots
// claims none and fails after the claim wait; one that fits takes only free
// slots, and revoking the run releases every claim it made.
func TestTeamMintClaimsAcrossProcesses(t *testing.T) {
	fastClaims(t)
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 3)
	ca, cb := proxy.client("host-a"), proxy.client("host-b")
	a, _ := claimGateway(t, f, 3, ca, nil)
	b, _ := claimGateway(t, f, 3, cb, func(c *Config) { c.ClaimWait = 100 * time.Millisecond })
	ctx := context.Background()
	ka, err := a.MintKey(ctx, runContext("r_solo", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.MintKey(ctx, teamRC("r_bigteam", 3)); err == nil || !strings.Contains(err.Error(), "claimed by another bench process") {
		t.Fatalf("team of 3 beside a held slot: %v", err)
	}
	for _, c := range liveClaims(t, ca) {
		if c.OwnerID == cb.OwnerID() {
			t.Fatalf("a failed team left a claim: %+v", c)
		}
	}
	rc := teamRC("r_team", 2)
	run, err := b.MintKey(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := b.MintMemberKeys(ctx, rc, run, rc.Agent.Team)
	if err != nil {
		t.Fatal(err)
	}
	run.Members = keys
	for _, k := range keys {
		if k.KeySlot == ka.KeySlot {
			t.Fatalf("member %s took A's slot %s", k.AgentID, k.KeySlot)
		}
		if err := cb.SetPlan(ctx, k.KeySlot, schemas.FaultPlan{Mode: faultproxy.ModeScripted, RunID: "r_team"}); err != nil {
			t.Fatalf("plan on member slot %s: %v", k.KeySlot, err)
		}
	}
	if err := b.RevokeKey(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, c := range liveClaims(t, ca) {
		if c.OwnerID == cb.OwnerID() {
			t.Fatalf("revoked team kept a claim: %+v", c)
		}
	}
}
