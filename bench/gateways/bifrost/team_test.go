package bifrost

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMintMemberKeys(t *testing.T) {
	f := newFakeBifrost(t)
	cfg := testConfig(f.srv.URL)
	cfg.Slots = append(cfg.Slots, Slot{Name: "fp-slot-3", Value: "slot-auth-3"})
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	rc := runContext("r_team0001", testModel)
	rc.Agent.Team = []schemas.TeamMember{{ID: "worker-a", Role: "worker", BudgetUSD: 0.25}, {ID: "lead", Role: "lead"}, {ID: "worker-b", Role: "worker"}}
	run, err := g.MintKey(ctx, rc)
	if err != nil {
		t.Fatalf("mint run key: %v", err)
	}
	keys, err := g.MintMemberKeys(ctx, rc, run, rc.Agent.Team)
	if err != nil {
		t.Fatalf("mint member keys: %v", err)
	}
	run.Members = keys
	if len(keys) != 3 || keys[1].AgentID != "lead" || keys[1].ID != run.ID || keys[1].KeySlot != run.KeySlot {
		t.Fatalf("the lead must run on the run key: %+v (run %s %s)", keys, run.ID, run.KeySlot)
	}
	slots := map[string]bool{}
	for _, k := range keys {
		slots[k.KeySlot] = true
		if k.Value == "" {
			t.Errorf("member %s has no key value", k.AgentID)
		}
	}
	if len(slots) != 3 {
		t.Errorf("members share slots: %+v", keys)
	}
	if keys[0].Name != "r_team0001--worker-a" {
		t.Errorf("member key name = %s", keys[0].Name)
	}
	body := f.lastBody("POST /api/governance/virtual-keys")
	if body["name"] != "r_team0001--worker-b" || body["budgets"] != nil {
		t.Errorf("last member key body = %v", body)
	}
	if ids := keyIDs(run); strings.Count(ids, ",") != 2 || !strings.HasPrefix(ids, run.ID+",") {
		t.Errorf("keyIDs = %s", ids)
	}
	if m := memberAgents(run); m[run.ID] != "lead" || m[keys[0].ID] != "worker-a" {
		t.Errorf("memberAgents = %v", m)
	}
	if err := g.RevokeKey(ctx, run); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if len(f.vks) != 0 || len(g.inUse) != 0 {
		t.Errorf("after revoke: vks %v, slots in use %v", f.vks, g.inUse)
	}
}

func TestAgentLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta map[string]any
		want string
	}{
		{"bifrost label header", map[string]any{"heimdall-agent": "worker-a"}, "worker-a"},
		{"configured header", map[string]any{"x-heimdall-agent": " lead "}, "lead"},
		{"baggage", map[string]any{"baggage": "k=v, heimdall.agent=re%20viewer;prop=1"}, "re viewer"},
		{"control characters dropped", map[string]any{"heimdall-agent": "a\x00b\nc"}, "abc"},
		{"none", map[string]any{"other": "x"}, ""},
		{"not a string", map[string]any{"heimdall-agent": 3}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentLabel(tc.meta); got != tc.want {
				t.Errorf("agentLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStaleRun(t *testing.T) {
	for _, tc := range []struct {
		name, run string
		ok        bool
	}{
		{"r_abc123", "r_abc123", true},
		{"r_abc123--worker-a", "r_abc123", true},
		{"r_abc123--team", "r_abc123", true},
		{"someone-else", "", false},
		{"r_ABC--x", "", false},
	} {
		run, ok := staleRun(tc.name)
		if run != tc.run || ok != tc.ok {
			t.Errorf("staleRun(%s) = %s %v", tc.name, run, ok)
		}
	}
}

func TestParseDetailAgentFields(t *testing.T) {
	raw := `{"id":"row1","timestamp":"2026-11-18T00:00:00Z","object":"chat_completion","provider":"anthropic","model":"m","status":"success",` +
		`"virtual_key_id":"vk-9","metadata":{"heimdall-agent":"worker-b"}}`
	p, err := parseDetail([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p.KeyID != "vk-9" || p.Req.AgentLabel != "worker-b" || p.Req.AgentID != "" {
		t.Errorf("parsed = key %s label %s id %s", p.KeyID, p.Req.AgentLabel, p.Req.AgentID)
	}
}
