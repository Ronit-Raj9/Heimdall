package agentinfer

import (
	"slices"
	"testing"
)

// TestInferModelFromSpanWithoutOperationName: a model span from an SDK that
// sets gen_ai.system and gen_ai.request.model but no gen_ai.operation.name
// names the agent's model; so does any other non-tool span with a model
// attribute (N109).
func TestInferModelFromSpanWithoutOperationName(t *testing.T) {
	body := otlpJSON("my-agent", "opentelemetry.instrumentation.openai", tspan{trace: t1, id: "b000000000000001", name: "openai.chat", attrs: map[string]string{
		"gen_ai.system": "openai", "gen_ai.request.model": "gpt-4o-mini"}})
	d, err := InferFromOTLP(body, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if d.Model == nil || d.Model.Model != "gpt-4o-mini" {
		t.Fatalf("model %+v, want gpt-4o-mini", d.Model)
	}
	if slices.Contains(d.Questions, "No model name in your traces. Which model does the agent use?") {
		t.Fatalf("still asks for the model: %v", d.Questions)
	}
	// A planning span that carries the model counts too.
	body = otlpJSON("my-agent", "custom", tspan{trace: t1, id: "b000000000000002", name: "plan next step", attrs: map[string]string{
		"gen_ai.request.model": "claude-x"}})
	if d, err = InferFromOTLP(body, "application/json"); err != nil || d.Model == nil || d.Model.Model != "claude-x" {
		t.Fatalf("plan span: %v %+v", err, d.Model)
	}
}
