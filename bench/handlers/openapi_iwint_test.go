package handlers_test

import (
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Every component of the OpenAPI document has a name: an anonymous struct
// (gatewayFeaturesDoc's feature rows) is inlined, so the generated clients
// never get a nameless type (the Python client failed to import with one).
func TestOpenAPIHasNoNamelessComponent(t *testing.T) {
	registered := map[string][]string{}
	for _, r := range handlers.APIRoutes {
		registered[r.Method] = append(registered[r.Method], r.Path)
	}
	doc, _ := handlers.BuildOpenAPI(registered, "", schemas.ModeSynthetic)
	comps, _ := doc["components"].(map[string]any)
	sch, _ := comps["schemas"].(map[string]any)
	if len(sch) == 0 {
		t.Fatalf("no component schemas in %v", doc["components"])
	}
	if _, ok := sch[""]; ok {
		t.Fatal(`a component schema is named "": an anonymous struct became a component`)
	}
}
