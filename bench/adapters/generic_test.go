package adapters

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestGenericJSONParsersNormalizeCommonEvents(t *testing.T) {
	for _, name := range []string{ParserCustomPython, ParserGeminiCLI, ParserMiniSWE} {
		t.Run(name, func(t *testing.T) {
			parser := newGenericJSONParser(name)
			result, err := parser.Parse(context.Background(), &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}}, []byte(
				`{"type":"tool_call","tool_name":"shell","message":"pytest"}`+"\n"+`{"type":"tool_result","output":"ok"}`+"\n"+`{"type":"agent_message","text":"task is complete"}`+"\n"))
			if err != nil || result == nil || len(result.Events) != 3 {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
			if result.Events[0].Type != schemas.EventToolCall || result.Events[1].Type != schemas.EventToolResult {
				t.Fatalf("events = %+v", result.Events)
			}
			if result.Claim == nil || !result.Claim.Made {
				t.Fatalf("claim = %+v", result.Claim)
			}
		})
	}
}
