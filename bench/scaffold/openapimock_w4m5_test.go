package scaffold

import (
	"strings"
	"testing"
)

// A file that is not YAML gives one plain sentence, never the parser's chain.
func TestOpenAPINotYAMLPlain(t *testing.T) {
	_, _, err := MockFromOpenAPI("svc", []byte("openapi: [3.0\n  paths: {{{\n\t- broken"))
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	if strings.Contains(msg, "unmarshal") || strings.Contains(msg, "scaffold.") || strings.Contains(msg, "yaml:") {
		t.Fatalf("raw text: %q", msg)
	}
	if msg != "This is not an OpenAPI file Heimdall can read. It must be YAML or JSON with an openapi: 3.x field." {
		t.Fatalf("message %q", msg)
	}
}
