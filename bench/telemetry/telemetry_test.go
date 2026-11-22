package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/selfmetrics"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

type collector struct {
	mu     sync.Mutex
	bodies []map[string]any
	paths  []string
	auth   []string
}

func (c *collector) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("collector got non-JSON: %v", err)
		}
		c.mu.Lock()
		c.bodies = append(c.bodies, m)
		c.paths = append(c.paths, r.URL.Path)
		c.auth = append(c.auth, r.Header.Get("Authorization"))
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// spans flattens every received span.
func (c *collector) spans() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, b := range c.bodies {
		rs, _ := b["resourceSpans"].([]any)
		for _, r := range rs {
			for _, ss := range r.(map[string]any)["scopeSpans"].([]any) {
				for _, s := range ss.(map[string]any)["spans"].([]any) {
					out = append(out, s.(map[string]any))
				}
			}
		}
	}
	return out
}

func attr(span map[string]any, key string) any {
	for _, a := range span["attributes"].([]any) {
		m := a.(map[string]any)
		if m["key"] == key {
			for _, v := range m["value"].(map[string]any) {
				return v
			}
		}
	}
	return nil
}

func TestTraceparentRoundTrip(t *testing.T) {
	sc := SpanContext{TraceID: TraceIDFrom("exp-1"), SpanID: newSpanID(), Sampled: true}
	got, ok := ParseTraceparent(sc.Traceparent())
	if !ok || got != sc {
		t.Fatalf("round trip: %v %v", got, ok)
	}
	for _, bad := range []string{"", "00-0000-00", "00-00000000000000000000000000000000-0000000000000001-01",
		"00-0af7651916cd43dd8448eb211c80319c-0000000000000000-01", "ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"00-0AF7651916CD43DD8448EB211C80319C-b7ad6b7169203331-01", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01-x"} {
		if _, ok := ParseTraceparent(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if sc, ok := ParseTraceparent("00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00"); !ok || sc.Sampled {
		t.Fatalf("unsampled header: %v %v", sc, ok)
	}
	if TraceIDFrom("a") != TraceIDFrom("a") || TraceIDFrom("a") == TraceIDFrom("b") {
		t.Fatal("TraceIDFrom is not a stable function of its seed")
	}
}

func TestOffRecordsNothing(t *testing.T) {
	SetDefault(nil)
	ctx, s := Start(context.Background(), "x")
	if s != nil || s.Traceparent() != "" || Enabled() {
		t.Fatal("tracing off must give a nil span")
	}
	s.SetAttrs(String("a", "b"))
	s.SetError("e")
	s.End()
	h := http.Header{}
	Inject(ctx, h)
	if h.Get("traceparent") != "" {
		t.Fatal("injected with tracing off")
	}
}

func TestExportTreeAndHeaders(t *testing.T) {
	var c collector
	srv := c.server(t)
	cfg, warns := ConfigFromEnv("heimdall-test", "v1", func(k string) string {
		return map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": srv.URL + "/",
			"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=Bearer%20abc",
			"OTEL_RESOURCE_ATTRIBUTES":    "deployment.environment=test",
		}[k]
	})
	if len(warns) != 0 || cfg.TracesURL != srv.URL+"/v1/traces" || cfg.MetricsURL != "" {
		t.Fatalf("config: %+v %v", cfg, warns)
	}
	tr := NewTracer(cfg)
	SetDefault(tr)
	t.Cleanup(func() { SetDefault(nil) })

	ctx, root := Start(context.Background(), "experiment", WithTraceID(TraceIDFrom("exp-9")), WithAttrs(String("heimdall.experiment", "exp-9")))
	_, child := Start(ctx, "lease")
	child.SetError("lost: no heartbeat")
	child.End()
	root.SetOK()
	root.End()
	tr.Flush(context.Background())

	spans := c.spans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	if c.auth[0] != "Bearer abc" || c.paths[0] != "/v1/traces" {
		t.Fatalf("auth %q path %q", c.auth[0], c.paths[0])
	}
	lease, exp := spans[0], spans[1]
	if lease["name"] != "lease" || exp["name"] != "experiment" {
		t.Fatalf("names: %v %v", lease["name"], exp["name"])
	}
	if lease["traceId"] != TraceIDFrom("exp-9").String() || lease["traceId"] != exp["traceId"] {
		t.Fatal("child not on the derived trace")
	}
	if lease["parentSpanId"] != exp["spanId"] {
		t.Fatal("child parent is not the root")
	}
	if st := lease["status"].(map[string]any); st["code"] != float64(2) || st["message"] != "lost: no heartbeat" {
		t.Fatalf("status: %v", st)
	}
	if attr(exp, "heimdall.experiment") != "exp-9" {
		t.Fatal("attribute missing")
	}
	res := c.bodies[0]["resourceSpans"].([]any)[0].(map[string]any)["resource"].(map[string]any)
	if !strings.Contains(mustJSON(res), `"service.name","value":{"stringValue":"heimdall-test"}`) ||
		!strings.Contains(mustJSON(res), "deployment.environment") {
		t.Fatalf("resource: %s", mustJSON(res))
	}
	if st := tr.Stats(); st.Sent != 2 || st.Dropped != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestSamplingFollowsParent(t *testing.T) {
	var c collector
	srv := c.server(t)
	tr := NewTracer(Config{TracesURL: srv.URL + "/v1/traces", SampleRatio: 0})
	SetDefault(tr)
	t.Cleanup(func() { SetDefault(nil) })
	ctx, root := Start(context.Background(), "root")
	if root.Recording() || root.Traceparent() == "" || strings.HasSuffix(root.Traceparent(), "-01") {
		t.Fatalf("ratio 0 root: recording=%v tp=%q", root.Recording(), root.Traceparent())
	}
	h := http.Header{}
	Inject(ctx, h)
	if !strings.HasSuffix(h.Get("traceparent"), "-00") {
		t.Fatalf("unsampled decision not propagated: %q", h.Get("traceparent"))
	}
	sampled := SpanContext{TraceID: newTraceID(), SpanID: newSpanID(), Sampled: true}
	_, child := Start(ContextWithRemote(context.Background(), sampled), "child")
	if !child.Recording() {
		t.Fatal("a sampled parent must be followed")
	}
	child.End()
	root.End()
	tr.Flush(context.Background())
	if n := len(c.spans()); n != 1 {
		t.Fatalf("exported %d spans, want 1", n)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if cfg, _ := ConfigFromEnv("s", "", env(map[string]string{"OTEL_SDK_DISABLED": "true", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318"})); cfg.TracesURL != "" {
		t.Fatal("disabled SDK still exports")
	}
	cfg, warns := ConfigFromEnv("s", "", env(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://c:4318",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://t:4318/v1/traces",
		"OTEL_METRICS_EXPORTER":              "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":        "grpc",
		"OTEL_TRACES_SAMPLER":                "parentbased_traceidratio",
		"OTEL_TRACES_SAMPLER_ARG":            "0.25",
		"OTEL_METRIC_EXPORT_INTERVAL":        "15000",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS": "x-k=1",
		"OTEL_SERVICE_NAME":                  "custom",
	}))
	if cfg.TracesURL != "http://t:4318/v1/traces" || cfg.MetricsURL != "http://c:4318/v1/metrics" || cfg.SampleRatio != 0.25 ||
		cfg.MetricsEvery != 15*time.Second || cfg.MetricHeaders["x-k"] != "1" || len(warns) != 1 {
		t.Fatalf("cfg %+v warns %v", cfg, warns)
	}
	if !strings.Contains(mustJSON(encodeAttrs(cfg.Resource)), `"custom"`) {
		t.Fatal("OTEL_SERVICE_NAME not used")
	}
	if cfg, w := ConfigFromEnv("s", "", env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4318"})); cfg.TracesURL != "" || len(w) != 1 {
		t.Fatalf("bad URL accepted: %+v", cfg)
	}
	if cfg, _ := ConfigFromEnv("s", "", env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_TRACES_EXPORTER": "none"})); cfg.TracesURL != "" {
		t.Fatal("OTEL_TRACES_EXPORTER=none ignored")
	}
}

func TestMiddlewareJoinsCallerTrace(t *testing.T) {
	var c collector
	srv := c.server(t)
	tr := NewTracer(Config{TracesURL: srv.URL + "/v1/traces", SampleRatio: 1})
	SetDefault(tr)
	t.Cleanup(func() { SetDefault(nil) })

	var inner SpanContext
	h := Middleware(func(rc *fasthttp.RequestCtx) {
		inner = SpanFromContext(rc).Context()
		if string(rc.Path()) == "/api/bench/boom" {
			rc.SetStatusCode(500)
		}
	})
	ln := fasthttputil.NewInmemoryListener()
	go func() { _ = fasthttp.Serve(ln, h) }()
	t.Cleanup(func() { _ = ln.Close() })
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return ln.Dial() }}}

	parent := SpanContext{TraceID: newTraceID(), SpanID: newSpanID(), Sampled: true}
	req, _ := http.NewRequest(http.MethodPost, "http://heimdall/api/bench/worker/leases/l_1/heartbeat?worker_id=w", nil)
	req.Header.Set("traceparent", parent.Traceparent())
	if resp, err := client.Do(req); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if inner.TraceID != parent.TraceID {
		t.Fatal("handler span not on the caller's trace")
	}
	if resp, err := client.Get("http://heimdall/api/bench/boom"); err == nil {
		resp.Body.Close()
	}
	if resp, err := client.Get("http://heimdall/healthz"); err == nil {
		resp.Body.Close()
	}
	tr.Flush(context.Background())
	spans := c.spans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d (health probes must not be traced)", len(spans))
	}
	if spans[0]["parentSpanId"] != parent.SpanID.String() || spans[0]["name"] != "POST worker" || spans[0]["kind"] != float64(2) {
		t.Fatalf("server span: %v", spans[0])
	}
	if p := attr(spans[0], "url.path"); p != "/api/bench/worker/leases/l_1/heartbeat" {
		t.Fatalf("url.path = %v (no query string)", p)
	}
	if st := spans[1]["status"].(map[string]any); st["code"] != float64(2) {
		t.Fatalf("5xx not an error: %v", spans[1])
	}
}

func TestMetricsPush(t *testing.T) {
	var c collector
	srv := c.server(t)
	reg := selfmetrics.New()
	reg.Gauge("heimdall_workers", "w", func(context.Context) []selfmetrics.Sample {
		return []selfmetrics.Sample{{Labels: map[string]string{"state": "idle"}, Value: 2}}
	})
	cv := reg.CounterVec("heimdall_remote_leases_ended_total", "e", "reason")
	cv.Inc("lost")
	hv := reg.Histogram("heimdall_evaluation_start_seconds", "s", []float64{1, 10})
	hv.Observe(0.5)
	hv.Observe(50)
	reg.Observe("GET", "/api/bench/runs", 200, 20*time.Millisecond)
	p := StartMetricsPush(Config{MetricsURL: srv.URL + "/v1/metrics", MetricsEvery: time.Hour, Headers: map[string]string{"Authorization": "k"}}, reg)
	if err := p.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.Stop(context.Background())
	body := mustJSON(c.bodies[0])
	for _, want := range []string{`"description":"w","gauge"`, `"name":"heimdall_workers"`, `"isMonotonic":true`,
		`"bucketCounts":["1","0","1"]`, `"explicitBounds":[1,10]`, `heimdall_http_request_duration_seconds`, `"stringValue":"lost"`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body lacks %s:\n%s", want, body)
		}
	}
	if c.paths[0] != "/v1/metrics" || c.auth[0] != "k" {
		t.Fatalf("path %q auth %q", c.paths[0], c.auth[0])
	}
}
