// Package telemetry is Heimdall's own OpenTelemetry tracing: spans for the
// server, the worker and the gateway calls they make, sent to an OTLP
// collector over HTTP with JSON bodies (lane Z14, INFRA_I4 item 12).
//
// It needs no module beyond the standard library and fasthttp: the OTel Go
// SDK is not vendored, and the wire format (OTLP/HTTP JSON, W3C
// traceparent) is small and stable. Everything is off until the standard
// OTEL_* environment variables name an endpoint (Setup); with tracing off,
// Start returns a span that records nothing and costs no allocation beyond
// the span itself.
//
// Rules: span attributes may carry ids (org, experiment, slot, run, lease);
// metric labels never do. Prompt, answer and tool content never goes into
// platform telemetry.
package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TraceID is a W3C trace id.
type TraceID [16]byte

// SpanID is a W3C span id.
type SpanID [8]byte

// IsZero reports an all-zero (invalid) id.
func (t TraceID) IsZero() bool { return t == TraceID{} }

// IsZero reports an all-zero (invalid) id.
func (s SpanID) IsZero() bool { return s == SpanID{} }

func (t TraceID) String() string { return hex.EncodeToString(t[:]) }
func (s SpanID) String() string  { return hex.EncodeToString(s[:]) }

// SpanContext is what crosses a process boundary: the trace, the span and
// whether the trace is sampled.
type SpanContext struct {
	TraceID TraceID
	SpanID  SpanID
	Sampled bool
}

// Valid reports both ids set.
func (sc SpanContext) Valid() bool { return !sc.TraceID.IsZero() && !sc.SpanID.IsZero() }

// Traceparent is the W3C header value, or "" for an invalid context.
func (sc SpanContext) Traceparent() string {
	if !sc.Valid() {
		return ""
	}
	flags := "00"
	if sc.Sampled {
		flags = "01"
	}
	return "00-" + sc.TraceID.String() + "-" + sc.SpanID.String() + "-" + flags
}

// ParseTraceparent reads a W3C traceparent header (version 00, or a later
// version read as 00 as the spec asks).
func ParseTraceparent(s string) (SpanContext, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 55 || s[2] != '-' || s[35] != '-' || s[52] != '-' {
		return SpanContext{}, false
	}
	ver := s[0:2]
	if ver == "ff" || (ver == "00" && len(s) != 55) || (len(s) > 55 && s[55] != '-') {
		return SpanContext{}, false
	}
	var sc SpanContext
	if _, err := hex.Decode(sc.TraceID[:], []byte(s[3:35])); err != nil {
		return SpanContext{}, false
	}
	if _, err := hex.Decode(sc.SpanID[:], []byte(s[36:52])); err != nil {
		return SpanContext{}, false
	}
	var flags [1]byte
	if _, err := hex.Decode(flags[:], []byte(s[53:55])); err != nil {
		return SpanContext{}, false
	}
	if !sc.Valid() || strings.ToLower(s[3:52]) != s[3:52] {
		return SpanContext{}, false
	}
	sc.Sampled = flags[0]&1 == 1
	return sc, true
}

// TraceIDFrom derives a trace id from a seed, so every process that knows
// the seed (an experiment id) puts its spans on one trace without sharing
// state.
func TraceIDFrom(seed string) TraceID {
	sum := sha256.Sum256([]byte("heimdall/trace/" + seed))
	var t TraceID
	copy(t[:], sum[:16])
	return t
}

func newTraceID() TraceID {
	var t TraceID
	for t.IsZero() {
		a, b := rand.Uint64(), rand.Uint64()
		for i := range 8 {
			t[i], t[8+i] = byte(a>>(8*i)), byte(b>>(8*i))
		}
	}
	return t
}

func newSpanID() SpanID {
	var s SpanID
	for s.IsZero() {
		a := rand.Uint64()
		for i := range 8 {
			s[i] = byte(a >> (8 * i))
		}
	}
	return s
}

// Kind is the OTLP span kind.
type Kind int

// Span kinds (OTLP SpanKind numbers).
const (
	KindInternal Kind = 1
	KindServer   Kind = 2
	KindClient   Kind = 3
)

// Attr is one span attribute. Value is a string, bool, int, int64 or
// float64; anything else is sent as its fmt string.
type Attr struct {
	Key   string
	Value any
}

// String, Int and Bool build attributes.
func String(k, v string) Attr        { return Attr{k, v} }
func Int(k string, v int) Attr       { return Attr{k, int64(v)} }
func Bool(k string, v bool) Attr     { return Attr{k, v} }
func Float(k string, v float64) Attr { return Attr{k, v} }

// Status codes (OTLP).
const (
	statusUnset = 0
	statusOK    = 1
	statusError = 2
)

// SpanData is a finished span as the exporter sends it.
type SpanData struct {
	Name          string
	Kind          Kind
	Context       SpanContext
	Parent        SpanID
	Start, End    time.Time
	Attrs         []Attr
	StatusCode    int
	StatusMessage string
}

// Span is one timed operation. A nil *Span is valid and records nothing, so
// callers never check.
type Span struct {
	mu     sync.Mutex
	t      *Tracer
	data   SpanData
	record bool
	ended  bool
}

// Context is the span's propagation context; zero for a nil span.
func (s *Span) Context() SpanContext {
	if s == nil {
		return SpanContext{}
	}
	return s.data.Context
}

// Traceparent is the span's W3C header value (sampled flag included); ""
// for a nil span (tracing off), so a lease never carries a header no one
// reads.
func (s *Span) Traceparent() string {
	if s == nil {
		return ""
	}
	return s.data.Context.Traceparent()
}

// Recording reports a span that will be exported.
func (s *Span) Recording() bool { return s != nil && s.record }

// SetAttrs adds attributes.
func (s *Span) SetAttrs(attrs ...Attr) {
	if !s.Recording() {
		return
	}
	s.mu.Lock()
	s.data.Attrs = append(s.data.Attrs, attrs...)
	s.mu.Unlock()
}

// SetError marks the span failed with a short reason (never content).
func (s *Span) SetError(msg string) {
	if !s.Recording() {
		return
	}
	s.mu.Lock()
	s.data.StatusCode, s.data.StatusMessage = statusError, clip(msg, 256)
	s.mu.Unlock()
}

// SetOK marks the span succeeded.
func (s *Span) SetOK() {
	if !s.Recording() {
		return
	}
	s.mu.Lock()
	if s.data.StatusCode == statusUnset {
		s.data.StatusCode = statusOK
	}
	s.mu.Unlock()
}

// End finishes the span now. Later calls do nothing.
func (s *Span) End() { s.EndAt(time.Now()) }

// EndAt finishes the span at t.
func (s *Span) EndAt(t time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.data.End = t
	d := s.data
	d.Attrs = append([]Attr(nil), s.data.Attrs...)
	s.mu.Unlock()
	if s.record && s.t != nil && s.t.exp != nil {
		s.t.exp.enqueue(d)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type ctxKey struct{}
type remoteKey struct{}

// ContextWithSpan returns ctx carrying s as the current span.
func ContextWithSpan(ctx context.Context, s *Span) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, s)
}

// ContextWithRemote returns ctx carrying a parent from another process (a
// traceparent header, a lease).
func ContextWithRemote(ctx context.Context, sc SpanContext) context.Context {
	if !sc.Valid() {
		return ctx
	}
	return context.WithValue(ctx, remoteKey{}, sc)
}

// SpanFromContext is ctx's current span, or nil.
func SpanFromContext(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(ctxKey{}).(*Span)
	return s
}

// parentOf is ctx's parent context: the current span, else a remote one.
func parentOf(ctx context.Context) SpanContext {
	if ctx == nil {
		return SpanContext{}
	}
	if s := SpanFromContext(ctx); s != nil {
		return s.Context()
	}
	sc, _ := ctx.Value(remoteKey{}).(SpanContext)
	return sc
}

// Inject sets the traceparent header from ctx's current span when tracing
// is on. An unsampled span still sends its header (flag 00), so the next
// process keeps the same decision.
func Inject(ctx context.Context, h http.Header) {
	if tp := SpanFromContext(ctx).Traceparent(); tp != "" {
		h.Set("traceparent", tp)
	}
}

// Option changes how Start makes a span.
type Option func(*startCfg)

type startCfg struct {
	kind    Kind
	attrs   []Attr
	start   time.Time
	parent  *SpanContext
	traceID TraceID
}

// WithKind sets the span kind (default internal).
func WithKind(k Kind) Option { return func(c *startCfg) { c.kind = k } }

// WithAttrs sets attributes at the start.
func WithAttrs(a ...Attr) Option { return func(c *startCfg) { c.attrs = append(c.attrs, a...) } }

// WithStart backdates the span.
func WithStart(t time.Time) Option { return func(c *startCfg) { c.start = t } }

// WithParent uses sc as the parent instead of ctx's (an invalid sc makes a
// root span).
func WithParent(sc SpanContext) Option { return func(c *startCfg) { c.parent = &sc } }

// WithTraceID puts a root span on a known trace (TraceIDFrom).
func WithTraceID(t TraceID) Option { return func(c *startCfg) { c.traceID = t } }

// Start begins a span under ctx's current span (or remote parent) on the
// process tracer and returns ctx carrying it.
func Start(ctx context.Context, name string, opts ...Option) (context.Context, *Span) {
	s := Default().StartSpan(ctx, name, opts...)
	if ctx == nil {
		ctx = context.Background()
	}
	return ContextWithSpan(ctx, s), s
}
