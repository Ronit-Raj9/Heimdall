package agentevents

import (
	"mime"
	"strings"
	"time"
)

// Span is one decoded OTLP span with every attribute kept, for readers that
// need more than a Record carries, such as agent inference from traces
// (bench/agentinfer). Type is the same classification ParseOTLP gives.
type Span struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentID     string            `json:"parent_id,omitempty"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Kind         string            `json:"kind,omitempty"` // server | client | internal | producer | consumer
	Start        time.Time         `json:"start,omitzero"`
	End          time.Time         `json:"end,omitzero"`
	Error        bool              `json:"error,omitempty"`
	Service      string            `json:"service,omitempty"`
	Scope        string            `json:"scope,omitempty"` // instrumentation scope name
	ScopeVersion string            `json:"scope_version,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Resource     map[string]string `json:"resource,omitempty"`
}

var spanKinds = map[int]string{1: "internal", 2: "server", 3: "client", 4: "producer", 5: "consumer"}

// DecodeSpans decodes an OTLP/HTTP trace export request (protobuf when the
// content type says protobuf, JSON otherwise) into spans with their full
// attributes. Spans with no span id are dropped.
func DecodeSpans(data []byte, contentType string) ([]Span, error) {
	mt, _, _ := mime.ParseMediaType(contentType)
	var spans []span
	var err error
	if strings.Contains(mt, "protobuf") {
		spans, err = decodeProtoRequest(data)
	} else {
		spans, err = decodeJSONRequest(data)
	}
	if err != nil {
		return nil, err
	}
	out := make([]Span, 0, len(spans))
	for _, sp := range spans {
		if sp.spanID == "" {
			continue
		}
		out = append(out, Span{
			TraceID: sp.traceID, SpanID: sp.spanID, ParentID: sp.parentID, Name: sp.name,
			Type: spanType(sp), Kind: spanKinds[sp.kind], Start: sp.start, End: sp.end,
			Error: sp.statusCode == 2, Service: sp.service, Scope: sp.scope, ScopeVersion: sp.scopeVersion,
			Attributes: sp.attrs, Resource: sp.resource,
		})
	}
	return out, nil
}
