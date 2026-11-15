package evidence

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestTimelineSkipsNonCallLedgerRows checks that the rows the tool proxy
// writes for traffic other than tool calls (handshake, listings,
// notifications, server requests, opaque traffic) never become tool
// executions on the timeline: only tool calls do.
func TestTimelineSkipsNonCallLedgerRows(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ev := &schemas.Evidence{ToolCalls: []schemas.ToolCall{
		{Seq: 1, Server: "fs", Tool: "initialize", Protocol: "mcp", Kind: schemas.ToolRowMCPMethod, Started: at},
		{Seq: 2, Server: "fs", Tool: "tools/list", Protocol: "mcp", Started: at},
		{Seq: 3, Server: "fs", Tool: "notifications/progress", Protocol: "mcp", Kind: schemas.ToolRowNotification, Started: at},
		{Seq: 4, Server: "fs", Tool: "sampling/createMessage", Protocol: "mcp", Kind: schemas.ToolRowServerRequest, Started: at},
		{Seq: 5, Server: "fs", Tool: schemas.ToolRowUnparsedTool, Protocol: "mcp", Kind: schemas.ToolRowOpaque, Unparsed: true, Started: at},
		{Seq: 6, Server: "fs", Tool: "read_file", Protocol: "mcp", Started: at},
	}}
	steps, _ := BuildTimeline(ev)
	n := 0
	for _, s := range steps {
		if s.Kind == schemas.StepToolExecution {
			n++
			if s.ToolName != "read_file" {
				t.Errorf("non-call row on the timeline: %+v", s)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d tool executions, want 1", n)
	}
}
