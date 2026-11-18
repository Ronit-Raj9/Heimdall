package evidence

import "testing"

// B2 MCP through Bifrost: Bifrost prefixes an MCP tool with its client name
// and a hyphen; the bench's client names carry the service id.
func TestToolNameFitsBifrostPrefix(t *testing.T) {
	if !toolNameFits("desk_r_abc123-get_ticket", "desk", "get_ticket") {
		t.Fatal("a Bifrost-prefixed tool name must fit the proxied tool")
	}
	if toolNameFits("billing_r_abc123-get_ticket", "desk", "get_ticket") {
		t.Fatal("another server's client must not fit")
	}
}
