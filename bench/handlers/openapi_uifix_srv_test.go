package handlers_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestOpenAPIDescribesUIFixQuery: every query parameter the UI fix wave added
// is in the generated (hosted) OpenAPI document of its route.
func TestOpenAPIDescribesUIFixQuery(t *testing.T) {
	p := handlers.APIPrefix
	want := []struct {
		method, path string
		params       []string
	}{
		{"GET", p + "/experiments", []string{"limit", "offset", "q", "arm_stats"}},
		{"GET", p + "/evaluations", []string{"limit", "offset", "q", "status", "fields"}},
		{"GET", p + "/agents", []string{"limit", "offset", "q"}},
		{"GET", p + "/suites", []string{"fields", "limit", "offset", "q"}},
		{"GET", p + "/suites/{id}/task-list", []string{"limit", "offset", "q"}},
		{"GET", p + "/clusters", []string{"suite", "limit", "offset", "q"}},
		{"GET", p + "/catalog", []string{"limit", "offset", "q"}},
		{"GET", p + "/runs/{id}", []string{"failure"}},
		{"GET", p + "/suites/{id}/health", []string{"tasks"}},
		{"GET", p + "/jobs", []string{"status", "fields"}},
		{"GET", p + "/orgs/{org}/audit", []string{"cursor", "q"}},
		{"GET", p + "/experiments/{id}/gateway-effect", []string{"refresh", "optional"}},
		{"GET", p + "/data/retention", []string{"preview_days"}},
		{"POST", p + "/connect/first-look", []string{"estimate"}},
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
