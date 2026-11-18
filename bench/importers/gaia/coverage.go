package gaia

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/importers/researchkit"
	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/research/recording"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/toolproxy"
)

// checkCoverage replays every search and fetch of a recording list (the
// queries.json the cassette was recorded from, research/recording's format)
// through the real tool proxy code in replay mode, with the exact bodies
// the websearch tool sends, and fails unless each one is answered 200 from
// the cassette. So an imported web question is known to get recorded
// answers for at least the recorded queries; an agent that words its
// searches differently still gets "not recorded" errors.
func checkCoverage(tape, queriesJSON []byte) (string, error) {
	var q recording.Queries
	dec := json.NewDecoder(bytes.NewReader(queriesJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		return "", fmt.Errorf("queries.json: %w", err)
	}
	if len(q.Search) == 0 {
		return "", errors.New("queries.json lists no searches")
	}
	tmp, err := os.MkdirTemp("", "gaia-coverage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	cas := filepath.Join(tmp, "tavily.cassette.jsonl")
	if err := os.WriteFile(cas, tape, 0o600); err != nil {
		return "", err
	}
	routes := toolproxy.Routes{RunID: "gaia-coverage", Routes: []toolproxy.Route{{
		Service: researchkit.TavilyID, Upstream: "https://api.tavily.com", Protocol: toolproxy.ProtocolExternal,
		External: &toolproxy.ExternalRoute{
			Mode:     schemas.ExternalReplay,
			Cassette: cas,
			Allow:    []schemas.ExternalAllow{{Methods: []string{http.MethodPost}, PathPrefix: "/search"}, {Methods: []string{http.MethodPost}, PathPrefix: "/extract"}},
			MaxCalls: 100000, MaxBytes: 4 << 20, TimeoutS: 30,
		},
	}}}
	raw, err := json.Marshal(routes)
	if err != nil {
		return "", err
	}
	rp := filepath.Join(tmp, "routes.json")
	if err := os.WriteFile(rp, raw, 0o600); err != nil {
		return "", err
	}
	proxy, err := toolproxy.New(toolproxy.Config{RoutesPath: rp, LedgerPath: filepath.Join(tmp, "tools.jsonl"), Getenv: func(string) string { return "" }})
	if err != nil {
		return "", fmt.Errorf("cassette: %w", err)
	}
	defer proxy.Close()
	send := func(path string, body []byte) error {
		req := httptest.NewRequest(http.MethodPost, "/x/"+researchkit.TavilyID+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("%s %s answered %d from the cassette", path, body, rec.Code)
		}
		return nil
	}
	searches, fetches := 0, 0
	for _, s := range q.Search {
		if research.NormalizeQuery(s) == "" {
			continue
		}
		if err := send("/search", research.SearchBody(s)); err != nil {
			return "", err
		}
		searches++
	}
	for _, u := range q.Extract {
		if err := send("/extract", research.ExtractBody(u)); err != nil {
			return "", err
		}
		fetches++
	}
	return fmt.Sprintf("%d searches and %d fetches replayed from the cassette", searches, fetches), nil
}
