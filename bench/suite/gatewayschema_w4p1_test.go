package suite

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSchemasAcceptGatewayTestAndPrompt checks the JSON schemas accept the
// W4 Bifrost feature test fields: gateway_test on an experiment, and
// gateway.bifrost.prompt and gateway.bifrost.mcp.tools on an agent.
func TestSchemasAcceptGatewayTestAndPrompt(t *testing.T) {
	exp := "name: x\nsuite: s@1.0.0\nbase: agents/a.yaml\narms: {A: {label: a}, B: {label: b}}\nprimary: {condition: clean, metric: verified_success}\nguards: []\nconditions: [{id: clean, tasks: all, trials: 1}]\nbudget: {total_usd: 1}\nverdict: {policy: paired_signflip}\nseed: 1\nconcurrency: 1\nmanifest_mode: agent_exposure\n" +
		"gateway_test: {feature: complexity_router, control: A, variants: [B], options: {tolerance: \"0.03\"}}\n"
	var e schemas.Experiment
	if err := DecodeYAML([]byte(exp), "x.yaml", SchemaExperiment, &e); err != nil {
		t.Fatal(err)
	}
	if e.GatewayTest == nil || e.GatewayTest.Feature != "complexity_router" || e.GatewayTest.Options["tolerance"] != "0.03" {
		t.Fatalf("gateway test: %+v", e.GatewayTest)
	}
	if err := DecodeYAML([]byte(strings.Replace(exp, "control: A, ", "", 1)), "x.yaml", SchemaExperiment, &schemas.Experiment{}); err == nil {
		t.Fatal("a gateway test without a control was accepted")
	}
	agent := "id: a\nadapter: command\nimage: bench/a:1\nargv: [run]\nmodel: m1\ngateway:\n  provider: openai\n  bifrost:\n    mcp: {code_mode: false, tools: [files/read, \"web/*\"], forbidden: [files/delete]}\n" +
		"    prompt: {name: triage, version: 3, source_id: p1, messages: [{role: system, content: hi}], model_params: {temperature: 0}, sha256: " + strings.Repeat("ab", 32) + "}\n"
	var a schemas.AgentConfig
	if err := DecodeYAML([]byte(agent), "a.yaml", SchemaAgent, &a); err != nil {
		t.Fatal(err)
	}
	if err := DecodeYAML([]byte(strings.Replace(agent, "sha256: ", "sha256: x", 1)), "a.yaml", SchemaAgent, &schemas.AgentConfig{}); err == nil {
		t.Fatal("a prompt with a bad sha256 was accepted")
	}
}
