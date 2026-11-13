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
