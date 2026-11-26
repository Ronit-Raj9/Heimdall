package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

func reportCardOf(t *testing.T, env OrgEnv) GatewayReportCard {
	t.Helper()
	h := gatewayTestsRouter(env, nil)
	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/report-card", "")
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var c GatewayReportCard
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func cardLine(t *testing.T, c GatewayReportCard, id string) GatewayCardLine {
	t.Helper()
	for _, l := range c.Lines {
		if l.Feature == id {
			return l
		}
	}
	t.Fatalf("no line %s in %+v", id, c.Lines)
	return GatewayCardLine{}
}

// fakeBifrost answers the management reads of the report card.
func fakeBifrost(t *testing.T, paths map[string]string) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		body, ok := paths[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestReportCardUnknownWhenGatewayDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	cfg := &lib.Config{Root: t.TempDir()}
	cfg.Gateway.URL = url
	c := reportCardOf(t, OrgEnv{Cfg: cfg, Mode: testMode})
	if !c.Gateway.Configured || c.Gateway.Reachable || c.Gateway.Error != "the gateway did not answer" {
		t.Fatalf("gateway: %+v", c.Gateway)
	}
	if strings.Contains(c.Gateway.Error, url) {
		t.Fatal("the error names the address")
	}
	for _, id := range []string{"complexity_router", "semantic_cache", "prompt_version", "mcp_tools"} {
		if l := cardLine(t, c, id); l.Enabled != "unknown" || l.EnabledSource != "the gateway did not answer" {
			t.Errorf("%s: %+v", id, l)
		}
	}
	want := []string{"complexity_router", "semantic_cache", "prompt_version", "skill_version", "mcp_tools", "providers", "budget_curve", "gateway_overhead", "new_model", "agent_gateway", "batch"}
	if len(c.Lines) != len(want) {
		t.Fatalf("%d lines", len(c.Lines))
	}
	for i, id := range want {
		if c.Lines[i].Feature != id {
			t.Fatalf("line %d is %s, want %s", i, c.Lines[i].Feature, id)
		}
	}
}

func TestReportCardEnabledFromPlugins(t *testing.T) {
	srv, _ := fakeBifrost(t, map[string]string{
		"/api/plugins/loaded":                     `{"plugins":["semantic_cache","governance","logging"]}`,
		"/api/version":                            `"v2.5.1"`,
		"/api/routing/complexity-analyzer-status": `{"state":"ready","llm":{"state":"ready"}}`,
		"/api/mcp/clients":                        `{"clients":[{"config":{"name":"files_r_abc123"}}],"total_count":1}`,
		"/api/agents":                             `{"agents":[{"name":"a"},{"name":"b"}],"count":2}`,
	})
	cfg := &lib.Config{Root: t.TempDir()}
	cfg.Gateway.URL = srv.URL
	c := reportCardOf(t, OrgEnv{Cfg: cfg, Mode: testMode})
	if !c.Gateway.Reachable || c.Gateway.Version != "v2.5.1" || len(c.Gateway.Plugins) != 3 {
		t.Fatalf("gateway: %+v", c.Gateway)
	}
	if c.Source.Mode != "run_gateway" {
		t.Fatalf("source: %+v", c.Source)
	}
	checks := map[string]string{"semantic_cache": "on", "prompt_version": "off", "complexity_router": "on", "mcp_tools": "off", "budget_curve": "on", "providers": "unknown", "agent_gateway": "on", "new_model": "off", "batch": "off"}
	for id, want := range checks {
		if l := cardLine(t, c, id); l.Enabled != want {
			t.Errorf("%s: %s, want %s (%s)", id, l.Enabled, want, l.EnabledSource)
		}
	}
	if l := cardLine(t, c, "agent_gateway"); l.Headline != "2 registered agents" {
		t.Errorf("agents headline %q", l.Headline)
	}
	if l := cardLine(t, c, "semantic_cache"); l.State != "not_measured" || l.Headline != "Not measured yet" {
		t.Errorf("unmeasured line: %+v", l)
	}
}

func TestReportCardLineFromNewestEffect(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range []gwfeature.Effect{
		{Feature: schemas.GatewayFeatureSemanticCache, ExpID: "e_old", State: gwfeature.StateNotEnoughData, Headline: "old", ComputedAt: old},
		{Feature: schemas.GatewayFeatureSemanticCache, ExpID: "e_new", State: gwfeature.StateMeasured, Result: "saved", Headline: "Saved 12% per task", ComputedAt: old.Add(time.Hour), Source: gwfeature.Source{Kind: "experiment", ExpID: "e_new", Runs: 20, Text: "comparison e_new: 20 runs"}},
	} {
		if err := gwfeature.Save(root, e); err != nil {
			t.Fatal(err)
		}
	}
	c := reportCardOf(t, OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode})
	l := cardLine(t, c, schemas.GatewayFeatureSemanticCache)
	if l.State != "measured" || l.Result != "saved" || l.Headline != "Saved 12% per task" || l.ExpID != "e_new" || l.Source == nil || l.Source.Runs != 20 || l.MeasuredAt == nil {
		t.Fatalf("%+v", l)
	}
	if l.Enabled != "unknown" {
		t.Fatalf("no gateway: enabled %s", l.Enabled)
	}
}

func TestReportCardNewModelLineFromWatchState(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".heimdall", "gateway", "model-watch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := `{"schedule":"nightly","baseline":{"anthropic":["claude-a"]},"seen":{
		"anthropic/claude-x":{"provider":"anthropic","model":"claude-x","first_seen":"2026-12-03T08:00:00Z","state":"started","evaluation_id":"ev_1","claimed_at":"2026-12-03T08:00:00Z"},
		"anthropic/claude-w":{"provider":"anthropic","model":"claude-w","first_seen":"2026-11-20T08:00:00Z","state":"failed","claimed_at":"2026-11-20T08:00:00Z"}},
		"checked_at":"2026-12-03T08:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "nightly.json"), []byte(st), 0o600); err != nil {
		t.Fatal(err)
	}
	c := reportCardOf(t, OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode})
	l := cardLine(t, c, "new_model")
	if l.Enabled != "on" || l.Headline != "claude-x found on 3 Dec: evaluation started" || l.Start != "" {
		t.Fatalf("%+v", l)
	}
}

func TestReportCardBatchLineAlwaysNotMeasured(t *testing.T) {
	root := t.TempDir()
	if err := gwfeature.Save(root, gwfeature.Effect{Feature: "batch", ExpID: "e_b", State: gwfeature.StateMeasured, Headline: "x", ComputedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	c := reportCardOf(t, OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode})
	l := cardLine(t, c, "batch")
	if l.Enabled != "off" || l.State != "not_measured" || l.Headline != "Heimdall does not use Bifrost's batch API yet" || l.ExpID != "" {
		t.Fatalf("%+v", l)
	}
}

func TestReportCardHostedNeverReadsOtherOrgs(t *testing.T) {
	srv, seen := fakeBifrost(t, map[string]string{
		"/api/plugins/loaded": `{"plugins":["prompts"]}`,
		"/api/version":        `"v2.5.1"`,
		"/api/mcp/clients":    `{"clients":[{"config":{"name":"other_org_tools"}}]}`,
		"/api/agents":         `{"agents":[{"name":"other"}]}`,
		"/api/skills":         `[{"id":"s"}]`,
	})
	cfg := &lib.Config{Root: t.TempDir()}
	cfg.Gateway.URL = srv.URL
	c := reportCardOf(t, OrgEnv{Cfg: cfg, Mode: testMode, Services: Services{Hosted: true, OrgID: "o1"}})
	for _, p := range *seen {
		if p == "/api/mcp/clients" || p == "/api/agents" || p == "/api/skills" {
			t.Fatalf("hosted card read %s of the shared gateway", p)
		}
	}
	if c.Source.Mode != "none" || c.Source.Connected {
		t.Fatalf("source: %+v", c.Source)
	}
	for _, id := range []string{"mcp_tools", "agent_gateway", "skill_version"} {
		if l := cardLine(t, c, id); l.Enabled != "unknown" {
			t.Errorf("%s: %+v", id, l)
		}
	}
	if l := cardLine(t, c, "prompt_version"); l.Enabled != "on" {
		t.Errorf("plugins of the run gateway are still read: %+v", l)
	}
}
