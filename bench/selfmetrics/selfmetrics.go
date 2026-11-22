// Package selfmetrics is the server's own Prometheus endpoint (GET /metrics
// in the text exposition format): API latency by method, area and status
// class, plus gauges that collectors read on each scrape (job queue depth,
// leases, workers, runs by status). It has no dependency beyond fasthttp, so
// it adds no module.
package selfmetrics

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// Sample is one value of a metric family with its labels.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Family is one metric with its help text, type (gauge or counter) and
// samples.
type Family struct {
	Name, Help, Type string
	Samples          []Sample
}

// CollectFunc returns a family's samples at scrape time. It must be quick
// and bounded by ctx.
type CollectFunc func(ctx context.Context) []Sample

type collector struct {
	name, help, typ string
	fn              CollectFunc
}

// latencyBuckets are the API latency histogram bounds in seconds.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

type histKey struct{ method, area, class string }

type hist struct {
	counts []uint64 // per bucket, cumulative at render time
	sum    float64
	n      uint64
}

// Registry holds collectors and the latency histogram. The zero value is not
// usable; use New.
type Registry struct {
	mu         sync.Mutex
	collectors []collector
	lat        map[histKey]*hist
	hists      []*HistogramVec // lane Z14: histograms updated as things happen (vec.go)
	timeout    time.Duration
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{lat: map[histKey]*hist{}, timeout: 5 * time.Second}
}

// Default is the process registry the server serves.
var Default = New()

// Gauge registers a gauge read by fn on every scrape. A name registered
// again replaces the earlier collector (a server rebuilt in one process).
func (r *Registry) Gauge(name, help string, fn CollectFunc) { r.add(name, help, "gauge", fn) }

// Counter registers a monotonically increasing value read by fn.
func (r *Registry) Counter(name, help string, fn CollectFunc) { r.add(name, help, "counter", fn) }

func (r *Registry) add(name, help, typ string, fn CollectFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, c := range r.collectors {
		if c.name == name {
			r.collectors[i] = collector{name, help, typ, fn}
			return
		}
	}
	r.collectors = append(r.collectors, collector{name, help, typ, fn})
}

// Observe records one API request.
func (r *Registry) Observe(method, path string, status int, d time.Duration) {
	k := histKey{method: normMethod(method), area: Area(path), class: strconv.Itoa(status/100) + "xx"}
	s := d.Seconds()
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.lat[k]
	if !ok {
		h = &hist{counts: make([]uint64, len(latencyBuckets))}
		r.lat[k] = h
	}
	for i, b := range latencyBuckets {
		if s <= b {
			h.counts[i]++
			break
		}
	}
	h.sum += s
	h.n++
}

// Middleware times every request through next. /metrics itself is not
// counted.
func (r *Registry) Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		start := time.Now()
		next(ctx)
		if string(ctx.Path()) == "/metrics" {
			return
		}
		r.Observe(string(ctx.Method()), string(ctx.Path()), ctx.Response.StatusCode(), time.Since(start))
	}
}

var areaRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Area is the bounded label for a request path: the first segment after
// /api/bench/ (orgs/{org}/ skipped), "ui" for the site, "health" for the
// probes. Ids never become labels.
func Area(path string) string {
	switch {
	case path == "/healthz" || path == "/readyz":
		return "health"
	case !strings.HasPrefix(path, "/api/"):
		return "ui"
	}
	rest := strings.TrimPrefix(path, "/api/")
	rest = strings.TrimPrefix(rest, "bench/")
	if strings.HasPrefix(rest, "orgs/") {
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) == 3 {
			rest = parts[2]
		} else {
			return "orgs"
		}
	}
	seg, _, _ := strings.Cut(rest, "/")
	if !areaRe.MatchString(seg) {
		return "other"
	}
	return seg
}

func normMethod(m string) string {
	switch m {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return m
	}
	return "OTHER"
}

// Gather returns every family: the latency histogram first, then the
// collectors in registration order.
func (r *Registry) Gather(ctx context.Context) []Family {
	r.mu.Lock()
	cs := append([]collector(nil), r.collectors...)
	r.mu.Unlock()
	out := make([]Family, 0, len(cs))
	for _, c := range cs {
		out = append(out, Family{Name: c.name, Help: c.help, Type: c.typ, Samples: c.fn(ctx)})
	}
	return out
}

// Render writes the text exposition format.
func (r *Registry) Render(ctx context.Context) string {
	var b strings.Builder
	r.renderLatency(&b)
	r.renderHists(&b) // lane Z14 (vec.go)
	for _, f := range r.Gather(ctx) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.Name, escHelp(f.Help), f.Name, f.Type)
		for _, s := range f.Samples {
			fmt.Fprintf(&b, "%s%s %s\n", f.Name, labels(s.Labels), num(s.Value))
		}
	}
	return b.String()
}

func (r *Registry) renderLatency(b *strings.Builder) {
	const name = "heimdall_http_request_duration_seconds"
	fmt.Fprintf(b, "# HELP %s API request latency by method, area and status class.\n# TYPE %s histogram\n", name, name)
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]histKey, 0, len(r.lat))
	for k := range r.lat {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, c := keys[i], keys[j]
		if a.area != c.area {
			return a.area < c.area
		}
		if a.method != c.method {
			return a.method < c.method
		}
		return a.class < c.class
	})
	for _, k := range keys {
		h := r.lat[k]
		base := map[string]string{"method": k.method, "area": k.area, "code": k.class}
		var cum uint64
		for i, bound := range latencyBuckets {
			cum += h.counts[i]
			fmt.Fprintf(b, "%s_bucket%s %d\n", name, labels(with(base, "le", num(bound))), cum)
		}
		fmt.Fprintf(b, "%s_bucket%s %d\n", name, labels(with(base, "le", "+Inf")), h.n)
		fmt.Fprintf(b, "%s_sum%s %s\n", name, labels(base), num(h.sum))
		fmt.Fprintf(b, "%s_count%s %d\n", name, labels(base), h.n)
	}
}

func with(m map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for a, b := range m {
		out[a] = b
	}
	out[k] = v
	return out
}

func labels(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+`="`+escLabel(m[k])+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func escHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func num(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Handler serves GET /metrics.
func (r *Registry) Handler(ctx *fasthttp.RequestCtx) {
	if !ctx.IsGet() && !ctx.IsHead() {
		ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	ctx.SetContentType("text/plain; version=0.0.4; charset=utf-8")
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBodyString(r.Render(cctx))
}
