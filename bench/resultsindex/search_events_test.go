package resultsindex

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// evidenceDoc builds a search document from an evidence.json body, the way
// buildDoc reads it.
func evidenceDoc(t *testing.T, ev map[string]any) *searchDoc {
	t.Helper()
	var se searchEvidence
	if err := json.Unmarshal(mustJSON(t, ev), &se); err != nil {
		t.Fatal(err)
	}
	return docFrom("s", &se)
}

func mustQuery(t *testing.T, raw string) SearchQuery {
	t.Helper()
	q, err := ParseSearchQuery(raw, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// matchFields returns the fields of a hit's matches, or nil when q misses.
func matchFields(t *testing.T, d *searchDoc, raw string) []string {
	t.Helper()
	hit, ok := match(d, mustQuery(t, raw))
	if !ok {
		return nil
	}
	out := []string{}
	for _, m := range hit.Matches {
		out = append(out, m.Field)
	}
	return out
}

func hasField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// A codex run (row N49): the model error text is only in the gateway rows
// and the events, the tool command only in the events, the final message
// only in the claim. Each is found, under its own field.
func TestSearchEventsErrorsAndClaim(t *testing.T) {
	d := evidenceDoc(t, map[string]any{
		"steps": []schemas.StepRecord{{Seq: 1, Kind: schemas.StepToolRequest, ToolName: "exec", Summary: "exec"}},
		"events": []schemas.Event{
			{Seq: 4, Type: schemas.EventToolCall, ToolName: "exec_command", Target: "python get_aws_pricing.py --region us-east-1", Summary: "exec_command"},
			{Seq: 5, Type: schemas.EventModelResponse, Summary: "error 503: The server is overloaded or not ready yet", IsError: true},
		},
		"requests": schemas.RequestLog{Requests: []schemas.GatewayRequest{{ModelServed: "gpt-6-luna", ErrorMessage: "upstream says: capacity exhausted for this model"}}},
		"claim":    schemas.Claim{Made: true, Text: "The cheapest instance is t4g.nano in us-east-1."},
	})
	for _, c := range []struct{ q, field string }{
		{"overloaded", SearchFieldError},
		{"capacity exhausted", SearchFieldError},
		{"get_aws_pricing", SearchFieldToolCall},
		{"t4g.nano", SearchFieldAnswer},
	} {
		if f := matchFields(t, d, c.q); !hasField(f, c.field) {
			t.Errorf("%q: fields %v, want %s", c.q, f, c.field)
		}
	}
	if !d.tools["exec_command"] {
		t.Errorf("event tool name not collected: %v", d.tools)
	}
}

// The same line in a step and an event is one passage, not two.
func TestSearchDedupesStepAndEventText(t *testing.T) {
	d := evidenceDoc(t, map[string]any{
		"steps":  []schemas.StepRecord{{Seq: 1, Kind: schemas.StepToolRequest, ToolName: "bash", Summary: "bash ls -la /work"}},
		"events": []schemas.Event{{Seq: 1, Type: schemas.EventToolCall, ToolName: "bash", Summary: "bash ls -la /work"}, {Seq: 2, Type: schemas.EventToolCall, ToolName: "bash", Summary: "Bash LS -la /work"}},
		"answer": schemas.Answer{Text: "listed"},
		"claim":  schemas.Claim{Text: "claim text is ignored when an answer exists"},
	})
	n := 0
	for _, p := range d.passages {
		if p.lower == "bash ls -la /work" {
			n++
		}
		if p.field == SearchFieldAnswer && p.text != "listed" {
			t.Errorf("claim added although the run has an answer: %q", p.text)
		}
	}
	if n != 1 {
		t.Fatalf("passages with the shared line: %d, want 1 (%+v)", n, d.passages)
	}
}

// A plain word that is a tool name finds the run even when only a witness
// step names the tool (row N50): plain-word results include tool: results.
func TestSearchPlainWordFindsToolName(t *testing.T) {
	d := evidenceDoc(t, map[string]any{
		"steps": []schemas.StepRecord{
			{Seq: 3, Kind: schemas.StepToolRequest, ToolName: "exec", Summary: "exec"},
			{Seq: 4, Kind: schemas.StepToolExecution, ToolName: "exec_command", Summary: "tool result exec_command exit 0:", WitnessOf: 3},
		},
	})
	tool := matchFields(t, d, "tool:exec_command")
	plain := matchFields(t, d, "exec_command")
	if tool == nil {
		t.Fatal("tool:exec_command misses")
	}
	if plain == nil {
		t.Fatal("plain exec_command misses although the run called that tool")
	}
	if len(plain) < len(tool) {
		t.Fatalf("plain word shows fewer matches (%v) than tool: (%v)", plain, tool)
	}
}
