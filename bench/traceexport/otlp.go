package traceexport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The types in this file are the OTLP/JSON trace format: the protobuf JSON
// mapping of opentelemetry/proto/collector/trace/v1 ExportTraceServiceRequest,
// with the OTLP deviations (https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding):
// keys are lowerCamelCase, trace and span ids are hex strings (not base64),
// and enums are integers. 64-bit integers are written as decimal strings and
// read from strings or numbers, as the protobuf JSON mapping allows. They are
// hand-written so the bench needs no OpenTelemetry dependency, and they are
// exported so the trace import side reads the same shapes.

// TracesData is one OTLP/JSON trace document: the body of a POST to
// /v1/traces and the content of an exported .otlp.json file.
type TracesData struct {
	ResourceSpans []ResourceSpans `json:"resourceSpans"`
}

// ResourceSpans is the spans of one resource. The exporter writes one per
// run, so every run is its own trace with its own resource attributes.
type ResourceSpans struct {
	Resource   Resource     `json:"resource"`
	ScopeSpans []ScopeSpans `json:"scopeSpans"`
	SchemaURL  string       `json:"schemaUrl,omitempty"`
}

// Resource describes the entity that produced the spans.
type Resource struct {
	Attributes []KeyValue `json:"attributes,omitempty"`
}

// ScopeSpans is the spans one instrumentation scope produced.
type ScopeSpans struct {
	Scope     Scope  `json:"scope"`
	Spans     []Span `json:"spans"`
	SchemaURL string `json:"schemaUrl,omitempty"`
}

// Scope names the instrumentation scope.
type Scope struct {
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	Attributes []KeyValue `json:"attributes,omitempty"`
}

// Span is one OTLP span. TraceID is 32 lowercase hex characters, SpanID and
// ParentSpanID 16; a root span has no ParentSpanID.
type Span struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId,omitempty"`
	TraceState        string      `json:"traceState,omitempty"`
	Name              string      `json:"name"`
	Kind              SpanKind    `json:"kind"`
	StartTimeUnixNano Uint64      `json:"startTimeUnixNano"`
	EndTimeUnixNano   Uint64      `json:"endTimeUnixNano"`
	Attributes        []KeyValue  `json:"attributes,omitempty"`
	Events            []SpanEvent `json:"events,omitempty"`
	Links             []SpanLink  `json:"links,omitempty"`
	Status            *Status     `json:"status,omitempty"`
}

// SpanKind is the OTLP span kind enum.
type SpanKind int

// Span kinds, with their OTLP enum values.
const (
	SpanKindUnspecified SpanKind = 0
	SpanKindInternal    SpanKind = 1
	SpanKindServer      SpanKind = 2
	SpanKindClient      SpanKind = 3
	SpanKindProducer    SpanKind = 4
	SpanKindConsumer    SpanKind = 5
)

// SpanEvent is a timestamped annotation on a span.
type SpanEvent struct {
	TimeUnixNano Uint64     `json:"timeUnixNano"`
	Name         string     `json:"name"`
	Attributes   []KeyValue `json:"attributes,omitempty"`
}

// SpanLink points from a span to a span of another (or the same) trace.
type SpanLink struct {
	TraceID    string     `json:"traceId"`
	SpanID     string     `json:"spanId"`
	Attributes []KeyValue `json:"attributes,omitempty"`
}

// Status is a span's status. A nil status (or code unset) means no status
// was set.
type Status struct {
	Code    StatusCode `json:"code,omitempty"`
	Message string     `json:"message,omitempty"`
}

// StatusCode is the OTLP status code enum.
type StatusCode int

// Status codes, with their OTLP enum values.
const (
	StatusUnset StatusCode = 0
	StatusOK    StatusCode = 1
	StatusError StatusCode = 2
)

// KeyValue is one attribute.
type KeyValue struct {
	Key   string   `json:"key"`
	Value AnyValue `json:"value"`
}

// AnyValue holds exactly one of its fields. IntValue is an int64, written as
// a decimal string.
type AnyValue struct {
	StringValue *string       `json:"stringValue,omitempty"`
	BoolValue   *bool         `json:"boolValue,omitempty"`
	IntValue    *Int64        `json:"intValue,omitempty"`
	DoubleValue *float64      `json:"doubleValue,omitempty"`
	ArrayValue  *ArrayValue   `json:"arrayValue,omitempty"`
	KvlistValue *KeyValueList `json:"kvlistValue,omitempty"`
	BytesValue  *string       `json:"bytesValue,omitempty"` // base64, as the protobuf JSON mapping writes bytes
}

// ArrayValue is a list of values.
type ArrayValue struct {
	Values []AnyValue `json:"values"`
}

// KeyValueList is a nested map of values.
type KeyValueList struct {
	Values []KeyValue `json:"values"`
}

// Str returns a string attribute.
func Str(key, v string) KeyValue {
	return KeyValue{Key: key, Value: AnyValue{StringValue: &v}}
}

// Int returns an integer attribute.
func Int(key string, v int64) KeyValue {
	i := Int64(v)
	return KeyValue{Key: key, Value: AnyValue{IntValue: &i}}
}

// Bool returns a boolean attribute.
func Bool(key string, v bool) KeyValue {
	return KeyValue{Key: key, Value: AnyValue{BoolValue: &v}}
}

// Double returns a floating point attribute.
func Double(key string, v float64) KeyValue {
	return KeyValue{Key: key, Value: AnyValue{DoubleValue: &v}}
}

// Strs returns a string array attribute.
func Strs(key string, vs []string) KeyValue {
	arr := &ArrayValue{Values: make([]AnyValue, len(vs))}
	for i := range vs {
		s := vs[i]
		arr.Values[i] = AnyValue{StringValue: &s}
	}
	return KeyValue{Key: key, Value: AnyValue{ArrayValue: arr}}
}

// Attr returns the value of the first attribute named key.
func Attr(attrs []KeyValue, key string) (AnyValue, bool) {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return AnyValue{}, false
}

// String renders the value as text: a string as is, numbers and booleans in
// Go syntax, arrays and maps as JSON. An empty value is "".
func (v AnyValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	case v.IntValue != nil:
		return strconv.FormatInt(int64(*v.IntValue), 10)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64)
	case v.BytesValue != nil:
		return *v.BytesValue
	case v.ArrayValue != nil, v.KvlistValue != nil:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
	return ""
}

// Validate checks the structure of a trace document: every trace id is 32
// lowercase hex characters and every span id 16, neither all zeros; span ids
// are unique within their trace; every parent span id names a span of the
// same trace; every span has a name and a start time no later than its end.
// It returns the first problem found, in document order. It does not require
// one root per trace, so a partial trace from another producer still passes.
func Validate(td *TracesData) error {
	if td == nil {
		return fmt.Errorf("validate: nil traces data")
	}
	type spanKey struct{ trace, span string }
	seen := map[spanKey]bool{}
	var all []Span
	for ri, rs := range td.ResourceSpans {
		for si, ss := range rs.ScopeSpans {
			for pi, sp := range ss.Spans {
				where := fmt.Sprintf("resourceSpans[%d].scopeSpans[%d].spans[%d]", ri, si, pi)
				if err := checkSpan(sp); err != nil {
					return fmt.Errorf("%s: %w", where, err)
				}
				k := spanKey{sp.TraceID, sp.SpanID}
				if seen[k] {
					return fmt.Errorf("%s: span id %s repeats in trace %s", where, sp.SpanID, sp.TraceID)
				}
				seen[k] = true
				all = append(all, sp)
			}
		}
	}
	for _, sp := range all {
		if sp.ParentSpanID != "" && !seen[spanKey{sp.TraceID, sp.ParentSpanID}] {
			return fmt.Errorf("span %s (%s): parent %s is not in trace %s", sp.SpanID, sp.Name, sp.ParentSpanID, sp.TraceID)
		}
	}
	return nil
}

// checkSpan checks one span's own fields.
func checkSpan(sp Span) error {
	if !isHexID(sp.TraceID, 32) {
		return fmt.Errorf("trace id %q is not 32 lowercase hex characters (not all zeros)", sp.TraceID)
	}
	if !isHexID(sp.SpanID, 16) {
		return fmt.Errorf("span id %q is not 16 lowercase hex characters (not all zeros)", sp.SpanID)
	}
	if sp.ParentSpanID != "" && !isHexID(sp.ParentSpanID, 16) {
		return fmt.Errorf("parent span id %q is not 16 lowercase hex characters (not all zeros)", sp.ParentSpanID)
	}
	if sp.ParentSpanID == sp.SpanID {
		return fmt.Errorf("span %s is its own parent", sp.SpanID)
	}
	if sp.Name == "" {
		return fmt.Errorf("span %s has no name", sp.SpanID)
	}
	if sp.StartTimeUnixNano == 0 {
		return fmt.Errorf("span %s (%s) has no start time", sp.SpanID, sp.Name)
	}
	if sp.EndTimeUnixNano < sp.StartTimeUnixNano {
		return fmt.Errorf("span %s (%s) ends before it starts", sp.SpanID, sp.Name)
	}
	return nil
}

// isHexID reports whether s is n lowercase hex characters and not all zeros.
func isHexID(s string, n int) bool {
	if len(s) != n || strings.Trim(s, "0") == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Uint64 is an unsigned 64-bit integer (fixed64 in the proto, such as span
// times in unix nanoseconds). It is written as a decimal string and read
// from a string or a number.
type Uint64 uint64

// MarshalJSON writes the number as a decimal string.
func (u Uint64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatUint(uint64(u), 10) + `"`), nil
}

// UnmarshalJSON reads a decimal string or a JSON number.
func (u *Uint64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*u = 0
		return nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("uint64 %s: %w", string(b), err)
	}
	*u = Uint64(n)
	return nil
}

// Int64 is a signed 64-bit integer (an intValue attribute). It is written as
// a decimal string and read from a string or a number.
type Int64 int64

// MarshalJSON writes the number as a decimal string.
func (i Int64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

// UnmarshalJSON reads a decimal string or a JSON number.
func (i *Int64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("int64 %s: %w", string(b), err)
	}
	*i = Int64(n)
	return nil
}
