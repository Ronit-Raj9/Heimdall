package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// liveCatalog hands out the same catalog every time, like the registry.
type liveCatalog struct{ c *schemas.Catalog }

func (l liveCatalog) Catalog() *schemas.Catalog { return l.c }

func TestCatalogSetsModeWithoutMutating(t *testing.T) {
	shared := &schemas.Catalog{Mode: schemas.ModeLive, Gates: []schemas.CatalogGate{{Name: "g"}}}
	d := fullDeps()
	d.catalog = liveCatalog{shared}
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/catalog", nil, nil))
	if body["mode"] != string(testMode) {
		t.Fatalf("mode = %v, want %s", body["mode"], testMode)
	}
	if shared.Mode != schemas.ModeLive {
		t.Errorf("shared catalog mutated: mode %s", shared.Mode)
	}
	if gates, _ := body["gates"].([]any); len(gates) != 1 {
		t.Errorf("gates = %v", body["gates"])
	}
}

// A bench root with no agents/ folder (or no suites, probes or metrics) answers
// empty lists, never null: the new experiment form reads them as lists.
func TestCatalogNilListsAnswerEmpty(t *testing.T) {
	shared := &schemas.Catalog{Suites: []schemas.CatalogSuite{{ID: "s", Version: "1"}}}
	d := fullDeps()
	d.catalog = liveCatalog{shared}
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/catalog", nil, nil))
	for _, key := range []string{"probes", "agents", "metrics", "gates"} {
		if list, ok := body[key].([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %#v, want []", key, body[key])
		}
	}
	if parts, ok := body["parts"].(map[string]any); !ok || len(parts) != 0 {
		t.Errorf("parts = %#v, want {}", body["parts"])
	}
	suite := body["suites"].([]any)[0].(map[string]any)
	if tasks, ok := suite["tasks"].([]any); !ok || len(tasks) != 0 {
		t.Errorf("suite tasks = %#v, want []", suite["tasks"])
	}
	if shared.Agents != nil || shared.Suites[0].Tasks != nil {
		t.Error("the shared catalog was changed in place")
	}
}
