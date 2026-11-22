package selfmetrics

// Lane Z14: counters and histograms that code updates as things happen
// (lease ends, evaluation start latency, upload acceptance time), next to
// the scrape-time collectors. Label values must come from a small fixed
// set: never an org, experiment, run or lease id.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// CounterVec is a counter with fixed label keys.
type CounterVec struct {
	keys []string
	mu   sync.Mutex
	vals map[string]float64
	lbl  map[string][]string
}

// CounterVec registers a counter updated with Add. A name registered again
// returns a fresh vec that replaces the earlier one.
func (r *Registry) CounterVec(name, help string, labelKeys ...string) *CounterVec {
	c := &CounterVec{keys: append([]string(nil), labelKeys...), vals: map[string]float64{}, lbl: map[string][]string{}}
	r.Counter(name, help, func(context.Context) []Sample { return c.samples() })
	return c
}

// Add adds v (>= 0) to the series of labelValues, given in key order.
func (c *CounterVec) Add(v float64, labelValues ...string) {
	if c == nil || v < 0 {
		return
	}
	vals := fit(labelValues, len(c.keys))
	k := strings.Join(vals, "\x00")
	c.mu.Lock()
	c.vals[k] += v
	c.lbl[k] = vals
	c.mu.Unlock()
}

// Inc adds one.
func (c *CounterVec) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Value reads one series (tests).
func (c *CounterVec) Value(labelValues ...string) float64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vals[strings.Join(fit(labelValues, len(c.keys)), "\x00")]
}

func (c *CounterVec) samples() []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.vals))
	for k := range c.vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Sample, 0, len(keys))
	for _, k := range keys {
		out = append(out, Sample{Labels: zip(c.keys, c.lbl[k]), Value: c.vals[k]})
	}
	return out
}

// HistogramVec is a histogram with fixed bounds and label keys.
type HistogramVec struct {
	name, help string
	bounds     []float64
	keys       []string
	mu         sync.Mutex
	series     map[string]*hseries
}

type hseries struct {
	labels []string
	counts []uint64 // per bucket, not cumulative; last is +Inf
	sum    float64
	n      uint64
}

// Histogram registers a histogram (bounds ascending, in the unit of the
// name, seconds for *_seconds). A name registered again replaces the
// earlier one.
func (r *Registry) Histogram(name, help string, bounds []float64, labelKeys ...string) *HistogramVec {
	h := &HistogramVec{name: name, help: help, bounds: append([]float64(nil), bounds...), keys: append([]string(nil), labelKeys...), series: map[string]*hseries{}}
	sort.Float64s(h.bounds)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.hists {
		if x.name == name {
			r.hists[i] = h
			return h
		}
	}
	r.hists = append(r.hists, h)
	return h
}

// Observe records one value for labelValues, given in key order.
func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	if h == nil {
		return
	}
	vals := fit(labelValues, len(h.keys))
	k := strings.Join(vals, "\x00")
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[k]
	if !ok {
		s = &hseries{labels: vals, counts: make([]uint64, len(h.bounds)+1)}
		h.series[k] = s
	}
	i := sort.SearchFloat64s(h.bounds, v) // first bound >= v
	s.counts[i]++
	s.sum += v
	s.n++
}

// HistogramPoint is one series of a histogram snapshot.
type HistogramPoint struct {
	Labels map[string]string
	Counts []uint64 // per bucket, not cumulative; len(Bounds)+1, the last is +Inf
	Sum    float64
	Count  uint64
}

// HistogramFamily is a histogram in a Snapshot.
type HistogramFamily struct {
	Name, Help string
	Bounds     []float64
	Points     []HistogramPoint
}

func (h *HistogramVec) snapshot() HistogramFamily {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := HistogramFamily{Name: h.name, Help: h.help, Bounds: append([]float64(nil), h.bounds...)}
	keys := make([]string, 0, len(h.series))
	for k := range h.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := h.series[k]
		f.Points = append(f.Points, HistogramPoint{Labels: zip(h.keys, s.labels), Counts: append([]uint64(nil), s.counts...), Sum: s.sum, Count: s.n})
	}
	return f
}

// Snapshot is every metric at one moment: the collectors' families and the
// histograms, the API latency one included (heimdall_http_request_duration_seconds).
type Snapshot struct {
	Families   []Family
	Histograms []HistogramFamily
}

// Snapshot gathers everything once (the OTLP metric push reads it).
func (r *Registry) Snapshot(ctx context.Context) Snapshot {
	out := Snapshot{Families: r.Gather(ctx)}
	out.Histograms = append(out.Histograms, r.latencySnapshot())
	r.mu.Lock()
	hs := append([]*HistogramVec(nil), r.hists...)
	r.mu.Unlock()
	for _, h := range hs {
		out.Histograms = append(out.Histograms, h.snapshot())
	}
	return out
}

func (r *Registry) latencySnapshot() HistogramFamily {
	f := HistogramFamily{Name: "heimdall_http_request_duration_seconds", Help: "API request latency by method, area and status class.", Bounds: append([]float64(nil), latencyBuckets...)}
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]histKey, 0, len(r.lat))
	for k := range r.lat {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j])
	})
	for _, k := range keys {
		h := r.lat[k]
		counts := append([]uint64(nil), h.counts...)
		var in uint64
		for _, c := range counts {
			in += c
		}
		counts = append(counts, h.n-in) // over the last bound
		f.Points = append(f.Points, HistogramPoint{Labels: map[string]string{"method": k.method, "area": k.area, "code": k.class}, Counts: counts, Sum: h.sum, Count: h.n})
	}
	return f
}

// renderHists writes the registered histograms in the text format.
func (r *Registry) renderHists(b *strings.Builder) {
	r.mu.Lock()
	hs := append([]*HistogramVec(nil), r.hists...)
	r.mu.Unlock()
	for _, h := range hs {
		f := h.snapshot()
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s histogram\n", f.Name, escHelp(f.Help), f.Name)
		for _, p := range f.Points {
			var cum uint64
			for i, bound := range f.Bounds {
				cum += p.Counts[i]
				fmt.Fprintf(b, "%s_bucket%s %d\n", f.Name, labels(with(p.Labels, "le", num(bound))), cum)
			}
			fmt.Fprintf(b, "%s_bucket%s %d\n", f.Name, labels(with(p.Labels, "le", "+Inf")), p.Count)
			fmt.Fprintf(b, "%s_sum%s %s\n", f.Name, labels(p.Labels), num(p.Sum))
			fmt.Fprintf(b, "%s_count%s %d\n", f.Name, labels(p.Labels), p.Count)
		}
	}
}

// fit pads or cuts label values to n.
func fit(vals []string, n int) []string {
	out := make([]string, n)
	copy(out, vals)
	return out
}

func zip(keys, vals []string) map[string]string {
	if len(keys) == 0 {
		return nil
	}
	m := make(map[string]string, len(keys))
	for i, k := range keys {
		m[k] = vals[i]
	}
	return m
}
