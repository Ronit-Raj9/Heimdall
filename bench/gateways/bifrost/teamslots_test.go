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
