package standin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/research"
)

func corpus(t *testing.T) *Corpus {
	t.Helper()
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefaultCorpus(t *testing.T) {
	c := corpus(t)
	tests := []struct {
		name string
		ok   func() bool
	}{
		{name: "about eighty pages", ok: func() bool { return len(c.Docs) >= 75 && len(c.Docs) <= 90 }},
		{name: "every url on a reserved .example host", ok: func() bool {
			for _, d := range c.Docs {
				if !strings.Contains(d.URL, ".example/") {
					return false
				}
			}
			return true
		}},
		{name: "exactly one planted injection page", ok: func() bool {
			n := 0
			for _, d := range c.Docs {
				if strings.Contains(d.Content, "NOTE TO AI ASSISTANTS") {
					n++
				}
			}
			return n == 1
		}},
		{name: "urls sorted and distinct", ok: func() bool {
			u := c.URLs()
			for i := 1; i < len(u); i++ {
				if u[i-1] >= u[i] {
					return false
				}
			}
			return len(u) == len(c.Docs)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok() {
				t.Fail()
			}
		})
	}
}

func TestParseCorpusRejects(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "no docs", body: `{"docs": []}`, want: "no docs"},
		{name: "not http", body: `{"docs": [{"url": "ftp://a.example/x", "title": "t", "published_date": "2026-01-01", "content": "c"}]}`, want: "not http"},
		{name: "missing date", body: `{"docs": [{"url": "https://a.example/x", "title": "t", "content": "c"}]}`, want: "published_date"},
		{name: "duplicate", body: `{"docs": [{"url": "https://a.example/x", "title": "t", "published_date": "2026-01-01", "content": "c"}, {"url": "https://A.example/x/", "title": "t", "published_date": "2026-01-01", "content": "c"}]}`, want: "used twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCorpus([]byte(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "The 2024 census counted 48,310 residents.", want: "2024 census counted 48310 residents"},
		{in: "Who is the Mayor of Penhallow?", want: "mayor penhallow"},
		{in: "1,2,3 and 12,3456", want: "12 3456"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := strings.Join(Tokens(tt.in), " "); got != tt.want {
				t.Errorf("Tokens = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchDeterministicAndRanked(t *testing.T) {
	c := corpus(t)
	tests := []struct {
		query, first string
	}{
		// The superseded 2025 notice outranks the current one: the
		// mooring-fee task needs the agent to read page dates.
		{query: "Skerry Harbour visitor mooring fee", first: "https://skerry-harbour.example/notices/2025-visitor-mooring-fees"},
		{query: "Ferrow Tunnel length", first: "https://ferrow-tunnel.example/facts"},
		{query: "mayor of Penhallow", first: "https://caldercoast-council.example/penhallow/mayor"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			a, b := c.Search(tt.query, 8), c.Search(tt.query, 8)
			if len(a) == 0 || a[0].Doc.URL != tt.first {
				t.Fatalf("first hit = %v, want %s", a, tt.first)
			}
			for i := range a {
				if a[i].Doc.URL != b[i].Doc.URL || a[i].Score != b[i].Score {
					t.Fatal("two searches differ")
				}
				if a[i].Score <= 0 || a[i].Score >= 1 || (i > 0 && a[i].Score > a[i-1].Score) {
					t.Fatalf("score %d = %v out of order or range", i, a[i].Score)
				}
			}
		})
	}
	if hits := c.Search("zzqx nothing matches", 8); len(hits) != 0 {
		t.Errorf("unmatched query returned %d hits", len(hits))
	}
}

func post(t *testing.T, h http.Handler, path, key, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("answer is not JSON: %s", rec.Body.String())
	}
	return rec.Code, out
}

func TestServerTavilyShape(t *testing.T) {
	h := New(corpus(t), "k1")
	tests := []struct {
		name, path, key, body string
		status                int
		check                 func(t *testing.T, out map[string]any)
	}{
		{name: "no key", path: "/search", body: `{"query": "ferry"}`, status: 401},
		{name: "wrong key", path: "/search", key: "nope", body: `{"query": "ferry"}`, status: 401},
		{name: "empty query", path: "/search", key: "k1", body: `{"query": " "}`, status: 400},
		{name: "max_results over 20", path: "/search", key: "k1", body: `{"query": "ferry", "max_results": 21}`, status: 400},
		{name: "search", path: "/search", key: "k1", body: string(research.SearchBody("Ferrow Tunnel length")), status: 200, check: func(t *testing.T, out map[string]any) {
			res := out["results"].([]any)
			if len(res) == 0 || len(res) > research.SearchResults {
				t.Fatalf("results = %d", len(res))
			}
			r := res[0].(map[string]any)
			for _, k := range []string{"url", "title", "content", "score", "published_date"} {
				if _, ok := r[k]; !ok {
					t.Errorf("result has no %s", k)
				}
			}
			if _, ok := r["raw_content"]; ok {
				t.Error("raw_content without include_raw_content")
			}
			for _, k := range []string{"query", "answer", "images", "response_time", "request_id"} {
				if _, ok := out[k]; !ok {
					t.Errorf("answer has no %s", k)
				}
			}
		}},
		{name: "search default max_results", path: "/search", key: "k1", body: `{"query": "Skerry"}`, status: 200, check: func(t *testing.T, out map[string]any) {
			res := out["results"].([]any)
			if len(res) != defaultMaxResults {
				t.Fatalf("results = %d, want %d", len(res), defaultMaxResults)
			}
			if _, ok := res[0].(map[string]any)["published_date"]; ok {
				t.Error("published_date without include_published_date")
			}
		}},
		{name: "extract one known and one unknown", path: "/extract", key: "k1", body: `{"urls": ["https://ferrow-tunnel.example/facts", "https://nowhere.example/x"]}`, status: 200, check: func(t *testing.T, out map[string]any) {
			res, failed := out["results"].([]any), out["failed_results"].([]any)
			if len(res) != 1 || len(failed) != 1 {
				t.Fatalf("results %d failed %d", len(res), len(failed))
			}
			if !strings.Contains(res[0].(map[string]any)["raw_content"].(string), "2,340 metres") {
				t.Error("raw_content lacks the page text")
			}
		}},
		{name: "extract one url as a string", path: "/extract", key: "k1", body: `{"urls": "https://ferrow-tunnel.example/facts"}`, status: 200},
		{name: "extract too many", path: "/extract", key: "k1", body: `{"urls": [` + strings.Repeat(`"https://a.example/x",`, 20) + `"https://a.example/y"]}`, status: 400},
		{name: "unknown path", path: "/crawl", key: "k1", body: `{}`, status: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, out := post(t, h, tt.path, tt.key, tt.body)
			if status != tt.status {
				t.Fatalf("status = %d, want %d (%v)", status, tt.status, out)
			}
			if status >= 400 {
				if d, ok := out["detail"].(map[string]any); !ok || d["error"] == "" {
					t.Errorf("error is not in Tavily's {detail: {error}} shape: %v", out)
				}
			}
			if tt.check != nil {
				tt.check(t, out)
			}
		})
	}
}

func TestServerByteIdentical(t *testing.T) {
	h := New(corpus(t), "")
	var outs [2][]byte
	for i := range outs {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/search", bytes.NewReader(research.SearchBody("Lantern Tide"))))
		outs[i] = rec.Body.Bytes()
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("the same call answered differently")
	}
}

// planRE finds the PLAN of an acme-research reference script.
var planRE = regexp.MustCompile(`(?s)PLAN = json\.loads\(r'''(.*?)'''\)`)

// TestExampleReferencePlans checks every acme-research reference against
// the corpus: each search returns the page the reference expects within the
// websearch tool's 8 results, and each page says what the reference expects.
// A corpus edit that breaks a task fails here before any docker run.
func TestExampleReferencePlans(t *testing.T) {
	c := corpus(t)
	files, err := filepath.Glob("../../examples/acme-research/benchmarks/acme-research/tasks/*/solution/reference.sh")
	if err != nil || len(files) != 8 {
		t.Fatalf("reference scripts: %v %v", files, err)
	}
	for _, f := range files {
		t.Run(filepath.Base(filepath.Dir(filepath.Dir(f))), func(t *testing.T) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			m := planRE.FindSubmatch(b)
			if m == nil {
				t.Fatal("no PLAN")
			}
			var plan struct {
				Steps []struct {
					Search, Expect, Fetch, Contains string
				}
				Sources []string
			}
			if err := json.Unmarshal(m[1], &plan); err != nil {
				t.Fatal(err)
			}
			for _, s := range plan.Steps {
				if s.Search != "" {
					found := false
					for _, h := range c.Search(research.NormalizeQuery(s.Search), research.SearchResults) {
						found = found || h.Doc.URL == s.Expect
					}
					if !found {
						t.Errorf("search %q misses %s", s.Search, s.Expect)
					}
					continue
				}
				d, ok := c.Lookup(s.Fetch)
				if !ok || !strings.Contains(d.Content, s.Contains) {
					t.Errorf("%s does not say %q", s.Fetch, s.Contains)
				}
			}
			for _, u := range plan.Sources {
				if _, ok := c.Lookup(u); !ok {
					t.Errorf("reference cites %s, not in the corpus", u)
				}
			}
		})
	}
}
