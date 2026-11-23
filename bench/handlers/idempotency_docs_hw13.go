package handlers

import (
	"reflect"

	"github.com/maximhq/bifrost/bench/lib"
)

// documentIdempotencyKey adds the optional Idempotency-Key header and the
// 409 and 422 answers of the idempotency middleware (idempotency package,
// fix wave 3, N186) to one POST operation. Existing parameters and answers
// are kept.
func (g *schemaGen) documentIdempotencyKey(op map[string]any) {
	params, _ := op["parameters"].([]any)
	op["parameters"] = append(params, map[string]any{
		"name": "Idempotency-Key", "in": "header", "required": false,
		"description": "Optional. The same key within 24 hours returns the first answer instead of starting the work again.",
		"schema":      map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{16,64}$"},
	})
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		return
	}
	errBody := func(desc string) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{
			"schema": g.schema(reflect.TypeOf(lib.ErrorBody{})),
		}}}
	}
	if _, ok := responses["409"]; !ok {
		responses["409"] = errBody("A request with this Idempotency-Key is still being processed.")
	}
	if _, ok := responses["422"]; !ok {
		responses["422"] = errBody("This Idempotency-Key was already used for a different request.")
	}
}
