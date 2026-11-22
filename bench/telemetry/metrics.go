package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/selfmetrics"
)

// MetricsSource is what the push reads each interval: a selfmetrics
// registry (selfmetrics.Default in the server and worker).
type MetricsSource interface {
	Snapshot(ctx context.Context) selfmetrics.Snapshot
}

// MetricsPush sends the registry's metrics as OTLP/HTTP JSON every
// interval: gauges as gauges, counters as cumulative monotonic sums, and
// histograms with explicit bounds. Prometheus scraping /metrics stays the
// main path; the push is for collectors that only take OTLP.
type MetricsPush struct {
	cfg    Config
	src    MetricsSource
	client *http.Client
	start  time.Time
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

// StartMetricsPush starts pushing to cfg.MetricsURL.
func StartMetricsPush(cfg Config, src MetricsSource) *MetricsPush {
	p := &MetricsPush{cfg: cfg, src: src, client: cfg.HTTP, start: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	if p.client == nil {
		p.client = &http.Client{Timeout: sendTimeout}
	}
	every := cfg.MetricsEvery
	if every <= 0 {
		every = 60 * time.Second
	}
	go func() {
		defer close(p.done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = p.Push(context.Background())
			case <-p.stop:
				_ = p.Push(context.Background())
				return
			}
		}
	}()
	return p
}

// Stop pushes once more and stops.
func (p *MetricsPush) Stop(ctx context.Context) {
	p.once.Do(func() { close(p.stop) })
	select {
	case <-p.done:
	case <-ctx.Done():
	}
}

// Push sends one snapshot now.
func (p *MetricsPush) Push(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	snap := p.src.Snapshot(cctx)
	cancel()
	body, err := json.Marshal(EncodeMetrics(p.cfg.Resource, snap, p.start, time.Now()))
	if err != nil {
		return err
	}
	headers := map[string]string{}
	for k, v := range p.cfg.Headers {
		headers[k] = v
	}
	for k, v := range p.cfg.MetricHeaders {
		headers[k] = v
	}
	err = post(p.client, p.cfg.MetricsURL, headers, body)
	if err != nil && p.cfg.Warn != nil {
		p.cfg.Warn("telemetry: metric push failed: " + err.Error())
	}
	return err
}

type metricsRequest struct {
	ResourceMetrics []resourceMetrics `json:"resourceMetrics"`
}

type resourceMetrics struct {
	Resource     otlpResource   `json:"resource"`
	ScopeMetrics []scopeMetrics `json:"scopeMetrics"`
}

type scopeMetrics struct {
	Scope   otlpScope    `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Unit        string         `json:"unit,omitempty"`
	Gauge       *otlpGauge     `json:"gauge,omitempty"`
	Sum         *otlpSum       `json:"sum,omitempty"`
	Histogram   *otlpHistogram `json:"histogram,omitempty"`
}

type numberPoint struct {
	Attributes []kv    `json:"attributes,omitempty"`
	Start      string  `json:"startTimeUnixNano,omitempty"`
	Time       string  `json:"timeUnixNano"`
	Value      float64 `json:"asDouble"`
}

type otlpGauge struct {
	DataPoints []numberPoint `json:"dataPoints"`
}

type otlpSum struct {
	DataPoints  []numberPoint `json:"dataPoints"`
	Temporality int           `json:"aggregationTemporality"` // 2: cumulative
	Monotonic   bool          `json:"isMonotonic"`
}

type histPoint struct {
	Attributes []kv      `json:"attributes,omitempty"`
	Start      string    `json:"startTimeUnixNano"`
	Time       string    `json:"timeUnixNano"`
	Count      string    `json:"count"`
	Sum        float64   `json:"sum"`
	Buckets    []string  `json:"bucketCounts"`
	Bounds     []float64 `json:"explicitBounds"`
}

type otlpHistogram struct {
	DataPoints  []histPoint `json:"dataPoints"`
	Temporality int         `json:"aggregationTemporality"`
}

func labelAttrs(m map[string]string) []kv {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make([]Attr, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, String(k, m[k]))
	}
	return encodeAttrs(attrs)
}

// EncodeMetrics is a snapshot as an OTLP metrics request. Cumulative
// points start at start (the process start of the push).
func EncodeMetrics(resource []Attr, snap selfmetrics.Snapshot, start, now time.Time) any {
	var ms []otlpMetric
	st, ts := nanos(start), nanos(now)
	for _, f := range snap.Families {
		m := otlpMetric{Name: f.Name, Description: f.Help}
		var pts []numberPoint
		for _, s := range f.Samples {
			p := numberPoint{Attributes: labelAttrs(s.Labels), Time: ts, Value: s.Value}
			if f.Type == "counter" {
				p.Start = st
			}
			pts = append(pts, p)
		}
		if pts == nil {
			pts = []numberPoint{}
		}
		if f.Type == "counter" {
			m.Sum = &otlpSum{DataPoints: pts, Temporality: 2, Monotonic: true}
		} else {
			m.Gauge = &otlpGauge{DataPoints: pts}
		}
		ms = append(ms, m)
	}
	for _, h := range snap.Histograms {
		m := otlpMetric{Name: h.Name, Description: h.Help, Unit: "s"}
		hist := &otlpHistogram{DataPoints: []histPoint{}, Temporality: 2}
		for _, p := range h.Points {
			b := make([]string, len(p.Counts))
			for i, c := range p.Counts {
				b[i] = strconv.FormatUint(c, 10)
			}
			hist.DataPoints = append(hist.DataPoints, histPoint{Attributes: labelAttrs(p.Labels), Start: st, Time: ts,
				Count: strconv.FormatUint(p.Count, 10), Sum: p.Sum, Buckets: b, Bounds: h.Bounds})
		}
		m.Histogram = hist
		ms = append(ms, m)
	}
	return metricsRequest{ResourceMetrics: []resourceMetrics{{
		Resource:     otlpResource{Attributes: encodeAttrs(resource)},
		ScopeMetrics: []scopeMetrics{{Scope: otlpScope{Name: ScopeName}, Metrics: ms}},
	}}}
}
