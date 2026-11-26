package schemas_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const recordedExperimentJSON = `{"id":"e1","suite":"s@1","base":"agents/a.yaml","arms":{"A":{"label":"control"},"B":{"label":"changed","set":{"model":"m2"}}},"primary":{"condition":"clean","metric":"verified_success"}}`

// TestExperimentWithoutGatewayTestHashesAsBefore checks that an experiment
// without a gateway test keeps its canonical hash, and that one with a test
// carries it under gateway_test.
func TestExperimentWithoutGatewayTestHashesAsBefore(t *testing.T) {
	var raw any
	if err := json.Unmarshal([]byte(recordedExperimentJSON), &raw); err != nil {
		t.Fatal(err)
	}
	var e schemas.Experiment
	if err := json.Unmarshal([]byte(recordedExperimentJSON), &e); err != nil {
		t.Fatal(err)
	}
	if e.GatewayTest != nil {
		t.Fatalf("GatewayTest = %+v, want nil", e.GatewayTest)
	}
	before, err := utils.HashJSON(e)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("gateway_test")) {
		t.Fatalf("plain experiment serialized gateway_test: %s", out)
	}
	var again schemas.Experiment
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	after, err := utils.HashJSON(again)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("hash changed: %s then %s", before, after)
	}
	e.GatewayTest = &schemas.GatewayTest{Feature: schemas.GatewayFeatureComplexityRouter, Control: "A", Variants: []string{"B"}}
	out, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"gateway_test":{"feature":"complexity_router","control":"A","variants":["B"]}`) {
		t.Fatalf("gateway test not serialized: %s", out)
	}
	withTest, _ := utils.HashJSON(e)
	if withTest == before {
		t.Fatal("a gateway test did not change the hash")
	}
}

// TestBifrostFeatureFieldsW4 checks the new gateway.bifrost fields round trip.
func TestBifrostFeatureFieldsW4(t *testing.T) {
	f := schemas.BifrostFeatures{
		MCP:    &schemas.BifrostMCP{Tools: []string{"s1/*"}, Forbidden: []string{"s1/drop"}},
		Prompt: &schemas.BifrostPrompt{Name: "p", Version: 2, Messages: []map[string]any{{"role": "system"}}, SHA256: strings.Repeat("a", 64)},
	}
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tools":["s1/*"]`, `"forbidden":["s1/drop"]`, `"prompt":{"name":"p","version":2`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("%s missing from %s", want, out)
		}
	}
	ev := schemas.BifrostMCPEvidence{FilterChecks: []schemas.BifrostMCPFilterCheck{{Service: "s1", Tool: "drop", Refused: true}}}
	out, _ = json.Marshal(ev)
	if !strings.Contains(string(out), `"filter_checks":[{"service":"s1","tool":"drop","listed":false,"refused":true,"marker":""}]`) {
		t.Fatalf("filter checks: %s", out)
	}
	rc := schemas.RunContext{GatewayHeaders: map[string]string{"x-bf-prompt-id": "p"}}
	if out, _ := json.Marshal(rc); strings.Contains(string(out), "x-bf-prompt-id") {
		t.Fatalf("GatewayHeaders serialized: %s", out)
	}
}
