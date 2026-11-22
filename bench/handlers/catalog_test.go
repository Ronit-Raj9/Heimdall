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
	// probes is never empty: it lists the generic probes every benchmark shares (TestCatalogListsGenericProbes).
	for _, key := range []string{"agents", "metrics", "gates"} {
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

// TestCatalogListsGenericProbes (fix wave 2, N69): the generic probes every
// benchmark shares are catalog entries with their layer, so a view that looks
// a probe up in the catalog (the failure test funnel) names its layer. A
// probe file with the same id wins, and the shared catalog is never changed.
func TestCatalogListsGenericProbes(t *testing.T) {
	shared := &schemas.Catalog{Probes: []schemas.ProbeSpec{{ID: schemas.GenericProbeModelDown, Layer: schemas.LayerTool}}}
	d := fullDeps()
	d.catalog = liveCatalog{shared}
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/catalog", nil, nil))
	probes, _ := body["probes"].([]any)
	layers := map[string][]string{}
	for _, raw := range probes {
		p, _ := raw.(map[string]any)
		id, _ := p["id"].(string)
		layer, _ := p["layer"].(string)
		layers[id] = append(layers[id], layer)
	}
	if got := layers[schemas.GenericProbeModelBrownout]; len(got) != 1 || got[0] != string(schemas.LayerGateway) {
		t.Errorf("%s layers = %v, want [gateway]", schemas.GenericProbeModelBrownout, got)
	}
	if got := layers[schemas.GenericProbeModelDown]; len(got) != 1 || got[0] != string(schemas.LayerTool) {
		t.Errorf("%s layers = %v, want the probe file's [tool] once", schemas.GenericProbeModelDown, got)
	}
	for _, id := range schemas.GenericProbeIDs {
		if len(layers[id]) != 1 {
			t.Errorf("%s listed %d times", id, len(layers[id]))
		}
	}
	if len(shared.Probes) != 1 {
		t.Errorf("the shared catalog was changed in place: %d probes", len(shared.Probes))
	}
}
