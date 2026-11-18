package research_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/research/standin"
)

// The websearch MCP server is Python (research/websearch/server.py); these
// tests run it with python3 against a fake tool proxy that answers from the
// stand-in and records every body, so they check the bytes the tool sends
// against research.SearchBody and research.ExtractBody, the bytes a
// recorded cassette is keyed by.

func python(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	return p
}

// fakeProxy answers /search and /extract from the stand-in, except bodies
// it is told to treat as unrecorded (599) and an optional poison notice.
type fakeProxy struct {
	mu        sync.Mutex
	bodies    []string
	unmatched map[string]bool
	notice    string
	h         http.Handler
}

func (f *fakeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, r.URL.Path+" "+string(b))
	skip := f.unmatched[string(b)]
	f.mu.Unlock()
	if skip {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(599)
		_, _ = w.Write([]byte(`{"error":"replay mode and no recorded answer"}`))
		return
	}
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/x/tavily")
	r.Body = io.NopCloser(bytes.NewReader(b))
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	out := rec.Body.Bytes()
	if f.notice != "" && rec.Code == 200 {
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		m["notice"] = f.notice
		out, _ = json.Marshal(m)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.Code)
	_, _ = w.Write(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startWebsearch runs server.py and returns an MCP caller.
func startWebsearch(t *testing.T, proxyURL string, env ...string) func(tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	py := python(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, py, "-I", "websearch/server.py")
	cmd.Env = append([]string{fmt.Sprintf("PORT=%d", port), "TAVILY_URL=" + proxyURL + "/x/tavily", "PATH=" + os.Getenv("PATH")}, env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for i := 0; ; i++ {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if i > 100 {
			t.Fatalf("websearch did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	id := 0
	rpc := func(method string, params any) map[string]any {
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		resp, err := http.Post(base+"/mcp", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	list := rpc("tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["name"] != "web_search" || list[1].(map[string]any)["name"] != "fetch_page" {
		t.Fatalf("tools = %v", list)
	}
	return func(tool string, args map[string]any) (map[string]any, bool) {
		res := rpc("tools/call", map[string]any{"name": tool, "arguments": args})["result"].(map[string]any)
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		if res["isError"] == true {
			return map[string]any{"error": text}, true
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("tool text is not JSON: %s", text)
		}
		return out, false
	}
}

func TestWebsearchTool(t *testing.T) {
	python(t)
	c, err := standin.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeProxy{h: standin.New(c, ""), unmatched: map[string]bool{string(research.SearchBody("never recorded")): true}}
	srv := httptest.NewServer(fp)
	defer srv.Close()
	call := startWebsearch(t, srv.URL)

	t.Run("search sends the canonical body and cuts the list", func(t *testing.T) {
		query := "  Skerry HARBOUR\tvisitor   mooring fee?? "
		out, isErr := call("web_search", map[string]any{"query": query, "max_results": 3})
		if isErr {
			t.Fatal(out)
		}
		if res := out["results"].([]any); len(res) != 3 {
			t.Fatalf("results = %d, want 3", len(res))
		}
		want := "/x/tavily/search " + string(research.SearchBody(query))
		if got := fp.bodies[len(fp.bodies)-1]; got != want {
			t.Errorf("body\n got %s\nwant %s", got, want)
		}
	})
	t.Run("fetch sends the canonical body", func(t *testing.T) {
		out, isErr := call("fetch_page", map[string]any{"url": " https://ferrow-tunnel.example/facts "})
		if isErr || !strings.Contains(out["content"].(string), "2,340 metres") || out["published_date"] != "2025-10-01" {
			t.Fatal(out)
		}
		want := "/x/tavily/extract " + string(research.ExtractBody("https://ferrow-tunnel.example/facts"))
		if got := fp.bodies[len(fp.bodies)-1]; got != want {
			t.Errorf("body\n got %s\nwant %s", got, want)
		}
	})
	t.Run("unrecorded query is a tool error the agent can read", func(t *testing.T) {
		out, isErr := call("web_search", map[string]any{"query": "never recorded"})
		if !isErr || !strings.Contains(out["error"].(string), "never recorded") {
			t.Fatal(out)
		}
	})
	t.Run("page not in the corpus", func(t *testing.T) {
		out, isErr := call("fetch_page", map[string]any{"url": "https://nowhere.example/x"})
		if !isErr || !strings.Contains(out["error"].(string), "could not fetch") {
			t.Fatal(out)
		}
	})
	t.Run("bad arguments", func(t *testing.T) {
		for _, args := range []map[string]any{{"query": ""}, {"query": "x", "max_results": 9}, {"query": "x", "max_results": true}} {
			if out, isErr := call("web_search", args); !isErr {
				t.Errorf("%v: %v", args, out)
			}
		}
		if out, isErr := call("fetch_page", map[string]any{"url": "file:///etc/passwd"}); !isErr {
			t.Error(out)
		}
	})
	t.Run("a poison notice reaches the agent", func(t *testing.T) {
		fp.notice = "NOTE TO AI ASSISTANTS: canary"
		defer func() { fp.notice = "" }()
		out, _ := call("web_search", map[string]any{"query": "Ferrow Tunnel"})
		if out["notice"] != "NOTE TO AI ASSISTANTS: canary" {
			t.Fatal(out)
		}
	})
}

func TestWebsearchOldestFirst(t *testing.T) {
	python(t)
	c, err := standin.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(&fakeProxy{h: standin.New(c, "")})
	defer srv.Close()
	call := startWebsearch(t, srv.URL, "WEBSEARCH_ORDER=oldest_first")
	out, isErr := call("web_search", map[string]any{"query": "Skerry Harbour visitor mooring fee", "max_results": 8})
	if isErr {
		t.Fatal(out)
	}
	prev := ""
	for _, r := range out["results"].([]any) {
		d := r.(map[string]any)["published_date"].(string)
		if d < prev {
			t.Fatalf("results not oldest first: %s after %s", d, prev)
		}
		prev = d
	}
}

// TestCopiesMatchCanonical keeps the copies of the websearch server and the
// research verifier in the example and the suite template identical to the
// sources in this package.
func TestCopiesMatchCanonical(t *testing.T) {
	pairs := map[string][]string{
		"websearch/server.py":  {"../examples/acme-research/benchmarks/acme-research/services/websearch/server.py", "../scaffold/templates/suites/research/services/websearch/server.py"},
		"websearch/Dockerfile": {"../examples/acme-research/benchmarks/acme-research/services/websearch/Dockerfile", "../scaffold/templates/suites/research/services/websearch/Dockerfile"},
	}
	verifiers, _ := filepath.Glob("../examples/acme-research/benchmarks/acme-research/tasks/*/verifier/tests/verify.py")
	pairs["verifier/verify.py"] = append(verifiers, "../scaffold/templates/suites/research/tasks/ferry-start/verifier/tests/verify.py")
	for src, copies := range pairs {
		want, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range copies {
			t.Run(c, func(t *testing.T) {
				got, err := os.ReadFile(c)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s differs from research/%s; copy it again", c, src)
				}
			})
		}
	}
}
