package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Queue and batch bounds: a slow or absent collector drops spans (counted)
// rather than holding memory or slowing a request.
const (
	queueSize    = 4096
	batchSize    = 512
	flushEvery   = 5 * time.Second
	sendTimeout  = 10 * time.Second
	maxErrorBody = 4 << 10
)

// exporter batches finished spans and posts them as OTLP/HTTP JSON.
type exporter struct {
	url      string
	headers  map[string]string
	client   *http.Client
	resource []Attr
	warn     func(string)

	ch    chan SpanData
	flush chan chan struct{}
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once

	sent, dropped, failed atomic.Uint64
	lastWarn              atomic.Int64
}

func newExporter(cfg Config) *exporter {
	e := &exporter{
		url: cfg.TracesURL, headers: cfg.Headers, client: cfg.HTTP, resource: cfg.Resource, warn: cfg.Warn,
		ch: make(chan SpanData, queueSize), flush: make(chan chan struct{}), stop: make(chan struct{}), done: make(chan struct{}),
	}
	if e.client == nil {
		e.client = &http.Client{Timeout: sendTimeout}
	}
	if e.warn == nil {
		e.warn = func(string) {}
	}
	go e.run(cfg.FlushEvery)
	return e
}

func (e *exporter) enqueue(d SpanData) {
	select {
	case e.ch <- d:
	default:
		e.dropped.Add(1)
	}
}

func (e *exporter) run(every time.Duration) {
	defer close(e.done)
	if every <= 0 {
		every = flushEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var buf []SpanData
	send := func() {
		if len(buf) > 0 {
			e.send(buf)
			buf = nil
		}
	}
	for {
		select {
		case d := <-e.ch:
			buf = append(buf, d)
			if len(buf) >= batchSize {
				send()
			}
		case <-t.C:
			send()
		case reply := <-e.flush:
			buf = e.drain(buf)
			send()
			close(reply)
		case <-e.stop:
			buf = e.drain(buf)
			send()
			return
		}
	}
}

func (e *exporter) drain(buf []SpanData) []SpanData {
	for {
		select {
		case d := <-e.ch:
			buf = append(buf, d)
		default:
			return buf
		}
	}
}

func (e *exporter) send(spans []SpanData) {
	for len(spans) > 0 {
		n := min(len(spans), batchSize)
		body, err := json.Marshal(encodeTraces(e.resource, spans[:n]))
		if err == nil {
			err = post(e.client, e.url, e.headers, body)
		}
		if err != nil {
			e.failed.Add(uint64(n))
			e.warnOnce("telemetry: span export failed: " + err.Error())
		} else {
			e.sent.Add(uint64(n))
		}
		spans = spans[n:]
	}
}

// warnOnce logs at most once a minute, so a missing collector does not
// flood the log.
func (e *exporter) warnOnce(msg string) {
	now := time.Now().Unix()
	if last := e.lastWarn.Load(); now-last < 60 || !e.lastWarn.CompareAndSwap(last, now) {
		return
	}
	e.warn(msg)
}

// Flush sends every queued span and waits, bounded by ctx.
func (e *exporter) Flush(ctx context.Context) {
	reply := make(chan struct{})
	select {
	case e.flush <- reply:
	case <-e.done:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-reply:
	case <-ctx.Done():
	}
}

func (e *exporter) shutdown(ctx context.Context) {
	e.once.Do(func() { close(e.stop) })
	select {
	case <-e.done:
	case <-ctx.Done():
	}
}

func post(client *http.Client, url string, headers map[string]string, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %d: %s", url, resp.StatusCode, bytes.TrimSpace(raw))
	}
	return nil
}

// Stats are the exporter's counters since start.
type Stats struct{ Sent, Dropped, Failed uint64 }

// Stats reads the process tracer's export counters (zero with tracing off).
func (t *Tracer) Stats() Stats {
	if t == nil || t.exp == nil {
		return Stats{}
	}
	return Stats{Sent: t.exp.sent.Load(), Dropped: t.exp.dropped.Load(), Failed: t.exp.failed.Load()}
}

// Flush sends every finished span now (tests, shutdown).
func (t *Tracer) Flush(ctx context.Context) {
	if t != nil && t.exp != nil {
		t.exp.Flush(ctx)
	}
}

// Shutdown flushes and stops the exporter.
func (t *Tracer) Shutdown(ctx context.Context) {
	if t != nil && t.exp != nil {
		t.exp.shutdown(ctx)
	}
}

// --- OTLP JSON (opentelemetry-proto, JSON mapping: ids are hex, 64-bit
// integers are strings) ---

type kv struct {
	Key   string `json:"key"`
	Value anyVal `json:"value"`
}

type anyVal struct {
	String *string  `json:"stringValue,omitempty"`
	Int    *string  `json:"intValue,omitempty"`
	Double *float64 `json:"doubleValue,omitempty"`
	Bool   *bool    `json:"boolValue,omitempty"`
}

func encodeAttrs(attrs []Attr) []kv {
	out := make([]kv, 0, len(attrs))
	for _, a := range attrs {
		var v anyVal
		switch x := a.Value.(type) {
		case string:
			v.String = &x
		case bool:
			v.Bool = &x
		case int:
			s := strconv.Itoa(x)
			v.Int = &s
		case int64:
			s := strconv.FormatInt(x, 10)
			v.Int = &s
		case float64:
			v.Double = &x
		default:
			s := fmt.Sprint(x)
			v.String = &s
		}
		out = append(out, kv{Key: a.Key, Value: v})
	}
	return out
}

type otlpResource struct {
	Attributes []kv `json:"attributes"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type otlpSpan struct {
	TraceID      string     `json:"traceId"`
	SpanID       string     `json:"spanId"`
	ParentSpanID string     `json:"parentSpanId,omitempty"`
	Name         string     `json:"name"`
	Kind         int        `json:"kind"`
	Start        string     `json:"startTimeUnixNano"`
	End          string     `json:"endTimeUnixNano"`
	Attributes   []kv       `json:"attributes,omitempty"`
	Status       otlpStatus `json:"status"`
}

type traceRequest struct {
	ResourceSpans []resourceSpans `json:"resourceSpans"`
}

type resourceSpans struct {
	Resource   otlpResource `json:"resource"`
	ScopeSpans []scopeSpans `json:"scopeSpans"`
}

type scopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

// ScopeName is the instrumentation scope of every span and metric.
const ScopeName = "github.com/maximhq/bifrost/bench/telemetry"

func nanos(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func encodeTraces(resource []Attr, spans []SpanData) traceRequest {
	out := make([]otlpSpan, 0, len(spans))
	for _, s := range spans {
		o := otlpSpan{
			TraceID: s.Context.TraceID.String(), SpanID: s.Context.SpanID.String(), Name: s.Name, Kind: int(s.Kind),
			Start: nanos(s.Start), End: nanos(s.End), Attributes: encodeAttrs(s.Attrs),
			Status: otlpStatus{Code: s.StatusCode, Message: s.StatusMessage},
		}
		if !s.Parent.IsZero() {
			o.ParentSpanID = s.Parent.String()
		}
		out = append(out, o)
	}
	return traceRequest{ResourceSpans: []resourceSpans{{
		Resource:   otlpResource{Attributes: encodeAttrs(resource)},
		ScopeSpans: []scopeSpans{{Scope: otlpScope{Name: ScopeName}, Spans: out}},
	}}}
}
