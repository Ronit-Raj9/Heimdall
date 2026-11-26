package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func mcpToolsAgent(tools, forbidden []string) *schemas.AgentConfig {
	return bifrostAgent("", &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{Tools: tools, Forbidden: forbidden}})
}

func joinErrs(errs []error) string {
	var s []string
	for _, e := range errs {
		s = append(s, e.Error())
	}
	return strings.Join(s, "; ")
}

func TestValidateMCPToolsUnknownService(t *testing.T) {
	if errs := validateBifrostMCP(mcpToolsAgent([]string{"desk/get_ticket", "desk/*"}, []string{"desk/issue_refund"}), mcpSuite(true)); len(errs) != 0 {
		t.Fatalf("valid lists refused: %s", joinErrs(errs))
	}
	for name, cfg := range map[string]*schemas.AgentConfig{
		"unknown service":   mcpToolsAgent([]string{"shop/get_order"}, nil),
		"not service/tool":  mcpToolsAgent([]string{"get_ticket"}, nil),
		"forbidden unknown": mcpToolsAgent(nil, []string{"shop/cancel"}),
	} {
		if errs := validateBifrostMCP(cfg, mcpSuite(true)); len(errs) == 0 {
			t.Errorf("%s: want a refusal", name)
		}
	}
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, fmt.Sprintf("desk/t%d", i))
	}
	if errs := validateBifrostMCP(mcpToolsAgent(many, nil), mcpSuite(true)); !strings.Contains(joinErrs(errs), "at most 50") {
		t.Fatalf("51 tools: errs = %s", joinErrs(errs))
	}
}

func TestValidateMCPForbiddenCoveredByTools(t *testing.T) {
	for name, cfg := range map[string]*schemas.AgentConfig{
		"same name":    mcpToolsAgent([]string{"desk/issue_refund"}, []string{"desk/issue_refund"}),
		"service star": mcpToolsAgent([]string{"desk/*"}, []string{"desk/issue_refund"}),
	} {
		if errs := validateBifrostMCP(cfg, mcpSuite(true)); !strings.Contains(joinErrs(errs), "issue_refund") {
			t.Errorf("%s: errs = %s, want the covered tool named", name, joinErrs(errs))
		}
	}
}

func TestValidateMCPForbiddenStarRefused(t *testing.T) {
	if errs := validateBifrostMCP(mcpToolsAgent(nil, []string{"desk/*"}), mcpSuite(true)); len(errs) == 0 {
		t.Fatal("a forbidden service star should be refused")
	}
}
