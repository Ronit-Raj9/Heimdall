package recording

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/research/standin"
	"github.com/maximhq/bifrost/bench/toolproxy"
)

const exampleSuite = "../../examples/acme-research/benchmarks/acme-research"

func TestRecordFromStandin(t *testing.T) {
	q := &Queries{Search: []string{"Ferrow Tunnel length", "  ferrow tunnel LENGTH? ", "mayor of Penhallow"}, Extract: []string{"https://ferrow-tunnel.example/facts", "https://nowhere.example/x"}}
	var tapes [2][]byte
	for i := range tapes {
		out := filepath.Join(t.TempDir(), "c.jsonl")
		res, err := Record(context.Background(), q, Options{Out: out})
		if err != nil {
			t.Fatal(err)
		}
		if res.Searches != 2 || res.Extracts != 2 || res.Upstream != "stand-in" || len(res.Failed) != 0 {
			t.Fatalf("result = %+v", res)
		}
		if tapes[i], err = os.ReadFile(out); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(tapes[0], tapes[1]) {
		t.Fatal("two recordings of the same calls differ")
	}
	if bytes.Contains(tapes[0], []byte(standinKey)) {
		t.Fatal("cassette holds the key")
	}
	entries, err := toolproxy.ReadCassette(bytes.NewReader(tapes[0]))
	if err != nil || len(entries) != 4 {
		t.Fatalf("cassette: %d entries, %v", len(entries), err)
	}
}

func TestRecordRealUpstreamNeedsKey(t *testing.T) {
	q := &Queries{Search: []string{"x"}}
	_, err := Record(context.Background(), q, Options{Upstream: "https://api.tavily.com", Out: filepath.Join(t.TempDir(), "c.jsonl"), Getenv: func(string) string { return "" }})
	if err == nil || !strings.Contains(err.Error(), "TAVILY_API_KEY is empty") {
		t.Fatalf("err = %v", err)
	}
}

// TestRecordInjectsKey points the recorder at a stand-in that demands a key,
// as Tavily does: the calls pass only because the proxy injected it, and the
// key is in neither the cassette nor the result.
func TestRecordInjectsKey(t *testing.T) {
	c, err := standin.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const key = "tvly-test-secret"
	srv := httptest.NewServer(standin.New(c, key))
	defer srv.Close()
	tests := []struct {
		name   string
		getenv func(string) string
		failed bool
	}{
		{name: "key injected", getenv: func(k string) string {
			if k == "TAVILY_API_KEY" {
				return key
			}
			return ""
		}},
		{name: "wrong key is refused upstream", getenv: func(string) string { return "tvly-wrong" }, failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "c.jsonl")
			res, err := Record(context.Background(), &Queries{Search: []string{"Lantern Tide"}}, Options{Upstream: srv.URL, Out: out, Getenv: tt.getenv})
			if err != nil {
				t.Fatal(err)
			}
			if (len(res.Failed) > 0) != tt.failed {
				t.Fatalf("failed = %v", res.Failed)
			}
			b, _ := os.ReadFile(out)
			if bytes.Contains(b, []byte(key)) || strings.Contains(strings.Join(res.Failed, " "), key) {
				t.Fatal("key leaked")
			}
		})
	}
}

// TestCommittedCassetteIsCurrent re-records the acme-research cassette from
// its queries and the stand-in and compares it with the committed one, so a
// corpus, query or body change that was not re-recorded fails here.
func TestCommittedCassetteIsCurrent(t *testing.T) {
	tests := []struct {
		name, suite string
		tasks       int
	}{
		{name: "acme-research example", suite: exampleSuite, tasks: 8},
		{name: "research template", suite: "../../scaffold/templates/suites/research", tasks: 1},
	}
	c, _ := standin.LoadDefault()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := LoadQueries(filepath.Join(tt.suite, "cassettes", "queries.json"))
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "c.jsonl")
			if _, err := Record(context.Background(), q, Options{Out: out}); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(out)
			want, err := os.ReadFile(filepath.Join(tt.suite, "cassettes", "tavily.cassette.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("committed cassette is stale: go run ./research/cmd/tavily-record -suite %s -corpus-urls", tt.suite)
			}
			files, _ := filepath.Glob(filepath.Join(tt.suite, "tasks", "*", "verifier", "tests", "corpus_urls.json"))
			for _, f := range files {
				b, _ := os.ReadFile(f)
				if !strings.Contains(string(b), c.URLs()[0]) || strings.Count(string(b), "https://") != len(c.Docs) {
					t.Errorf("%s is stale", f)
				}
			}
			if len(files) != tt.tasks {
				t.Errorf("corpus_urls.json in %d tasks, want %d", len(files), tt.tasks)
			}
		})
	}
}

func TestLoadQueries(t *testing.T) {
	dir := t.TempDir()
	tests := []struct{ name, body, want string }{
		{name: "empty", body: `{"search": []}`, want: "lists nothing"},
		{name: "unknown key", body: `{"searches": ["x"]}`, want: "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, tt.name+".json")
			_ = os.WriteFile(p, []byte(tt.body), 0o644)
			if _, err := LoadQueries(p); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
