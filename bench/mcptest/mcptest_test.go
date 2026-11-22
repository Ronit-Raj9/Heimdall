package mcptest_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/maximhq/bifrost/bench/mcptest/mcptestfake"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func fakeTarget(t *testing.T, sse bool) (*mcptestfake.Server, mcptest.Target) {
	t.Helper()
	fs := mcptestfake.New()
	fs.RequireToken, fs.SSE = true, sse
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	return fs, mcptest.Target{URL: srv.URL + "/mcp", Headers: map[string]string{"Authorization": "Bearer " + mcptestfake.Token}}
}

func inspect(t *testing.T, sse bool) (*mcptestfake.Server, *mcptest.Report) {
	t.Helper()
	fs, tgt := fakeTarget(t, sse)
	rep := mcptest.Inspect(context.Background(), tgt, nil, mcptest.Options{Repeats: 3, CallReadOnly: true})
	if !rep.Reached {
		t.Fatalf("not reached: %s", rep.Error)
	}
	return fs, rep
}

func TestInspectMeasuresServer(t *testing.T) {
	for _, sse := range []bool{false, true} {
		fs, rep := inspect(t, sse)
		if rep.Hello.Name != "fake-shop" || !rep.Hello.Session || !rep.Hello.HasTools {
			t.Fatalf("hello = %+v", rep.Hello)
		}
		if len(rep.Tools) != 3 {
			t.Fatalf("tools = %d", len(rep.Tools))
		}
		if rep.Reliability.Rate != 1 || !rep.Reliability.StableListing || !rep.Reliability.PingSupported {
			t.Fatalf("reliability = %+v", rep.Reliability)
		}
		for _, c := range rep.Recovery {
			if !c.Passed || c.Skipped {
				t.Errorf("sse=%v recovery %s: passed %v skipped %v: %s", sse, c.ID, c.Passed, c.Skipped, c.Detail)
			}
		}
		if len(rep.Recovery) != 7 {
			t.Fatalf("recovery checks = %d", len(rep.Recovery))
		}
		if fs.CallsOf("delete_order") != 0 || fs.CallsOf("do") != 0 {
			t.Fatal("a tool not marked read-only and not allowed was called")
		}
		var ok []mcptest.ToolCallResult
		for _, c := range rep.Calls {
			if c.OK {
				ok = append(ok, c)
			}
		}
		if len(ok) != 3 || ok[0].Tool != "search_orders" || !ok[0].Placeholder || ok[0].Args["query"] != "test" || !strings.Contains(ok[0].Excerpt, "A-1001") {
			t.Fatalf("calls = %+v", rep.Calls)
		}
		if rep.Latency.Samples == 0 || rep.Latency.ByMethod["tools/list"].Samples == 0 {
			t.Fatalf("latency = %+v", rep.Latency)
		}
		if rep.Scores.Reliability != 100 || rep.Scores.ErrorRecovery != 100 || rep.Scores.Overall == 0 {
			t.Fatalf("scores = %+v", rep.Scores)
		}
	}
}

func TestQualityFindsWeakTools(t *testing.T) {
	_, rep := inspect(t, false)
	by := map[string]mcptest.ToolQuality{}
	for _, q := range rep.Quality.Tools {
		by[q.Tool] = q
	}
	codes := func(q mcptest.ToolQuality) []string {
		var out []string
		for _, is := range q.Issues {
			out = append(out, is.Code)
		}
		return out
	}
	good, bad := by["search_orders"], by["do"]
	if good.DescriptionScore != 100 || good.SchemaScore != 100 || len(good.Issues) != 0 || !good.ReadOnly {
		t.Fatalf("search_orders = %+v", good)
	}
	for _, c := range []string{"name_vague", "description_missing", "param_no_description", "param_no_type", "param_ambiguous"} {
		if !slices.Contains(codes(bad), c) {
			t.Errorf("do lacks %s: %v", c, codes(bad))
		}
	}
	if bad.DescriptionScore >= 60 || bad.SchemaScore >= 60 {
		t.Fatalf("do scored too well: %+v", bad)
	}
	if !by["delete_order"].Destructive {
		t.Fatal("destructive hint not read")
	}
}

func TestInspectUnreachableAndUnauthorized(t *testing.T) {
	rep := mcptest.Inspect(context.Background(), mcptest.Target{URL: "http://127.0.0.1:1/mcp"}, nil, mcptest.Options{})
	if rep.Reached || rep.Error == "" || rep.Scores.Overall != 0 {
		t.Fatalf("unreachable report = %+v", rep)
	}
	_, tgt := fakeTarget(t, false)
	tgt.Headers = nil
	rep = mcptest.Inspect(context.Background(), tgt, nil, mcptest.Options{})
	if rep.Reached || !strings.Contains(rep.Error, "HTTP 401") {
		t.Fatalf("unauthorized report error = %q", rep.Error)
	}
}

func TestPlaceholderArgs(t *testing.T) {
	got := mcptest.PlaceholderArgs(json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"enum":["x","y"]},"c":{"type":"string","default":"d"},"o":{"type":"object","properties":{"z":{"type":"boolean"}},"required":["z"]}},"required":["a","b","c","o"]}`))
	b, _ := json.Marshal(got)
	if string(b) != `{"a":1,"b":"x","c":"d","o":{"z":false}}` {
		t.Fatalf("args = %s", b)
	}
}

func draft(t *testing.T) ([]scaffold.File, mcptest.DraftResult, string) {
	t.Helper()
	_, rep := inspect(t, false)
	files, res, err := mcptest.Draft(mcptest.DraftOptions{SuiteID: "shop-mcp", ServerID: "shop", URL: rep.URL, AuthEnv: "SHOP_MCP_TOKEN", AuthScheme: "Bearer", Report: rep})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "shop-mcp")
	if err := scaffold.WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	return files, res, dir
}

func TestDraftWritesLoadableBenchmark(t *testing.T) {
	_, res, dir := draft(t)
	if len(res.Tasks) != 3 || res.Tasks[0].ID != "tool-search-orders" || !res.Tasks[0].Reference {
		t.Fatalf("tasks = %+v", res.Tasks)
	}
	if !slices.Equal(res.NoReference, []string{"tool-do", "tool-delete-order"}) {
		t.Fatalf("no reference = %v", res.NoReference)
	}
	s, err := suite.Builtin(filepath.Dir(dir))[0].Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("drafted suite does not load: %v", err)
	}
	if len(s.External) != 1 || !s.External[0].MCP || s.External[0].Mode != schemas.ExternalLive || s.External[0].AuthEnv != "SHOP_MCP_TOKEN" {
		t.Fatalf("external = %+v", s.External)
	}
	if len(s.Tasks) != 3 {
		t.Fatalf("loaded tasks = %d", len(s.Tasks))
	}
	tk := s.Tasks[0]
	if tk.Provenance.InstructionReviewed == nil || *tk.Provenance.InstructionReviewed {
		t.Fatal("a drafted task must be pending review")
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == "error" {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	ins, _ := os.ReadFile(filepath.Join(dir, "tasks/tool-search-orders/instruction.md"))
	if !strings.Contains(string(ins), "Search the shop's orders") || !strings.Contains(string(ins), `query: "test"`) || strings.Contains(string(ins), "verif") {
		t.Fatalf("instruction = %s", ins)
	}
	if _, err := os.Stat(filepath.Join(dir, mcptest.ServerFile)); err != nil {
		t.Fatal(err)
	}
}

func TestDraftRefusesBadOptions(t *testing.T) {
	_, rep := inspect(t, false)
	cases := []mcptest.DraftOptions{
		{SuiteID: "Bad Id", ServerID: "shop", URL: rep.URL, Report: rep},
		{SuiteID: "ok", ServerID: "Shop_1", URL: rep.URL, Report: rep},
		{SuiteID: "ok", ServerID: "shop", URL: "https://u:p@x.example/mcp", Report: rep},
		{SuiteID: "ok", ServerID: "shop", URL: rep.URL, AuthEnv: "lower", Report: rep},
		{SuiteID: "ok", ServerID: "shop", URL: rep.URL},
		{SuiteID: "ok", ServerID: "shop", URL: rep.URL, Report: rep, Tools: []string{"missing"}},
	}
	for i, o := range cases {
		if _, _, err := mcptest.Draft(o); err == nil {
			t.Errorf("case %d drafted", i)
		}
	}
}

// TestVerifierGradesLedger runs the drafted verify.py over ledgers: a valid
// successful call passes; no call, a bad-argument call and an error result fail.
func TestVerifierGradesLedger(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	_, _, dir := draft(t)
	tests := filepath.Join(dir, "tasks/tool-search-orders/verifier/tests")
	row := func(args string, isErr bool) string {
		b, _ := json.Marshal(map[string]any{"server": "shop", "tool": "search_orders", "protocol": "external", "status": 200, "is_error": isErr,
			"arguments": `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_orders","arguments":` + args + `}}`})
		return string(b)
	}
	cases := []struct {
		name   string
		ledger []string
		reward int
	}{
		{"valid", []string{row(`{"query":"A-1001"}`, false)}, 1},
		{"none", nil, 0},
		{"wrong type", []string{row(`{"query":5}`, false)}, 0},
		{"missing", []string{row(`{}`, false)}, 0},
		{"error result", []string{row(`{"query":"A-1001"}`, true)}, 0},
	}
	for _, c := range cases {
		tmp := t.TempDir()
		ledger := filepath.Join(tmp, "tools.jsonl")
		if err := os.WriteFile(ledger, []byte(strings.Join(c.ledger, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python3", "-I", filepath.Join(tests, "verify.py"))
		cmd.Env = append(os.Environ(), "GRADER_TOOLS="+ledger, "GRADER_OUT="+tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", c.name, err, out)
		}
		var got struct {
			Reward int `json:"reward"`
		}
		b, _ := os.ReadFile(filepath.Join(tmp, "reward.json"))
		if json.Unmarshal(b, &got) != nil || got.Reward != c.reward {
			t.Errorf("%s: reward.json = %s, want reward %d", c.name, b, c.reward)
		}
	}
}

func TestServerReport(t *testing.T) {
	_, rep := inspect(t, false)
	call := func(tool, args string, isErr bool) schemas.ToolCall {
		return schemas.ToolCall{Server: "shop", Tool: tool, Protocol: "external", Status: 200, IsError: isErr, LatencyMS: 40,
			Arguments: `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`}
	}
	list := schemas.ToolCall{Server: "shop", Tool: "tools/list", Arguments: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`}
	runs := []mcptest.RunLedger{
		{RunID: "r1", Agent: "codex", Task: "tool-search-orders", Passed: true, Rows: []schemas.ToolCall{list, call("search_orders", `{"query":"x"}`, false)}},
		{RunID: "r2", Agent: "claude-code", Task: "tool-search-orders", Rows: []schemas.ToolCall{call("search_orders", `{"q":"x"}`, true), call("ghost", `{}`, true)}},
		{RunID: "r3", Agent: "claude-code", Task: "tool-do", Rows: []schemas.ToolCall{call("search_orders", `{"query":"y"}`, false)}},
	}
	sr := mcptest.BuildServerReport("shop", rep.Tools, map[string]string{"tool-search-orders": "search_orders", "tool-do": "do"}, runs)
	if sr.Runs != 3 || sr.Calls != 4 || sr.Errors != 2 || !slices.Equal(sr.Agents, []string{"claude-code", "codex"}) || !slices.Equal(sr.Unlisted, []string{"ghost"}) {
		t.Fatalf("report = %+v", sr)
	}
	so, do := sr.Tools[0], sr.Tools[1]
	if so.Tool != "search_orders" || so.Runs != 2 || so.Picked != 2 || so.Calls != 3 || so.Valid != 2 || so.Succeeded != 2 || so.Passed != 1 || len(so.ByAgent) != 2 {
		t.Fatalf("search_orders = %+v", so)
	}
	if do.Runs != 1 || do.Picked != 0 || do.PickRate != 0 {
		t.Fatalf("do = %+v", do)
	}
}

func TestValidArgs(t *testing.T) {
	s := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"},"k":{"enum":["a","b"]},"l":{"type":"array","items":{"type":"string"}}},"required":["n"]}`)
	good := map[string]any{"n": float64(2), "k": "a", "l": []any{"x"}}
	if !mcptest.ValidArgs(s, good) {
		t.Fatal("good args refused")
	}
	for _, bad := range []map[string]any{{}, {"n": "2"}, {"n": 1.5}, {"n": float64(1), "k": "c"}, {"n": float64(1), "l": []any{1.0}}} {
		if mcptest.ValidArgs(s, bad) {
			t.Errorf("bad args %v accepted", bad)
		}
	}
}
