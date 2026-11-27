package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/tenancy"
)

// TestHostedCGSummaryRoutesStayInOrg: through the real hosted dispatcher
// and two organizations, the routes that call other routes in process
// (home summary, evaluation card, MCP options) answer each caller from its
// own organization only, admit a viewer, refuse a user of no organization,
// and never reach another organization by naming it.
func TestHostedCGSummaryRoutesStayInOrg(t *testing.T) {
	f := newHostedFixture(t)
	const home = "/api/bench/home/summary"
	for _, c := range []struct{ who, own, other string }{{"alice", f.aExp, f.bExp}, {"vera", f.aExp, f.bExp}, {"bob", f.bExp, f.aExp}} {
		r := f.do("GET", home, c.who, nil, nil)
		if r.status != 200 || !strings.Contains(r.body, c.own) || strings.Contains(r.body, c.other) {
			t.Fatalf("%s home summary: %d %s", c.who, r.status, trim(r.body))
		}
	}
	for _, path := range []string{home, home + "?" + tenancy.OrgQueryParam + "=beta"} {
		if r := f.do("GET", path, "mallory", nil, nil); r.status < 400 || strings.Contains(r.body, f.aExp) || strings.Contains(r.body, f.bExp) {
			t.Fatalf("mallory %s: %d %s", path, r.status, trim(r.body))
		}
	}
	if r := f.do("GET", home+"?"+tenancy.OrgQueryParam+"=beta", "alice", nil, nil); r.status != 403 && r.status != 404 {
		t.Fatalf("alice naming beta: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", home, "alice", map[string]string{tenancy.OrgHeader: "beta"}, nil); r.status != 403 && r.status != 404 {
		t.Fatalf("alice with header beta: %d %s", r.status, trim(r.body))
	}
	// An id that is not the caller's is a plain 404, whatever exists elsewhere.
	for _, id := range []string{f.bExp, "ev_missing", "..%2Fusage"} {
		if r := f.do("GET", "/api/bench/evaluations/"+id+"/card", "alice", nil, nil); r.status != 404 {
			t.Fatalf("card %s: %d %s", id, r.status, trim(r.body))
		}
	}
	if r := f.do("GET", "/api/bench/gateway/mcp-options?suite=coding-core", "vera", nil, nil); r.status != 200 || !strings.Contains(r.body, `"suite":"coding-core"`) {
		t.Fatalf("mcp options: %d %s", r.status, trim(r.body))
	}
	for _, q := range []string{"", "?suite=" + strings.Repeat("x", 300)} {
		if r := f.do("GET", "/api/bench/gateway/mcp-options"+q, "vera", nil, nil); r.status != 400 {
			t.Fatalf("mcp options %q: %d", q, r.status)
		}
	}
	if r := f.do("GET", "/api/bench/gateway/mcp-options?suite=..%2F..%2Fusage", "vera", nil, nil); r.status != 404 {
		t.Fatalf("mcp options traversal: %d %s", r.status, trim(r.body))
	}
}
