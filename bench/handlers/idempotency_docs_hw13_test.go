package handlers_test

import (
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Every POST documents the optional Idempotency-Key header and the 409 and
// 422 answers of the idempotency middleware; a GET does not (fix wave 3, N186).
func TestOpenAPIPostsDocumentIdempotencyKey(t *testing.T) {
	registered := map[string][]string{
		"GET":  {handlers.APIPrefix + "/evaluations"},
		"POST": {handlers.APIPrefix + "/evaluations"},
	}
	doc, _ := handlers.BuildOpenAPI(registered, "", schemas.ModeSynthetic)
	ops := doc["paths"].(map[string]map[string]any)[handlers.APIPrefix+"/evaluations"]
	hasKey := func(op map[string]any) bool {
		params, _ := op["parameters"].([]any)
		for _, p := range params {
			if m, _ := p.(map[string]any); m["name"] == "Idempotency-Key" && m["in"] == "header" && m["required"] == false {
				return true
			}
		}
		return false
	}
	post := ops["post"].(map[string]any)
	if !hasKey(post) {
		t.Fatalf("POST has no Idempotency-Key header: %v", post["parameters"])
	}
	responses := post["responses"].(map[string]any)
	for _, code := range []string{"409", "422", "202"} {
		if _, ok := responses[code]; !ok {
			t.Fatalf("POST responses lack %s: %v", code, responses)
		}
	}
	if hasKey(ops["get"].(map[string]any)) {
		t.Fatal("GET documents Idempotency-Key")
	}
}
