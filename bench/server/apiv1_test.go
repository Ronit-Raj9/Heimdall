package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// Every v1 route is served by a bench route the org router registers, the
// hosted policy has a rule for, and the OpenAPI table describes.
func TestV1RoutesAreServed(t *testing.T) {
	routes := OrgRoutes(testConfig())
	pol := tenancy.DefaultPolicy()
	docs := map[string]bool{}
	for _, rd := range append(append([]handlers.RouteDoc(nil), handlers.APIRoutes...), handlers.OrgRouteDocs...) {
		docs[rd.Method+" "+rd.Path] = true
	}
	seen := map[string]bool{}
	for _, r := range handlers.V1Routes {
		k := r.Method + " " + r.Path
		if seen[k] {
			t.Errorf("duplicate v1 route %s", k)
		}
		seen[k] = true
		if !strings.HasPrefix(r.Path, handlers.APIV1Prefix+"/") || !strings.HasPrefix(r.Bench, handlers.APIPrefix+"/") {
			t.Errorf("%s: bad prefixes (bench %s)", k, r.Bench)
		}
		if !slices.Contains(routes[r.Method], r.Bench) {
			t.Errorf("%s: bench route %s %s is not registered", k, r.Method, r.Bench)
		}
		if _, ok := pol.OrgRule(r.Method, r.Bench); !ok {
			t.Errorf("%s: no tenancy rule for %s %s", k, r.Method, r.Bench)
		}
		if !docs[r.Method+" "+r.Bench] {
			t.Errorf("%s: %s %s has no RouteDoc", k, r.Method, r.Bench)
		}
	}
}

func TestV1AliasServesBenchRoutes(t *testing.T) {
	t.Setenv(lib.WriteTokenEnv, "tok")
	s, err := New(testConfig(), Deps{Version: "v-test"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := startServer(t, s)
	const host = "127.0.0.1:8090"

	// Same handler, same answer as the bench route; the version header is set.
	v1 := get(t, c, "GET", "/api/v1/agents", host, nil, nil)
	bench := get(t, c, "GET", "/api/bench/agents", host, nil, nil)
	if v1.StatusCode() != bench.StatusCode() || string(v1.Body()) != string(bench.Body()) {
		t.Fatalf("v1 %d %s != bench %d %s", v1.StatusCode(), v1.Body(), bench.StatusCode(), bench.Body())
	}
	if got := string(v1.Header.Peek(handlers.APIVersionHeader)); got != "1" {
		t.Errorf("api version header = %q", got)
	}
	if got := string(bench.Header.Peek(handlers.APIVersionHeader)); got != "" {
		t.Errorf("bench route carries api version header %q", got)
	}

	// Renamed route: /benchmarks is served by /suites, query kept.
	v1b := get(t, c, "GET", "/api/v1/benchmarks?x=1", host, nil, nil)
	bb := get(t, c, "GET", "/api/bench/suites?x=1", host, nil, nil)
	if v1b.StatusCode() != bb.StatusCode() || string(v1b.Body()) != string(bb.Body()) {
		t.Fatalf("benchmarks v1 %d %s != suites %d %s", v1b.StatusCode(), v1b.Body(), bb.StatusCode(), bb.Body())
	}

	// Writes keep the write token check of their bench route.
	if r := get(t, c, "POST", "/api/v1/estimate", host, []byte("{}"), nil); r.StatusCode() != 401 {
		t.Errorf("v1 write without token = %d %s, want 401", r.StatusCode(), r.Body())
	}

	// Routes outside the stable subset are not reachable through v1.
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/experiments", 404},
		{"GET", "/api/v1/project/export.zip", 404},
		{"GET", "/api/v1/runs/r_1/files/x", 404},
		{"GET", "/api/v1", 404},
		{"DELETE", "/api/v1/agents", 405},
		{"POST", "/api/v1/openapi.json", 405},
	} {
		r := get(t, c, tc.method, tc.path, host, nil, nil)
		if r.StatusCode() != tc.status {
			t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, r.StatusCode(), tc.status, r.Body())
		}
	}

	// The v1 document lists exactly the v1 routes.
	r := get(t, c, "GET", "/api/v1/openapi.json", host, nil, nil)
	if r.StatusCode() != 200 {
		t.Fatalf("openapi = %d %s", r.StatusCode(), r.Body())
	}
	var doc struct {
		OpenAPI string                               `json:"openapi"`
		Info    map[string]any                       `json:"info"`
		Paths   map[string]map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(r.Body(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") || doc.Info["version"] != handlers.APIV1Version {
		t.Errorf("openapi %q info %v", doc.OpenAPI, doc.Info)
	}
	n := 0
	for p, ops := range doc.Paths {
		if !strings.HasPrefix(p, "/api/v1/") {
			t.Errorf("path %s outside v1", p)
		}
		for m, op := range ops {
			n++
			if op["x-heimdall-token-scope"] == nil {
				t.Errorf("%s %s lacks x-heimdall-token-scope", m, p)
			}
			if tag, _ := op["tags"].([]any); len(tag) == 1 && tag[0] == "v1" {
				t.Errorf("%s %s undocumented", m, p)
			}
		}
	}
	if n != len(handlers.V1Routes) {
		t.Errorf("document has %d operations, want %d", n, len(handlers.V1Routes))
	}
	if op := doc.Paths["/api/v1/evaluations"]["post"]; op["x-heimdall-token-scope"] != "evaluate" || op["security"] == nil {
		t.Errorf("POST /evaluations op = %v", op)
	}
}

func TestV1HostedDocument(t *testing.T) {
	doc := handlers.BuildV1OpenAPI("v", "live", true)
	paths := doc["paths"].(map[string]map[string]any)
	op := paths["/api/v1/runs/{id}"]["get"].(map[string]any)
	if op["x-heimdall-token-scope"] != "read" || op["security"] == nil {
		t.Errorf("GET /runs/{id} = %v", op)
	}
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if _, ok := schemes["apiToken"]; !ok {
		t.Errorf("hosted schemes %v", schemes)
	}
}
