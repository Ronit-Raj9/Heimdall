package export

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/traceexport"
)

// MarshalOTLPProto encodes a trace as the protobuf ExportTraceServiceRequest
// of opentelemetry-proto (collector/trace/v1), by hand, so the bench needs no
// protobuf dependency. It is the counterpart of agentevents/pbwire.go's
// decoder and uses the same field numbers:
//
//	ExportTraceServiceRequest: 1 resource_spans
//	ResourceSpans: 1 resource, 2 scope_spans, 3 schema_url
//	Resource: 1 attributes
//	ScopeSpans: 1 scope, 2 spans, 3 schema_url
//	InstrumentationScope: 1 name, 2 version, 3 attributes
//	Span: 1 trace_id, 2 span_id, 3 trace_state, 4 parent_span_id (bytes),
//	      5 name, 6 kind (enum), 7 start_time_unix_nano, 8 end_time_unix_nano
//	      (fixed64), 9 attributes, 11 events, 13 links, 15 status
//	Span.Event: 1 time_unix_nano (fixed64), 2 name, 3 attributes
//	Span.Link: 1 trace_id, 2 span_id, 4 attributes
//	Status: 2 message, 3 code (enum)
//	KeyValue: 1 key, 2 value
//	AnyValue: 1 string, 2 bool, 3 int (varint), 4 double (fixed64),
//	          5 array (ArrayValue: 1 values), 6 kvlist (KeyValueList: 1
//	          values), 7 bytes
//
// Fields are written in field-number order and empty fields are left out, as
// a protobuf encoder does, so the same trace always gives the same bytes.
func MarshalOTLPProto(td *traceexport.TracesData) ([]byte, error) {
	var out []byte
	for _, rs := range td.ResourceSpans {
		b, err := pbResourceSpans(rs)
		if err != nil {
			return nil, err
		}
		out = pbBytes(out, 1, b)
	}
	return out, nil
}

func pbResourceSpans(rs traceexport.ResourceSpans) ([]byte, error) {
	var b []byte
	res, err := pbAttrs(nil, 1, rs.Resource.Attributes)
	if err != nil {
		return nil, err
	}
	b = pbBytes(b, 1, res)
	for _, ss := range rs.ScopeSpans {
		sb, err := pbScopeSpans(ss)
		if err != nil {
			return nil, err
		}
		b = pbBytes(b, 2, sb)
	}
	return pbString(b, 3, rs.SchemaURL), nil
}

func pbScopeSpans(ss traceexport.ScopeSpans) ([]byte, error) {
	scope := pbString(nil, 1, ss.Scope.Name)
	scope = pbString(scope, 2, ss.Scope.Version)
	scope, err := pbAttrs(scope, 3, ss.Scope.Attributes)
	if err != nil {
		return nil, err
	}
	b := pbBytes(nil, 1, scope)
	for _, sp := range ss.Spans {
		sb, err := pbSpan(sp)
		if err != nil {
			return nil, err
		}
		b = pbBytes(b, 2, sb)
	}
	return pbString(b, 3, ss.SchemaURL), nil
}

func pbSpan(sp traceexport.Span) ([]byte, error) {
	var b []byte
	var err error
	if b, err = pbHexID(b, 1, sp.TraceID, 16); err != nil {
		return nil, err
	}
	if b, err = pbHexID(b, 2, sp.SpanID, 8); err != nil {
		return nil, err
	}
	b = pbString(b, 3, sp.TraceState)
	if sp.ParentSpanID != "" {
		if b, err = pbHexID(b, 4, sp.ParentSpanID, 8); err != nil {
			return nil, err
		}
	}
	b = pbString(b, 5, sp.Name)
	if sp.Kind != 0 {
		b = pbVarint(b, 6, uint64(sp.Kind))
	}
	b = pbFixed64(b, 7, uint64(sp.StartTimeUnixNano))
	b = pbFixed64(b, 8, uint64(sp.EndTimeUnixNano))
	if b, err = pbAttrs(b, 9, sp.Attributes); err != nil {
		return nil, err
	}
	for _, ev := range sp.Events {
		e := pbFixed64(nil, 1, uint64(ev.TimeUnixNano))
		e = pbString(e, 2, ev.Name)
		if e, err = pbAttrs(e, 3, ev.Attributes); err != nil {
			return nil, err
		}
		b = pbBytes(b, 11, e)
	}
	for _, l := range sp.Links {
		var lb []byte
		if lb, err = pbHexID(lb, 1, l.TraceID, 16); err != nil {
			return nil, err
		}
		if lb, err = pbHexID(lb, 2, l.SpanID, 8); err != nil {
			return nil, err
		}
		if lb, err = pbAttrs(lb, 4, l.Attributes); err != nil {
			return nil, err
		}
		b = pbBytes(b, 13, lb)
	}
	if sp.Status != nil {
		st := pbString(nil, 2, sp.Status.Message)
		if sp.Status.Code != 0 {
			st = pbVarint(st, 3, uint64(sp.Status.Code))
		}
		b = pbBytes(b, 15, st)
	}
	return b, nil
}

// pbAttrs appends each attribute as a KeyValue message in field n.
func pbAttrs(b []byte, n int, attrs []traceexport.KeyValue) ([]byte, error) {
	for _, kv := range attrs {
		v, err := pbAnyValue(kv.Value, 0)
		if err != nil {
			return nil, fmt.Errorf("attribute %s: %w", kv.Key, err)
		}
		m := pbString(nil, 1, kv.Key)
		m = pbBytes(m, 2, v)
		b = pbBytes(b, n, m)
	}
	return b, nil
}

func pbAnyValue(v traceexport.AnyValue, depth int) ([]byte, error) {
	if depth > 8 {
		return nil, fmt.Errorf("value nested too deep")
	}
	switch {
	case v.StringValue != nil:
		return pbStringAlways(nil, 1, *v.StringValue), nil
	case v.BoolValue != nil:
		x := uint64(0)
		if *v.BoolValue {
			x = 1
		}
		return pbVarintAlways(nil, 2, x), nil
	case v.IntValue != nil:
		return pbVarintAlways(nil, 3, uint64(int64(*v.IntValue))), nil
	case v.DoubleValue != nil:
		return pbFixed64Always(nil, 4, math.Float64bits(*v.DoubleValue)), nil
	case v.ArrayValue != nil:
		var arr []byte
		for _, x := range v.ArrayValue.Values {
			e, err := pbAnyValue(x, depth+1)
			if err != nil {
				return nil, err
			}
			arr = pbBytes(arr, 1, e)
		}
		return pbBytesAlways(nil, 5, arr), nil
	case v.KvlistValue != nil:
		kvl, err := pbAttrs(nil, 1, v.KvlistValue.Values)
		if err != nil {
			return nil, err
		}
		return pbBytesAlways(nil, 6, kvl), nil
	case v.BytesValue != nil:
		raw, err := base64.StdEncoding.DecodeString(*v.BytesValue)
		if err != nil {
			return nil, fmt.Errorf("bytes value is not base64")
		}
		return pbBytesAlways(nil, 7, raw), nil
	}
	return nil, nil
}

func pbHexID(b []byte, n int, id string, size int) ([]byte, error) {
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != size {
		return nil, fmt.Errorf("id %q is not %d hex bytes", id, size)
	}
	return pbBytesAlways(b, n, raw), nil
}

func pbKey(b []byte, n, wire int) []byte {
	return binary.AppendUvarint(b, uint64(n)<<3|uint64(wire))
}

func pbVarint(b []byte, n int, v uint64) []byte {
	if v == 0 {
		return b
	}
	return pbVarintAlways(b, n, v)
}

func pbVarintAlways(b []byte, n int, v uint64) []byte {
	return binary.AppendUvarint(pbKey(b, n, 0), v)
}

func pbFixed64(b []byte, n int, v uint64) []byte {
	if v == 0 {
		return b
	}
	return pbFixed64Always(b, n, v)
}

func pbFixed64Always(b []byte, n int, v uint64) []byte {
	return binary.LittleEndian.AppendUint64(pbKey(b, n, 1), v)
}

func pbString(b []byte, n int, s string) []byte {
	if s == "" {
		return b
	}
	return pbStringAlways(b, n, s)
}

func pbStringAlways(b []byte, n int, s string) []byte {
	return append(binary.AppendUvarint(pbKey(b, n, 2), uint64(len(s))), s...)
}

func pbBytes(b []byte, n int, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	return pbBytesAlways(b, n, v)
}

func pbBytesAlways(b []byte, n int, v []byte) []byte {
	return append(binary.AppendUvarint(pbKey(b, n, 2), uint64(len(v))), v...)
}
