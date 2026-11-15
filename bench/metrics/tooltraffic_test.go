package metrics

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestToolTrafficUnparsed(t *testing.T) {
	noProxy := &schemas.RunContext{Task: &schemas.Task{Services: []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleData}}}}
	call := ledgerCall(1, "fs", "read", false)
	call.RequestBytes = 40
	opaque := schemas.ToolCall{Seq: 2, Server: "fs", Tool: schemas.ToolRowUnparsedTool, Protocol: "mcp", Kind: schemas.ToolRowOpaque, Unparsed: true, UnparsedReason: "request body is not JSON-RPC"}
	bigArgs := ledgerCall(3, "fs", "write", false)
	bigArgs.Unparsed, bigArgs.UnparsedReason = true, "request body over the 32 MiB cap; arguments not kept"
	initialize := schemas.ToolCall{Seq: 4, Server: "fs", Tool: "initialize", Protocol: "mcp", Kind: schemas.ToolRowMCPMethod}
	tests := []struct {
		name  string
		rc    *schemas.RunContext
		calls []schemas.ToolCall
		want  schemas.Value
	}{
		{"no proxied service", noProxy, []schemas.ToolCall{}, schemas.NotApplicable()},
		{"ledger missing", toolRun(), nil, schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary)},
		{"empty ledger", toolRun(), []schemas.ToolCall{}, schemas.Observed(0, schemas.EvidenceBoundary)},
		{"everything parsed", toolRun(), []schemas.ToolCall{initialize, call}, schemas.Observed(0, schemas.EvidenceBoundary)},
		{"opaque and oversized rows count", toolRun(), []schemas.ToolCall{initialize, call, opaque, bigArgs}, schemas.Observed(2, schemas.EvidenceBoundary)},
		{"ledger from before the recording proxy", toolRun(), []schemas.ToolCall{ledgerCall(1, "fs", "read", false)}, schemas.Unknown(reasonOldLedger, schemas.EvidenceBoundary)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newToolTrafficUnparsedMetric().Compute(context.Background(), tt.rc, &schemas.Evidence{ToolCalls: tt.calls})
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v %v, want %+v", got, err, tt.want)
			}
		})
	}
}

// TestToolLedgerMetricsCountOnlyToolCalls checks that rows the proxy writes
// for traffic other than tool calls (handshakes, listings, notifications,
// server requests, opaque traffic) never count as proxied tool calls.
func TestToolLedgerMetricsCountOnlyToolCalls(t *testing.T) {
	rows := []schemas.ToolCall{
		{Seq: 1, Server: "fs", Tool: "initialize", Protocol: "mcp", Kind: schemas.ToolRowMCPMethod},
		{Seq: 2, Server: "fs", Tool: "tools/list", Protocol: "mcp", Kind: schemas.ToolRowMCPMethod},
		{Seq: 3, Server: "fs", Tool: "tools/list", Protocol: "mcp"}, // a listing written before kinds existed
		{Seq: 4, Server: "fs", Tool: "notifications/progress", Protocol: "mcp", Kind: schemas.ToolRowNotification},
		{Seq: 5, Server: "fs", Tool: "sampling/createMessage", Protocol: "mcp", Kind: schemas.ToolRowServerRequest},
		{Seq: 6, Server: "fs", Tool: schemas.ToolRowUnparsedTool, Protocol: "mcp", Kind: schemas.ToolRowOpaque, Unparsed: true, IsError: true},
		{Seq: 7, Server: "fs", Tool: "GET /mcp", Protocol: "mcp", Kind: schemas.ToolRowTransport, IsError: true},
		ledgerCall(8, "fs", "read", false),
		ledgerCall(9, "fs", "write", true),
	}
	ev := &schemas.Evidence{ToolCalls: rows}
	got, _ := newToolCallsProxiedMetric().Compute(context.Background(), toolRun(), ev)
	if !reflect.DeepEqual(got, schemas.Observed(2, schemas.EvidenceBoundary)) {
		t.Fatalf("tool_calls_proxied = %+v, want 2", got)
	}
	rate, _ := newToolErrorRateMetric().Compute(context.Background(), toolRun(), ev)
	if rate.V == nil || *rate.V != 0.5 {
		t.Fatalf("tool_error_rate = %+v, want 0.5", rate)
	}
	distinct, _ := newDistinctToolsMetric().Compute(context.Background(), toolRun(), ev)
	if !reflect.DeepEqual(distinct, schemas.Observed(2, schemas.EvidenceBoundary)) {
		t.Fatalf("distinct_tools = %+v, want 2", distinct)
	}
}
