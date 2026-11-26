package environments

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// filterCheckGateway records each CheckForbiddenTool call and refuses it.
type filterCheckGateway struct {
	calls []string
}

func (g *filterCheckGateway) CheckForbiddenTool(_ context.Context, slug, key, tool, marker string) schemas.BifrostMCPFilterCheck {
	g.calls = append(g.calls, slug+" "+key+" "+tool+" "+marker)
	return schemas.BifrostMCPFilterCheck{Tool: tool, Marker: marker, Listed: true, Refused: true}
}

func forbiddenRC(forbidden ...string) *schemas.RunContext {
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_abc123"}, Agent: &schemas.AgentConfig{}}
	rc.Agent.Gateway.Bifrost = &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{Forbidden: forbidden}}
	return rc
}

func TestCheckForbiddenCallsEachEntry(t *testing.T) {
	gw := &filterCheckGateway{}
	clients := []schemas.BifrostMCPClient{{Service: "desk", Slug: "desk-r-abc123"}, {Service: "shop", Slug: "shop-r-abc123"}}
	key := &schemas.RunKey{ID: "vk-mcp", Value: "sk-bf-mcp"}
	got := checkForbidden(context.Background(), gw, forbiddenRC("desk/issue_refund", "shop/cancel_order", "mail/send"), clients, key)
	if len(got) != 3 {
		t.Fatalf("checks = %+v, want 3", got)
	}
	want := []string{"desk-r-abc123 sk-bf-mcp issue_refund r_abc123", "shop-r-abc123 sk-bf-mcp cancel_order r_abc123"}
	if len(gw.calls) != 2 || gw.calls[0] != want[0] || gw.calls[1] != want[1] {
		t.Fatalf("calls = %v, want %v", gw.calls, want)
	}
	if got[0].Service != "desk" || !got[0].Refused || got[1].Service != "shop" {
		t.Fatalf("checks = %+v", got)
	}
	if got[2].Service != "mail" || got[2].Refused || got[2].Detail == "" {
		t.Fatalf("a service without a client: check = %+v, want a detail and no refusal", got[2])
	}
	if checkForbidden(context.Background(), gw, forbiddenRC(), clients, key) != nil {
		t.Fatal("no forbidden entries should give no checks")
	}
}

func TestCheckForbiddenWithoutMethod(t *testing.T) {
	clients := []schemas.BifrostMCPClient{{Service: "desk", Slug: "desk-r-abc123"}}
	got := checkForbidden(context.Background(), struct{}{}, forbiddenRC("desk/issue_refund"), clients, &schemas.RunKey{Value: "k"})
	if len(got) != 1 || got[0].Detail != "this gateway cannot run the filter check" || got[0].Tool != "issue_refund" || got[0].Service != "desk" {
		t.Fatalf("checks = %+v", got)
	}
}
