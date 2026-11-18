package environments

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestGatewayUpstream(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"http://toolproxy:9200/s/desk/mcp", "http://tp-r_1:9200/s/desk/mcp"},
		{"http://tools.internal:9200/s/desk/mcp", "http://tp-r_1:9200/s/desk/mcp"},
		{"http://toolproxy/s/desk/mcp", "http://tp-r_1:9200/s/desk/mcp"},
	} {
		got, err := gatewayUpstream(tt.in, toolProxyGatewayAlias("r_1"))
		if err != nil || got != tt.want {
			t.Errorf("gatewayUpstream(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := gatewayUpstream("not a url", "tp-r_1"); err == nil {
		t.Error("a URL without a host must be refused")
	}
	n := ledgerToolCalls([]schemas.ToolCall{{Protocol: "mcp"}, {Protocol: "mcp", Kind: "mcp_method"}, {Protocol: "http"}, {Protocol: "mcp"}})
	if n != 2 {
		t.Errorf("ledgerToolCalls = %d, want 2", n)
	}
}
