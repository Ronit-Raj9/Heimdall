package handlers_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestOpenAPIDescribesD1Routes: the archive and unarchive routes, the
// monitor test delete and every list's archived= are in the hosted
// OpenAPI document (UI fix wave D1).
func TestOpenAPIDescribesD1Routes(t *testing.T) {
	p := handlers.APIPrefix
	want := []struct {
		method, path string
		params       []string
	}{
		{"GET", p + "/evaluations", []string{"archived"}},
		{"GET", p + "/experiments", []string{"archived"}},
		{"GET", p + "/runs", []string{"archived"}},
		{"GET", p + "/improvements", []string{"archived"}},
		{"GET", p + "/findings", []string{"archived", "state"}},
		{"GET", p + "/shares", []string{"kind", "target"}},
		{"DELETE", p + "/monitors/certifications/{id}", nil},
	}
	for _, obj := range []string{"/evaluations/{id}", "/experiments/{id}", "/runs/{id}", "/improvements/{id}", "/findings/{id}"} {
		want = append(want, struct {
			method, path string
			params       []string
		}{"POST", p + obj + "/archive", nil}, struct {
			method, path string
			params       []string
		}{"POST", p + obj + "/unarchive", nil})
	}
	registered := map[string][]string{}
	for _, w := range want {
		registered[w.method] = append(registered[w.method], w.path)
	}
	doc, undocumented := handlers.BuildHostedOpenAPI(registered, "", schemas.ModeSynthetic)
	if len(undocumented) != 0 {
		t.Fatalf("undocumented %v", undocumented)
	}
	paths := doc["paths"].(map[string]map[string]any)
	for _, w := range want {
		op, ok := paths[w.path][strings.ToLower(w.method)]
		if !ok {
			t.Errorf("%s %s missing", w.method, w.path)
			continue
		}
		b, _ := json.Marshal(op.(map[string]any)["parameters"])
		for _, name := range w.params {
			if !strings.Contains(string(b), `"name":"`+name+`"`) {
				t.Errorf("%s %s lacks query %s: %s", w.method, w.path, name, b)
			}
		}
	}
}
