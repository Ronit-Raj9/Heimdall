package handlers

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/maximhq/bifrost/bench/mcptest/mcptestfake"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func newMCPTestClient(t *testing.T, hosted bool, index ReadIndex) (*authoringClient, *mcptestfake.Server, string) {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	fake := mcptestfake.New()
	fake.RequireToken = true
	up := httptest.NewServer(fake)
	t.Cleanup(up.Close)
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Hosted: hosted, Projects: ps, Suites: diskSuites{root: filepath.Join(root, "benchmarks")}, Index: index},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
	}
	h := &mcpTestArea{env: env, getenv: func(k string) string {
		if k == "SHOP_MCP_TOKEN" {
			return mcptestfake.Token
		}
		return ""
	}}
	r.POST(MCPServerInspectPath, env.W(h.inspect))
	r.POST(MCPServerBenchmarksPath, env.W(h.draft))
	r.GET(MCPServerReportPath, env.R(h.report))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}, fake, up.URL + "/mcp"
}

func TestMCPServerInspectRoute(t *testing.T) {
	c, fake, url := newMCPTestClient(t, false, nil)
	body, _ := json.Marshal(map[string]any{"url": url, "secret": "SHOP_MCP_TOKEN", "auth_scheme": "Bearer", "call_read_only": true, "repeats": 2})
	st, out := c.do(t, "POST", "/mcp-servers/inspect", body, nil)
	if st != 200 {
		t.Fatalf("inspect = %d %s", st, out)
	}
	var rep mcptest.Report
	if err := json.Unmarshal(out, &rep); err != nil || !rep.Reached || len(rep.Tools) != 3 || rep.Scores.Overall == 0 {
		t.Fatalf("report = %s", out)
	}
	if strings.Contains(string(out), mcptestfake.Token) {
		t.Fatal("the credential reached the answer")
	}
	if fake.CallsOf("delete_order") != 0 {
		t.Fatal("destructive tool called")
	}
	for _, bad := range []map[string]any{
		{"url": url, "secret": "MISSING_TOKEN"},
		{"url": "ftp://x"},
		{"url": url, "unknown": 1},
	} {
		b, _ := json.Marshal(bad)
		if st, out := c.do(t, "POST", "/mcp-servers/inspect", b, nil); st != 400 {
			t.Errorf("%v = %d %s, want 400", bad, st, out)
		}
	}
}

func TestMCPServerInspectHostedRefusesPrivate(t *testing.T) {
	c, _, url := newMCPTestClient(t, true, nil)
	body, _ := json.Marshal(map[string]any{"url": url})
	if st, out := c.do(t, "POST", "/mcp-servers/inspect", body, nil); st != 400 || !strings.Contains(string(out), "not allowed") {
		t.Fatalf("hosted private url = %d %s", st, out)
	}
}

func TestMCPServerDraftAndReport(t *testing.T) {
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e1": {}}}
	c, _, url := newMCPTestClient(t, false, idx)
	body, _ := json.Marshal(map[string]any{"id": "shop-mcp", "server_id": "shop", "url": url, "secret": "SHOP_MCP_TOKEN", "auth_scheme": "Bearer", "call_read_only": true})
	st, out := c.do(t, "POST", "/mcp-servers/benchmarks", body, nil)
	if st != 201 {
		t.Fatalf("draft = %d %s", st, out)
	}
	var res MCPServerDraftResponse
	if err := json.Unmarshal(out, &res); err != nil || len(res.Tasks) != 3 || len(res.ReferenceAgents) == 0 {
		t.Fatalf("draft = %s", out)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "shop-mcp", "suite.yaml")); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.do(t, "POST", "/mcp-servers/benchmarks", body, nil); st != 409 {
		t.Fatalf("second draft = %d, want 409", st)
	}

	ledger := `{"server":"shop","tool":"search_orders","protocol":"external","status":200,"arguments":"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"search_orders\",\"arguments\":{\"query\":\"A-1001\"}}}"}` + "\n"
	idx.runs = []RunView{
		{Slot: schemas.Slot{ExpID: "e1", Suite: "shop-mcp", Task: "tool-search-orders", Arm: "codex", Counted: true}, Run: schemas.Run{RunID: "r1", Outcome: schemas.OutcomePass}},
		{Slot: schemas.Slot{ExpID: "e1", Suite: "other", Task: "x", Arm: "codex", Counted: true}, Run: schemas.Run{RunID: "r2"}},
	}
	idx.files = map[string]map[string][]byte{"r1": {"tools.jsonl": []byte(ledger)}}
	st, out = c.do(t, "GET", "/mcp-servers/benchmarks/shop-mcp/report?experiment=e1", nil, nil)
	if st != 200 {
		t.Fatalf("report = %d %s", st, out)
	}
	var rep MCPServerReport
	if err := json.Unmarshal(out, &rep); err != nil || rep.Runs != 1 || rep.Calls != 1 || rep.Tools[0].Tool != "search_orders" || rep.Tools[0].Succeeded != 1 || rep.Tools[0].Passed != 1 {
		t.Fatalf("report = %s", out)
	}
	if st, _ := c.do(t, "GET", "/mcp-servers/benchmarks/nope/report", nil, nil); st != 404 {
		t.Fatalf("unknown benchmark = %d", st)
	}
}
