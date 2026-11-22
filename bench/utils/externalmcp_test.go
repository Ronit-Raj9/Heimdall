package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// An MCP server under test runs live without an experiment opt-in, gets the
// default allowlist, and is listed to the agent as an MCP server at the
// proxy URL with no path added.
func TestExternalMCPServer(t *testing.T) {
	api := schemas.ExternalAPI{ID: "srv", BaseURL: "https://mcp.example.com/mcp", Mode: schemas.ExternalLive, MCP: true}
	if m, err := ExternalMode(api, "", false); err != nil || m != schemas.ExternalLive {
		t.Fatalf("mode = %q, %v", m, err)
	}
	plain := api
	plain.MCP = false
	if _, err := ExternalMode(plain, "", false); err == nil {
		t.Fatal("a plain live API ran without the experiment opting in")
	}
	if al := ExternalAllowFor(api); len(al) != 1 || al[0].PathPrefix != "/" || len(al[0].Methods) != 2 {
		t.Fatalf("allow = %+v", al)
	}
	if al := ExternalAllowFor(plain); len(al) != 0 {
		t.Fatalf("plain API got a default allowlist: %+v", al)
	}
	hs := []schemas.ServiceHandle{
		{Spec: schemas.ServiceSpec{ID: "srv", Role: schemas.ServiceRoleExternal, MCP: &schemas.MCPEndpoint{}}, URL: ExternalURL("srv")},
		{Spec: schemas.ServiceSpec{ID: "fx", Role: schemas.ServiceRoleExternal}, URL: ExternalURL("fx")},
	}
	got := MCPServers(hs)
	if len(got) != 1 || got["srv"] != "http://toolproxy:9200/x/srv" {
		t.Fatalf("mcp servers = %v", got)
	}
}
