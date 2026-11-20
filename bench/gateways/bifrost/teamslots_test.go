package bifrost

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// slotGateway is a gateway with n slots that Setup would have created.
func slotGateway(t *testing.T, n int) (*Gateway, ProviderSetup) {
	t.Helper()
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Slots = nil
	for i := 1; i <= n; i++ {
		name := "fp-slot-" + string(rune('0'+i))
		cfg.Slots = append(cfg.Slots, Slot{Name: name, Value: name})
	}
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cfg.Slots {
		g.keyIDs[s.Name] = "key-" + s.Name
	}
	return g, g.providers[0]
}

func teamRC(runID string, members int) *schemas.RunContext {
	rc := runContext(runID, testModel)
	for i := 0; i < members; i++ {
		rc.Agent.Team = append(rc.Agent.Team, schemas.TeamMember{ID: "m" + string(rune('a'+i))})
	}
	return rc
}

func TestTeamSlotsAllOrNothing(t *testing.T) {
	old := slotPoll
	slotPoll = 5 * time.Millisecond
	t.Cleanup(func() { slotPoll = old })
	g, p := slotGateway(t, 4)
	ctx := context.Background()
	if n := teamSlots(teamRC("r_a", 3)); n != 3 {
		t.Fatalf("teamSlots = %d", n)
	}
	if _, _, err := g.acquireRunSlots(ctx, p, "r_big", 5); err == nil || !strings.Contains(err.Error(), "needs 5 key slots") {
		t.Fatalf("team over the pool: %v", err)
	}
	// Team A takes 3 of 4 slots at once.
	if _, _, err := g.acquireRunSlots(ctx, p, "r_teama", 3); err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, h := range g.inUse {
		if strings.HasSuffix(h, "r_teama") {
			held++
		}
	}
	if held != 3 {
		t.Fatalf("team A holds %d slots, want 3: %v", held, g.inUse)
	}
	// Team B needs 3: it waits and holds nothing while it waits.
	got := make(chan error, 1)
	go func() {
		_, _, err := g.acquireRunSlots(ctx, p, "r_teamb", 3)
		got <- err
	}()
	time.Sleep(30 * time.Millisecond)
	g.mu.Lock()
	for _, h := range g.inUse {
		if strings.HasSuffix(h, "r_teamb") {
			t.Errorf("team B holds a slot while it waits: %v", g.inUse)
		}
	}
	g.mu.Unlock()
	// A single-agent run waits too while a team holds slots... and gets the
	// free slot at once, since one is enough.
	if _, _, err := g.acquireRunSlots(ctx, p, "r_single", 1); err != nil {
		t.Fatalf("single run with one slot free: %v", err)
	}
	// Team A ends: its reservations and slots go, team B gets three.
	g.releaseReserved("r_teama")
	g.mu.Lock()
	for name, h := range g.inUse {
		if h == "pending:r_teama" || h == "pending:r_single" {
			delete(g.inUse, name)
		}
	}
	g.mu.Unlock()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("team B: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("team B never got its slots")
	}
}

// TestTeamSlotsNoDeadlock races two teams that each need three of four
// slots many times: every round both finish, one after the other.
func TestTeamSlotsNoDeadlock(t *testing.T) {
	old := slotPoll
	slotPoll = time.Millisecond
	t.Cleanup(func() { slotPoll = old })
	g, p := slotGateway(t, 4)
	for round := 0; round < 20; round++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, run := range []string{"r_x", "r_y"} {
			wg.Add(1)
			go func(run string) {
				defer wg.Done()
				s, _, err := g.acquireRunSlots(ctx, p, run, 3)
				if err != nil {
					errs <- err
					return
				}
				time.Sleep(2 * time.Millisecond)
				g.releaseReserved(run)
				g.releaseSlot(s.Name, run)
			}(run)
		}
		wg.Wait()
		cancel()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

func TestKeySlots(t *testing.T) {
	g, _ := slotGateway(t, 4)
	if n := g.KeySlots(""); n != 4 {
		t.Errorf("KeySlots default = %d", n)
	}
	if n := g.KeySlots("nope"); n != 0 {
		t.Errorf("KeySlots unknown = %d", n)
	}
}

// TestA2ATeamSlotsUpFront covers 3-A-01: an A2A team reserves its entry and
// every worker slot at once.
func TestA2ATeamSlotsUpFront(t *testing.T) {
	rc := runContext("r_a2a", testModel)
	rc.Agent.Adapter = "a2a"
	rc.Agent.A2A = &schemas.A2AAgent{CardURL: "http://e/card", Team: []schemas.A2AMember{{ID: "wa"}, {ID: "wb"}}}
	if n := teamSlots(rc); n != 3 {
		t.Fatalf("teamSlots = %d, want 3", n)
	}
	g, p := slotGateway(t, 2)
	if _, _, err := g.acquireRunSlots(context.Background(), p, "r_a2a", teamSlots(rc)); err == nil || !strings.Contains(err.Error(), "needs 3 key slots") {
		t.Fatalf("A2A team over the pool: %v", err)
	}
}

// TestOwnServiceSlotsFailFast covers 3-A-14: a run whose own service key
// holds the slots it needs fails at once instead of waiting forever.
func TestOwnServiceSlotsFailFast(t *testing.T) {
	g, p := slotGateway(t, 2)
	g.mu.Lock()
	g.inUse["fp-slot-1"] = "vk-svc"
	g.minted["vk-svc"] = mintRecord{slot: "fp-slot-1", at: time.Now(), run: "r_own" + "s0abc"}
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := g.acquireRunSlots(ctx, p, "r_own", 2)
	if err == nil || !strings.Contains(err.Error(), "its own services hold 1 of the 2") {
		t.Fatalf("want an immediate own-service refusal, got %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("waited for its own slot")
	}
}

// TestMintKeyRefusesRoutingBudgetUnderFairness covers 3-A-09: the fairness
// cap never silently replaces the routing treatment's budget.
func TestMintKeyRefusesRoutingBudgetUnderFairness(t *testing.T) {
	g, _ := slotGateway(t, 1)
	rc := runContext("r_rb", testModel)
	rc.Experiment = &schemas.Experiment{Fairness: schemas.FairnessEqualBudget}
	rc.Agent.Budgets.USD = 1
	rc.Agent.Gateway.Routing = &schemas.RoutingPolicy{Targets: []schemas.RouteTarget{{Model: testModel}}, Limits: &schemas.RouteLimits{BudgetUSD: 0.02}}
	if _, err := g.MintKey(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "both limit the run key") {
		t.Fatalf("want a refusal, got %v", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.inUse) != 0 {
		t.Fatalf("slot not released: %v", g.inUse)
	}
}

// TestServeMCPRefusesFoldedNameCollision covers 3-A-10.
func TestServeMCPRefusesFoldedNameCollision(t *testing.T) {
	g, _ := slotGateway(t, 1)
	rc := runContext("r_mcp", testModel)
	rc.Key = &schemas.RunKey{ID: "vk-1"}
	ups := []schemas.MCPUpstream{{Service: "lookup-cache", URL: "http://a"}, {Service: "lookup--cache", URL: "http://b"}}
	if _, _, err := g.ServeMCP(context.Background(), rc, ups, false); err == nil || !strings.Contains(err.Error(), "both map to MCP client name") {
		t.Fatalf("want a collision refusal, got %v", err)
	}
}
