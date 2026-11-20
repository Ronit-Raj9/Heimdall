package metrics

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func b2ms(v float64) *float64 { return &v }

func b2rows(rows ...schemas.GatewayRequest) *schemas.Evidence {
	return &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true, Requests: rows}}
}

func b2msg(id string, overhead *float64, tok *schemas.Tokens) schemas.GatewayRequest {
	return schemas.GatewayRequest{ID: id, Kind: "message", Status: "success", OverheadMS: overhead, Tokens: tok}
}

func b2compute(t *testing.T, m schemas.Metric, ev *schemas.Evidence) schemas.Value {
	t.Helper()
	v, err := m.Compute(context.Background(), nil, ev)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func b2observed(t *testing.T, v schemas.Value, want float64) {
	t.Helper()
	got, ok := v.Float()
	if !ok || got != want {
		t.Fatalf("value = %+v, want observed %v", v, want)
	}
}

func TestGatewayOverhead(t *testing.T) {
	ev := b2rows(b2msg("a", b2ms(0.5), nil), b2msg("b", b2ms(1.5), nil), b2msg("c", b2ms(9.25), nil), b2msg("d", b2ms(1.0), nil),
		schemas.GatewayRequest{ID: "e", Kind: "count_tokens", OverheadMS: b2ms(100)})
	b2observed(t, b2compute(t, newGatewayOverheadMetric(GatewayOverheadMS), ev), 1.25)    // median of 0.5, 1.0, 1.5, 9.25
	b2observed(t, b2compute(t, newGatewayOverheadMetric(GatewayOverheadP95MS), ev), 9.25) // nearest rank: ceil(0.95*4) = 4th
	partial := b2rows(b2msg("a", b2ms(2), nil), b2msg("b", nil, nil))
	v := b2compute(t, newGatewayOverheadMetric(GatewayOverheadMS), partial)
	b2observed(t, v, 2)
	if v.Reason != "1 of 2 model rows record the overhead" {
		t.Fatalf("reason = %q", v.Reason)
	}
	if v := b2compute(t, newGatewayOverheadMetric(GatewayOverheadMS), b2rows(b2msg("a", nil, nil))); v.State != schemas.ValueUnknown {
		t.Fatalf("no overhead recorded: %+v, want unknown", v)
	}
	if v := b2compute(t, newGatewayOverheadMetric(GatewayOverheadMS), &schemas.Evidence{Requests: &schemas.RequestLog{Complete: false}}); v.State != schemas.ValueUnknown {
		t.Fatalf("incomplete log: %+v, want unknown", v)
	}
	if v := b2compute(t, newGatewayOverheadMetric(GatewayOverheadMS), b2rows()); v.State != schemas.ValueNotApplicable {
		t.Fatalf("no rows: %+v, want not applicable", v)
	}
}

func TestInputTokensAndCacheReadShare(t *testing.T) {
	ev := b2rows(
		b2msg("a", nil, &schemas.Tokens{Fresh: 37, CacheWrite: 347, Out: 16}),
		b2msg("b", nil, &schemas.Tokens{Fresh: 68, CacheRead: 347, Out: 16}),
		schemas.GatewayRequest{ID: "c", Kind: "message", Status: "error"}, // injected fault: no usage, skipped
	)
	b2observed(t, b2compute(t, newInputTokensMetric(), ev), 799)
	b2observed(t, b2compute(t, newCacheReadShareMetric(), ev), 0.434) // 347 / 799
	missing := b2rows(b2msg("a", nil, nil))
	if v := b2compute(t, newInputTokensMetric(), missing); v.State != schemas.ValueUnknown {
		t.Fatalf("missing usage: %+v, want unknown", v)
	}
	if v := b2compute(t, newCacheReadShareMetric(), b2rows(b2msg("a", nil, &schemas.Tokens{Out: 3}))); v.State != schemas.ValueNotApplicable {
		t.Fatalf("no input: %+v, want not applicable", v)
	}
}

func TestSemanticCacheHits(t *testing.T) {
	ev := b2rows(
		schemas.GatewayRequest{ID: "a", Kind: "message", Status: "success", ResponseID: "msg_1"},
		schemas.GatewayRequest{ID: "b", Kind: "message", Status: "success", ResponseID: "msg_1", CacheHit: true}, // replay of this run's own reply
		schemas.GatewayRequest{ID: "c", Kind: "message", Status: "success", ResponseID: "msg_9", CacheHit: true}, // first given elsewhere
	)
	b2observed(t, b2compute(t, newSemanticCacheMetric(SemanticCacheHits), ev), 2)
	b2observed(t, b2compute(t, newSemanticCacheMetric(SemanticCacheForeignHits), ev), 1)
}

func TestBifrostMCPWitness(t *testing.T) {
	ledger := []schemas.ToolCall{
		{Server: "desk", Tool: "get_ticket", Protocol: "mcp", Arguments: `{"ticket_id": "T-1001"}`},
		{Server: "desk", Tool: "get_ticket", Protocol: "mcp", Arguments: `{"ticket_id":"T-1001"}`},
		{Server: "desk", Tool: "issue_refund", Protocol: "mcp", Arguments: `{"order_id":"A-1001","amount_cents":4200}`},
		{Server: "desk", Kind: "tools_list", Protocol: "mcp"}, // not a tool call
	}
	hash := func(args map[string]any) string { return utils.CanonicalArgsSHA256(args) }
	calls := []schemas.BifrostMCPCall{
		{Service: "desk", Tool: "get_ticket", ArgsSHA256: hash(map[string]any{"ticket_id": "T-1001"})},
		{Service: "desk", Tool: "get_ticket", ArgsSHA256: hash(map[string]any{"ticket_id": "T-1001"})},
		{Service: "desk", Tool: "reply_to_ticket", ArgsSHA256: hash(map[string]any{"ticket_id": "T-1001"})},
	}
	only, matched := MCPWitnessDiff(ledger, calls)
	if matched != 2 || !reflect.DeepEqual(only, []string{"bifrost only: desk reply_to_ticket", "ledger only: desk issue_refund"}) {
		t.Fatalf("diff = %v matched %d", only, matched)
	}
	ev := &schemas.Evidence{ToolCalls: ledger, BifrostMCP: &schemas.BifrostMCPEvidence{Complete: true, Clients: []schemas.BifrostMCPClient{{Service: "desk"}}, Calls: calls}}
	b2observed(t, b2compute(t, newBifrostMCPMetric(BifrostMCPWitnessMismatch), ev), 2)
	b2observed(t, b2compute(t, newBifrostMCPMetric(BifrostMCPCalls), ev), 3)
	if v := b2compute(t, newBifrostMCPMetric(BifrostMCPWitnessMismatch), &schemas.Evidence{}); v.State != schemas.ValueNotApplicable {
		t.Fatalf("direct run: %+v, want not applicable", v)
	}
	// A missing ledger is no witness, not an empty one (audit 1-C-10).
	noLedger := &schemas.Evidence{BifrostMCP: ev.BifrostMCP}
	if v := b2compute(t, newBifrostMCPMetric(BifrostMCPWitnessMismatch), noLedger); v.State != schemas.ValueUnknown {
		t.Fatalf("missing ledger: %+v, want unknown", v)
	}
	ev.BifrostMCP.Complete, ev.BifrostMCP.Reason = false, "page 2 failed"
	if v := b2compute(t, newBifrostMCPMetric(BifrostMCPCalls), ev); v.State != schemas.ValueUnknown {
		t.Fatalf("incomplete log: %+v, want unknown", v)
	}
}
