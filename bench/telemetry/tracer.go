package telemetry

import (
	"context"
	"encoding/binary"
	"sync/atomic"
	"time"
)

// Tracer makes spans and hands finished ones to its exporter. The zero
// Tracer (and a nil one) records nothing.
type Tracer struct {
	exp *exporter
	// ratio is the share of new root traces sampled (0..1); children follow
	// their parent's sampled flag (parent-based sampling).
	ratio float64
}

var global atomic.Pointer[Tracer]

var noop = &Tracer{}

// Default is the process tracer: the one Setup installed, else one that
// records nothing.
func Default() *Tracer {
	if t := global.Load(); t != nil {
		return t
	}
	return noop
}

// SetDefault installs t as the process tracer (nil: none). Tests use it with
// NewTracer.
func SetDefault(t *Tracer) { global.Store(t) }

// Enabled reports a process tracer that exports.
func Enabled() bool { return Default().exp != nil }

// StartSpan begins a span. Without an exporter the span records nothing
// and gets no ids, so it propagates nothing either.
func (t *Tracer) StartSpan(ctx context.Context, name string, opts ...Option) *Span {
	if t == nil || t.exp == nil {
		return nil
	}
	cfg := startCfg{kind: KindInternal}
	for _, o := range opts {
		o(&cfg)
	}
	parent := parentOf(ctx)
	if cfg.parent != nil {
		parent = *cfg.parent
	}
	s := &Span{t: t}
	s.data.Name = name
	s.data.Kind = cfg.kind
	s.data.Attrs = cfg.attrs
	s.data.Start = cfg.start
	if s.data.Start.IsZero() {
		s.data.Start = time.Now()
	}
	s.data.Context.SpanID = newSpanID()
	if parent.Valid() {
		s.data.Context.TraceID = parent.TraceID
		s.data.Parent = parent.SpanID
		s.data.Context.Sampled = parent.Sampled
	} else {
		s.data.Context.TraceID = cfg.traceID
		if s.data.Context.TraceID.IsZero() {
			s.data.Context.TraceID = newTraceID()
		}
		s.data.Context.Sampled = t.sampleRoot(s.data.Context.TraceID)
	}
	s.record = s.data.Context.Sampled
	return s
}

// sampleRoot samples by trace id (the W3C random part), so every process
// decides the same way for a derived trace id.
func (t *Tracer) sampleRoot(id TraceID) bool {
	switch {
	case t.ratio >= 1:
		return true
	case t.ratio <= 0:
		return false
	}
	v := binary.BigEndian.Uint64(id[8:]) >> 11
	return float64(v) < t.ratio*float64(uint64(1)<<53)
}
