package mcptest_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/mcptest"
)

type refusedTransport struct{}

func (refusedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp 172.22.0.2:8080: refused to connect to private or local address 172.22.0.2")
}

func TestMCPInspectErrorHasNoAddress(t *testing.T) {
	rep := mcptest.Inspect(context.Background(), mcptest.Target{URL: "https://mcp.example.com/mcp"}, &http.Client{Transport: refusedTransport{}}, mcptest.Options{})
	if rep.Reached || rep.Error == "" {
		t.Fatalf("want an unreached report with an error: %+v", rep)
	}
	if strings.Contains(rep.Error, "172.22.0.2") {
		t.Fatalf("report error names the address: %q", rep.Error)
	}
}
