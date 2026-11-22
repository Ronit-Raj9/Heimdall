package agentevents

import "testing"

func TestDecodeSpansKeepsScopeKindAndResource(t *testing.T) {
	for name, tc := range map[string]struct {
		body []byte
		ct   string
		kind string
	}{
		"json":  {[]byte(otlpJSON), "application/json", ""},
		"proto": {protoRequest(), "application/x-protobuf", "internal"},
	} {
		spans, err := DecodeSpans(tc.body, tc.ct)
		if err != nil || len(spans) != 5 {
			t.Fatalf("%s: %v %d", name, err, len(spans))
		}
		s := spans[2]
		wantScope := "x"
		if name == "proto" {
			wantScope = "scope"
		}
		if s.Scope != wantScope || s.Service != "my-agent" || s.Resource["service.name"] != "my-agent" || s.Kind != tc.kind {
			t.Fatalf("%s: %+v", name, s)
		}
		if s.Type != TypeToolCall || !s.Error || s.Attributes["gen_ai.tool.name"] != "read_file" || s.ParentID != "aaaaaaaaaaaaaaa2" {
			t.Fatalf("%s: %+v", name, s)
		}
	}
}
