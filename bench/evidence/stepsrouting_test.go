package evidence

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestModelSwitch (W9): a turn answered by another model than the previous
// answered turn is flagged; a failed turn changes nothing.
func TestModelSwitch(t *testing.T) {
	ok := func(model string, fallback int) schemas.GatewayRequest {
		return schemas.GatewayRequest{Status: statusSuccess, ModelServed: model, FallbackIndex: fallback}
	}
	failed := schemas.GatewayRequest{Status: "error"}
	cases := []struct {
		name       string
		prev       string
		rows       []schemas.GatewayRequest
		want, next string
	}{
		{"first turn", "", []schemas.GatewayRequest{ok("strong", 0)}, "", "strong"},
		{"same model", "strong", []schemas.GatewayRequest{ok("anthropic/strong", 0)}, "", "anthropic/strong"},
		{"fallback answered", "strong", []schemas.GatewayRequest{failed, ok("cheap", 1)}, "strong to cheap", "cheap"},
		{"failed turn keeps prev", "strong", []schemas.GatewayRequest{failed}, "", "strong"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, next := modelSwitch(c.prev, c.rows)
			if got != c.want || next != c.next {
				t.Fatalf("switch %q next %q, want %q %q", got, next, c.want, c.next)
			}
		})
	}
}
