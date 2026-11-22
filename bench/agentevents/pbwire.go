package agentevents

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// A minimal decoder for the protobuf encoding of an OTLP trace export
// request, written by hand so the bench needs no protobuf dependency. It
// reads only the fields the package uses and skips every other field by its
// wire type, so newer OTLP versions decode too. Field numbers are those of
// opentelemetry/proto/collector/trace/v1 and trace/v1, common/v1:
//
//	ExportTraceServiceRequest: 1 resource_spans (ResourceSpans)
//	ResourceSpans: 1 resource (Resource), 2 scope_spans (ScopeSpans)
//	Resource: 1 attributes (KeyValue)
//	ScopeSpans: 1 scope (InstrumentationScope: 1 name, 2 version), 2 spans (Span)
//	Span: 1 trace_id, 2 span_id, 4 parent_span_id (bytes), 5 name,
//	      6 kind (enum), 7 start_time_unix_nano, 8 end_time_unix_nano (fixed64),
//	      9 attributes (KeyValue), 15 status (Status)
//	Status: 2 message, 3 code (enum)
//	KeyValue: 1 key, 2 value (AnyValue)
//	AnyValue: 1 string, 2 bool, 3 int (varint), 4 double (fixed64),
//	          5 array (ArrayValue: 1 values), 6 kvlist (KeyValueList: 1
//	          values), 7 bytes

// Protobuf wire types.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

var errProto = errors.New("OTLP protobuf: malformed message")

// pbField is one decoded field: its number, wire type and value (the varint
// or fixed value in num, the payload of a length-delimited field in buf).
type pbField struct {
	n    int
	wire int
	num  uint64
	buf  []byte
}

// pbFields decodes the top level fields of one message.
func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	for len(b) > 0 {
		key, k := binary.Uvarint(b)
		if k <= 0 {
			return nil, errProto
		}
		b = b[k:]
		f := pbField{n: int(key >> 3), wire: int(key & 7)}
		if f.n <= 0 {
			return nil, errProto
		}
		switch f.wire {
		case wireVarint:
			v, k := binary.Uvarint(b)
			if k <= 0 {
				return nil, errProto
			}
			f.num, b = v, b[k:]
		case wireFixed64:
			if len(b) < 8 {
				return nil, errProto
			}
			f.num, b = binary.LittleEndian.Uint64(b), b[8:]
		case wireFixed32:
			if len(b) < 4 {
				return nil, errProto
			}
			f.num, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		case wireBytes:
			l, k := binary.Uvarint(b)
			if k <= 0 || l > uint64(len(b)-k) {
				return nil, errProto
			}
			f.buf, b = b[k:k+int(l)], b[k+int(l):]
		default:
			return nil, errProto
		}
		out = append(out, f)
	}
	return out, nil
}

// decodeProtoRequest reads every span of an ExportTraceServiceRequest.
func decodeProtoRequest(data []byte) ([]span, error) {
	top, err := pbFields(data)
	if err != nil {
		return nil, err
	}
	var out []span
	for _, rsf := range top {
		if rsf.n != 1 || rsf.wire != wireBytes {
			continue
		}
		rs, err := pbFields(rsf.buf)
		if err != nil {
			return nil, err
		}
		service := ""
		var resource map[string]string
		for _, f := range rs {
			if f.n == 1 && f.wire == wireBytes {
				res, err := pbFields(f.buf)
				if err != nil {
					return nil, err
				}
				attrs := map[string]string{}
				for _, a := range res {
					if a.n == 1 && a.wire == wireBytes {
						pbKeyValue(a.buf, attrs, 0)
					}
				}
				service = attrs["service.name"]
				resource = attrs
			}
		}
		for _, f := range rs {
			if f.n != 2 || f.wire != wireBytes {
				continue
			}
			ss, err := pbFields(f.buf)
			if err != nil {
				return nil, err
			}
			scope, scopeVersion := "", ""
			for _, sf := range ss {
				if sf.n == 1 && sf.wire == wireBytes { // InstrumentationScope: 1 name, 2 version
					if sc, err := pbFields(sf.buf); err == nil {
						for _, x := range sc {
							switch {
							case x.n == 1 && x.wire == wireBytes:
								scope = string(x.buf)
							case x.n == 2 && x.wire == wireBytes:
								scopeVersion = string(x.buf)
							}
						}
					}
				}
			}
			for _, sf := range ss {
				if sf.n != 2 || sf.wire != wireBytes {
					continue
				}
				sp, err := pbSpan(sf.buf)
				if err != nil {
					return nil, err
				}
				sp.service = service
				sp.resource = resource
				sp.scope, sp.scopeVersion = scope, scopeVersion
				out = append(out, sp)
			}
		}
	}
	return out, nil
}

func pbSpan(b []byte) (span, error) {
	fs, err := pbFields(b)
	if err != nil {
		return span{}, err
	}
	sp := span{attrs: map[string]string{}}
	for _, f := range fs {
		switch {
		case f.n == 1 && f.wire == wireBytes:
			sp.traceID = hex.EncodeToString(f.buf)
		case f.n == 2 && f.wire == wireBytes:
			sp.spanID = hex.EncodeToString(f.buf)
		case f.n == 4 && f.wire == wireBytes:
			sp.parentID = hex.EncodeToString(f.buf)
		case f.n == 5 && f.wire == wireBytes:
			sp.name = string(f.buf)
		case f.n == 6 && f.wire == wireVarint:
			sp.kind = int(f.num)
		case f.n == 7 && f.wire == wireFixed64:
			sp.start = nanos(f.num)
		case f.n == 8 && f.wire == wireFixed64:
			sp.end = nanos(f.num)
		case f.n == 9 && f.wire == wireBytes:
			pbKeyValue(f.buf, sp.attrs, 0)
		case f.n == 15 && f.wire == wireBytes:
			st, err := pbFields(f.buf)
			if err != nil {
				return span{}, err
			}
			for _, s := range st {
				switch {
				case s.n == 2 && s.wire == wireBytes:
					sp.statusMessage = string(s.buf)
				case s.n == 3 && s.wire == wireVarint:
					sp.statusCode = int(s.num)
				}
			}
		}
	}
	return sp, nil
}

func nanos(n uint64) time.Time {
	if n == 0 || n > math.MaxInt64 {
		return time.Time{}
	}
	t := time.Unix(0, int64(n)).UTC()
	if t.Year() < 2000 || t.Year() > 2200 {
		return time.Time{}
	}
	return t
}

// pbKeyValue decodes one KeyValue into attrs. A malformed one is skipped.
func pbKeyValue(b []byte, attrs map[string]string, depth int) {
	if len(attrs) >= 256 {
		return
	}
	fs, err := pbFields(b)
	if err != nil {
		return
	}
	key, val := "", ""
	for _, f := range fs {
		switch {
		case f.n == 1 && f.wire == wireBytes:
			key = string(f.buf)
		case f.n == 2 && f.wire == wireBytes:
			val = pbAnyValue(f.buf, depth)
		}
	}
	if key != "" {
		attrs[key] = val
	}
}

// pbAnyValue renders an AnyValue as text: scalars as they are, arrays and
// key-value lists as JSON-like text.
func pbAnyValue(b []byte, depth int) string {
	if depth > 4 {
		return ""
	}
	fs, err := pbFields(b)
	if err != nil {
		return ""
	}
	for _, f := range fs {
		switch {
		case f.n == 1 && f.wire == wireBytes:
			return string(f.buf)
		case f.n == 2 && f.wire == wireVarint:
			return strconv.FormatBool(f.num != 0)
		case f.n == 3 && f.wire == wireVarint:
			return strconv.FormatInt(int64(f.num), 10)
		case f.n == 4 && f.wire == wireFixed64:
			return strconv.FormatFloat(math.Float64frombits(f.num), 'g', -1, 64)
		case f.n == 5 && f.wire == wireBytes:
			vals, err := pbFields(f.buf)
			if err != nil {
				return ""
			}
			parts := []string{}
			for _, v := range vals {
				if v.n == 1 && v.wire == wireBytes {
					parts = append(parts, strconv.Quote(pbAnyValue(v.buf, depth+1)))
				}
			}
			return "[" + strings.Join(parts, ",") + "]"
		case f.n == 6 && f.wire == wireBytes:
			vals, err := pbFields(f.buf)
			if err != nil {
				return ""
			}
			inner := map[string]string{}
			var order []string
			for _, v := range vals {
				if v.n == 1 && v.wire == wireBytes {
					before := len(inner)
					pbKeyValue(v.buf, inner, depth+1)
					if len(inner) > before {
						for k := range inner {
							if !contains(order, k) {
								order = append(order, k)
							}
						}
					}
				}
			}
			parts := make([]string, 0, len(order))
			for _, k := range order {
				parts = append(parts, strconv.Quote(k)+":"+strconv.Quote(inner[k]))
			}
			return "{" + strings.Join(parts, ",") + "}"
		case f.n == 7 && f.wire == wireBytes:
			return hex.EncodeToString(f.buf)
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
