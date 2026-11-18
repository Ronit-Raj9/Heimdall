// Package recording records a research suite's Tavily cassette through the
// real tool proxy code in record mode (docs/research-tasks.md). It builds a
// toolproxy.Server with one external route, id tavily, in record mode,
// sends it the exact bodies the websearch tool sends (research.SearchBody
// and research.ExtractBody) for a list of queries and URLs, and copies the
// cassette the proxy wrote. The upstream is either the in-process Tavily
// stand-in (no key, no network) or a real Tavily endpoint with the key taken
// from the environment and injected by the proxy, never printed.
package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/research/standin"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/toolproxy"
)

// standinKey is the key the in-process stand-in demands and the proxy
// injects, so a recording from the stand-in proves the credential path too.
const standinKey = "standin-record-key"

// Queries is a suite's recording list (cassettes/queries.json).
type Queries struct {
	// Search are the queries to record, as an agent would type them; each
	// is sent normalized, once.
	Search []string `json:"search"`
	// Extract are page URLs to record fetches of.
	Extract []string `json:"extract,omitempty"`
	// ExtractCorpus, with the stand-in, also records a fetch of every page
	// of the corpus, so fetch_page answers for any corpus URL in replay.
	ExtractCorpus bool `json:"extract_corpus,omitempty"`
}

// LoadQueries reads a recording list.
func LoadQueries(path string) (*Queries, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read queries: %w", err)
	}
	var q Queries
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(q.Search) == 0 && len(q.Extract) == 0 && !q.ExtractCorpus {
		return nil, fmt.Errorf("%s lists nothing to record", path)
	}
	return &q, nil
}

// Options configures Record.
type Options struct {
	// Upstream is the Tavily base URL, e.g. https://api.tavily.com. Empty
	// starts the embedded stand-in on a loopback port.
	Upstream string
	// KeyEnv names the variable holding the key (default TAVILY_API_KEY).
	// With a real upstream it must be set; its value is read through
	// Getenv by the proxy and never returned or printed.
	KeyEnv string
	Getenv func(string) string // default os.Getenv
	Out    string              // the cassette to write
}

// Result says what was recorded.
type Result struct {
	Upstream string // "stand-in" or the upstream's host
	Searches int
	Extracts int
	Cassette string
	Failed   []string // calls that did not answer 200 (the cassette still records them)
}

// Record sends every query and URL of q through a record-mode tool proxy
// and writes the cassette to opts.Out (replacing it).
func Record(ctx context.Context, q *Queries, opts Options) (*Result, error) {
	if opts.Out == "" {
		return nil, errors.New("no output cassette")
	}
	keyEnv := opts.KeyEnv
	if keyEnv == "" {
		keyEnv = "TAVILY_API_KEY"
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	res := &Result{Cassette: opts.Out}
	upstream := strings.TrimRight(opts.Upstream, "/")
	var corpus *standin.Corpus
	if upstream == "" {
		c, err := standin.LoadDefault()
		if err != nil {
			return nil, err
		}
		corpus = c
		srv := httptest.NewServer(standin.New(c, standinKey))
		defer srv.Close()
		upstream, res.Upstream = srv.URL, "stand-in"
		getenv = func(k string) string {
			if k == keyEnv {
				return standinKey
			}
			return ""
		}
	} else {
		if getenv(keyEnv) == "" {
			return nil, fmt.Errorf("%s is empty: set the Tavily key in the environment of this command", keyEnv)
		}
		res.Upstream = upstream
	}
	tmp, err := os.MkdirTemp("", "tavily-record-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	tape := filepath.Join(tmp, "tavily.cassette.jsonl")
	routes := toolproxy.Routes{RunID: "tavily-record", Routes: []toolproxy.Route{{
		Service: research.APIID, Upstream: upstream, Protocol: toolproxy.ProtocolExternal,
		External: &toolproxy.ExternalRoute{
			Mode:       schemas.ExternalRecord,
			RecordTo:   tape,
			Allow:      []schemas.ExternalAllow{{Methods: []string{http.MethodPost}, PathPrefix: "/search"}, {Methods: []string{http.MethodPost}, PathPrefix: "/extract"}},
			Headers:    map[string]string{"Content-Type": "application/json"},
			AuthEnv:    keyEnv,
			AuthHeader: "Authorization",
			AuthScheme: "Bearer",
			MaxCalls:   100000,
			MaxBytes:   4 << 20,
			TimeoutS:   60,
		},
	}}}
	raw, err := json.Marshal(routes)
	if err != nil {
		return nil, fmt.Errorf("encode routes: %w", err)
	}
	routesPath := filepath.Join(tmp, "routes.json")
	if err := os.WriteFile(routesPath, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write routes: %w", err)
	}
	proxy, err := toolproxy.New(toolproxy.Config{RoutesPath: routesPath, LedgerPath: filepath.Join(tmp, "tools.jsonl"), Getenv: getenv})
	if err != nil {
		return nil, fmt.Errorf("start tool proxy: %w", err)
	}
	send := func(path string, body []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		req := httptest.NewRequest(http.MethodPost, "/x/"+research.APIID+path, bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			res.Failed = append(res.Failed, fmt.Sprintf("%s %s: status %d: %.200s", path, body, rec.Code, rec.Body.String()))
		}
		return nil
	}
	seen := map[string]bool{}
	for _, s := range q.Search {
		body := research.SearchBody(s)
		if research.NormalizeQuery(s) == "" || seen[string(body)] {
			continue
		}
		seen[string(body)] = true
		if err := send("/search", body); err != nil {
			_ = proxy.Close()
			return nil, err
		}
		res.Searches++
	}
	urls := append([]string(nil), q.Extract...)
	if q.ExtractCorpus && corpus != nil {
		urls = append(urls, corpus.URLs()...)
	}
	for _, u := range urls {
		body := research.ExtractBody(u)
		if seen[string(body)] {
			continue
		}
		seen[string(body)] = true
		if err := send("/extract", body); err != nil {
			_ = proxy.Close()
			return nil, err
		}
		res.Extracts++
	}
	if err := proxy.Close(); err != nil {
		return nil, fmt.Errorf("close tool proxy: %w", err)
	}
	b, err := os.ReadFile(tape)
	if err != nil {
		return nil, fmt.Errorf("read recorded cassette: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Out), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(opts.Out), err)
	}
	tmpOut := opts.Out + ".tmp"
	if err := os.WriteFile(tmpOut, b, 0o644); err != nil {
		return nil, fmt.Errorf("write cassette: %w", err)
	}
	if err := os.Rename(tmpOut, opts.Out); err != nil {
		return nil, fmt.Errorf("write cassette: %w", err)
	}
	return res, nil
}

// WriteCorpusURLs writes the stand-in corpus's URL list (sorted, one JSON
// list) to research.CorpusURLsFile in every task's verifier tests folder of
// a suite folder (tasks/*/verifier/tests), where the verifier reads it to
// tell a corpus page from an invented citation. It returns the files
// written.
func WriteCorpusURLs(suiteDir string) ([]string, error) {
	c, err := standin.LoadDefault()
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(c.URLs(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode corpus urls: %w", err)
	}
	b = append(b, '\n')
	dirs, err := filepath.Glob(filepath.Join(suiteDir, "tasks", "*", "verifier", "tests"))
	if err != nil {
		return nil, fmt.Errorf("find task verifier folders: %w", err)
	}
	var out []string
	for _, d := range dirs {
		p := filepath.Join(d, research.CorpusURLsFile)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return out, fmt.Errorf("write %s: %w", p, err)
		}
		out = append(out, p)
	}
	return out, nil
}
