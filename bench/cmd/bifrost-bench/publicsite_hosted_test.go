package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/dist"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestHostedPublicSiteAnonymous (lane Y08): the public site answers
// without sign-in, and its neighbours (in-app docs, the org catalog, suites)
// still answer 401.
func TestHostedPublicSiteAnonymous(t *testing.T) {
	dl := t.TempDir()
	if err := os.WriteFile(filepath.Join(dl, "bifrost-bench-linux-amd64"), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dist.WriteManifest(dl, "0123abcd", benchVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(DownloadsDirEnv, dl)
	f := newHostedFixture(t)
	for _, path := range []string{
		handlers.PublicPrefix + "/overview", handlers.PublicPrefix + "/benchmarks", handlers.PublicPrefix + "/docs",
		handlers.PublicPrefix + "/docs/quickstart", handlers.PublicPrefix + "/pricing", handlers.PublicPrefix + "/downloads",
		"/downloads/bifrost-bench-linux-amd64", "/downloads/SHA256SUMS", "/downloads/heimdall.gitlab-ci.yml", handlers.SecurityTxtPath,
	} {
		if r := f.do("GET", path, "", nil, nil); r.status != 200 {
			t.Errorf("anonymous GET %s: %d %s", path, r.status, trim(r.body))
		}
	}
	for _, path := range []string{"/api/bench/docs", "/api/bench/docs/quickstart", "/api/bench/benchmarks/catalog", "/api/bench/suites", "/api/bench/agents"} {
		if r := f.do("GET", path, "", nil, nil); r.status != 401 {
			t.Errorf("anonymous GET %s: %d, want 401", path, r.status)
		}
	}
	r := f.do("GET", handlers.PublicPrefix+"/downloads", "", nil, nil)
	var m handlers.DownloadsManifest
	if err := json.Unmarshal([]byte(r.body), &m); err != nil || m.Commit != "0123abcd" || m.MCPURL != "https://heimdall.test/mcp" {
		t.Fatalf("downloads: %v %s", err, trim(r.body))
	}
	gl := f.do("GET", "/downloads/heimdall.gitlab-ci.yml", "", nil, nil)
	if !strings.Contains(gl.body, `HEIMDALL_URL: "https://heimdall.test"`) {
		t.Fatalf("gitlab template lacks the server address:\n%s", trim(gl.body))
	}
	ov := f.do("GET", handlers.PublicPrefix+"/overview", "", nil, nil)
	if !strings.Contains(ov.body, `"mcp":true`) || !strings.Contains(ov.body, `"commit":"0123abcd"`) {
		t.Fatalf("overview: %s", ov.body)
	}
}

// TestBuildPublicLibrary: counts come from the summaries, org packs are left
// out, licences come from the tasks before the catalog, packs resolve.
func TestBuildPublicLibrary(t *testing.T) {
	sums := []schemas.SuiteSummary{
		{ID: "hd-swe-verified", Builtin: true, Description: "Real bugs", TaskCount: 10, AdmittedCount: 8,
			Tasks: []schemas.SuiteTask{{Provenance: &schemas.TaskProvenance{License: "MIT"}}, {Provenance: &schemas.TaskProvenance{License: "BSD-3-Clause"}}, {}}},
		{ID: "tau2", Builtin: true, TaskCount: 5, AdmittedCount: 5},
		{ID: "my-own", TaskCount: 99, AdmittedCount: 99},
		{ID: "broken", Builtin: true, TaskCount: 3, Error: "load failed: /srv/secret/path"},
	}
	l := buildPublicLibrary(sums, map[string]catalogInfo{"tau2": {title: "tau2-bench", summary: "Customer support", license: "MIT"}})
	if l.Totals.Benchmarks != 3 || l.Totals.Tasks != 15 || l.Totals.Ready != 13 {
		t.Fatalf("totals %+v", l.Totals)
	}
	by := map[string]handlers.PublicBenchmark{}
	for _, b := range l.Benchmarks {
		by[b.ID] = b
	}
	if _, ok := by["my-own"]; ok {
		t.Fatal("an org benchmark is on the public catalog")
	}
	if got := by["hd-swe-verified"].Licenses; len(got) != 2 || got[0] != "BSD-3-Clause" {
		t.Errorf("task licences %v", got)
	}
	if b := by["tau2"]; b.Title != "tau2-bench" || b.Description != "Customer support" || b.Licenses[0] != "MIT" || len(b.Packs) == 0 {
		t.Errorf("tau2 %+v", b)
	}
	if b := by["broken"]; b.TaskCount != 0 || strings.Contains(b.Error, "/srv") {
		t.Errorf("broken %+v", b)
	}
	if len(l.Packs) == 0 {
		t.Error("no packs resolved")
	}
}

// TestHostedRemoteMCP: /mcp takes an org API token, replays each tool call
// as that token through the hosted server, and refuses everyone else.
func TestHostedRemoteMCP(t *testing.T) {
	f := newHostedFixture(t)
	rpc := func(as string, hdr map[string]string, body any) resp {
		h := map[string]string{"Accept": "application/json, text/event-stream"}
		if as != "" {
			h["Authorization"] = "Bearer test-" + as
		}
		for k, v := range hdr {
			h[k] = v
		}
		return f.do("POST", handlers.MCPPath, "", h, body)
	}
	init := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}}
	if r := rpc("", nil, init); r.status != 401 {
		t.Fatalf("anonymous: %d %s", r.status, r.body)
	}
	if r := rpc("runA", nil, init); r.status != 403 {
		t.Fatalf("runner token: %d %s", r.status, r.body)
	}
	if r := rpc("tokA", map[string]string{"Origin": "https://evil.example"}, init); r.status != 403 {
		t.Fatalf("foreign origin: %d", r.status)
	}
	r := rpc("tokA", nil, init)
	if r.status != 200 || !strings.Contains(r.body, `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize: %d %s", r.status, r.body)
	}
	if r := rpc("tokA", nil, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); r.status != 202 {
		t.Fatalf("notification: %d %s", r.status, r.body)
	}
	if r := rpc("tokA", nil, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}); r.status != 200 || !strings.Contains(r.body, `"list_agents"`) {
		t.Fatalf("tools/list: %d %s", r.status, trim(r.body))
	}
	// The tool call reaches the org API as tokA: acme's agents, not an error.
	call := rpc("tokA", nil, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_agents"}})
	var out struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(call.body), &out); err != nil || call.status != 200 || out.Result.IsError || len(out.Result.Content) == 0 {
		t.Fatalf("tools/call list_agents: %d %v %s", call.status, err, trim(call.body))
	}
	if !strings.Contains(out.Result.Content[0].Text, `"agents"`) {
		t.Fatalf("list_agents answer: %s", trim(out.Result.Content[0].Text))
	}
	// A batch answers each request.
	batch := []any{map[string]any{"jsonrpc": "2.0", "id": 4, "method": "ping"}, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "ping"}}
	if r := rpc("tokA", nil, batch); r.status != 200 || strings.Count(r.body, `"jsonrpc"`) != 2 {
		t.Fatalf("batch: %d %s", r.status, r.body)
	}
	for _, m := range []string{"GET", "DELETE"} {
		if r := f.do(m, handlers.MCPPath, "tokA", nil, nil); r.status != 405 {
			t.Errorf("%s /mcp: %d", m, r.status)
		}
	}
}
