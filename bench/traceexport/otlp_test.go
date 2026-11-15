package traceexport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUint64AndInt64JSON(t *testing.T) {
	b, err := json.Marshal(struct {
		U Uint64 `json:"u"`
		I Int64  `json:"i"`
	}{U: 1794612559430535264, I: -42})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"u":"1794612559430535264","i":"-42"}` {
		t.Fatalf("got %s", b)
	}
	cases := []struct {
		in   string
		want Uint64
		bad  bool
	}{
		{`"12"`, 12, false},
		{`12`, 12, false},
		{`null`, 0, false},
		{`"x"`, 0, true},
		{`-1`, 0, true},
	}
	for _, c := range cases {
		var u Uint64
		err := json.Unmarshal([]byte(c.in), &u)
		if (err != nil) != c.bad || (!c.bad && u != c.want) {
			t.Fatalf("Uint64 %s: got %d, %v", c.in, u, err)
		}
		var i Int64
		if err := json.Unmarshal([]byte(c.in), &i); !c.bad && err != nil {
			t.Fatalf("Int64 %s: %v", c.in, err)
		}
	}
}

func TestAnyValueString(t *testing.T) {
	cases := []struct {
		kv   KeyValue
		want string
	}{
		{Str("k", "v"), "v"},
		{Int("k", 7), "7"},
		{Bool("k", true), "true"},
		{Double("k", 0.5), "0.5"},
		{Strs("k", []string{"a", "b"}), `{"arrayValue":{"values":[{"stringValue":"a"},{"stringValue":"b"}]}}`},
		{KeyValue{Key: "k"}, ""},
	}
	for _, c := range cases {
		if got := c.kv.Value.String(); got != c.want {
			t.Fatalf("%+v: got %q, want %q", c.kv, got, c.want)
		}
	}
	if _, ok := Attr([]KeyValue{Str("a", "1")}, "b"); ok {
		t.Fatal("Attr found a missing key")
	}
}

func TestTracesDataRoundTrip(t *testing.T) {
	td, err := Build(syntheticInput(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	var back TracesData
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	b2, err := Marshal(&back)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(b2) {
		t.Fatal("OTLP/JSON does not round-trip through the types")
	}
}

func TestValidate(t *testing.T) {
	good := func() *TracesData {
		return &TracesData{ResourceSpans: []ResourceSpans{{ScopeSpans: []ScopeSpans{{Spans: []Span{
			{TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), Name: "root", StartTimeUnixNano: 1, EndTimeUnixNano: 2},
			{TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("c", 16), ParentSpanID: strings.Repeat("b", 16), Name: "child", StartTimeUnixNano: 1, EndTimeUnixNano: 1},
		}}}}}}
	}
	if err := Validate(good()); err != nil {
		t.Fatalf("valid trace: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Span, *Span)
		want   string
	}{
		{"short trace id", func(r, _ *Span) { r.TraceID = "abc" }, "trace id"},
		{"upper hex", func(r, _ *Span) { r.SpanID = strings.Repeat("B", 16) }, "span id"},
		{"zero span id", func(r, _ *Span) { r.SpanID = strings.Repeat("0", 16) }, "span id"},
		{"dangling parent", func(_, c *Span) { c.ParentSpanID = strings.Repeat("d", 16) }, "parent"},
		{"end before start", func(_, c *Span) { c.EndTimeUnixNano = 0 }, "ends before"},
		{"no start", func(r, _ *Span) { r.StartTimeUnixNano = 0 }, "no start"},
		{"no name", func(r, _ *Span) { r.Name = "" }, "no name"},
		{"duplicate id", func(r, c *Span) { c.SpanID = r.SpanID; c.ParentSpanID = "" }, "repeats"},
		{"self parent", func(_, c *Span) { c.ParentSpanID = c.SpanID }, "own parent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			td := good()
			spans := td.ResourceSpans[0].ScopeSpans[0].Spans
			c.mutate(&spans[0], &spans[1])
			err := Validate(td)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
	if Validate(nil) == nil {
		t.Fatal("nil document passed")
	}
}
