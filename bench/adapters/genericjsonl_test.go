package adapters

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestGenericJSONLParser(t *testing.T) {
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r9"}, Key: &schemas.RunKey{Value: "sk-run-key-0123456789abcdef"}}
	raw := []byte("{\"type\":\"plan\",\"output\":\"x\"}\nnoise\n@@heimdall-event {\"type\":\"tool_call\",\"name\":\"grep\",\"input\":\"sk-run-key-0123456789abcdef\"}\n{\"type\":\"message\",\"output\":\"All tests pass.\"}\n")
	res, err := newGenericJSONLParser().Parse(context.Background(), rc, raw)
	if err != nil || len(res.Events) != 3 {
		t.Fatalf("%v %+v", err, res)
	}
	for _, e := range res.Events {
		if e.Source != schemas.SourceAgentReported || e.RunID != "r9" || e.Channel != schemas.ReportChannelStdout {
			t.Fatalf("event %+v", e)
		}
	}
	if res.Events[1].Target == "sk-run-key-0123456789abcdef" {
		t.Fatal("run key not redacted")
	}
	if res.Claim == nil || !res.Claim.Made {
		t.Fatalf("claim %+v", res.Claim)
	}
}
