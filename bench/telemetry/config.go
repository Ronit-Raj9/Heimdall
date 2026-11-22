package telemetry

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is where and how spans and metrics go. ConfigFromEnv reads it
// from the standard OTEL_* variables.
type Config struct {
	TracesURL  string // full OTLP/HTTP traces URL; empty: no traces
	MetricsURL string // full OTLP/HTTP metrics URL; empty: no metric push
	Headers    map[string]string
	// MetricHeaders are added to Headers for metric pushes only.
	MetricHeaders map[string]string
	Resource      []Attr
	SampleRatio   float64       // share of root traces kept (1 by default)
	FlushEvery    time.Duration // span batch interval (5 s by default)
	MetricsEvery  time.Duration // metric push interval (60 s by default)
	HTTP          *http.Client
	Warn          func(string)
}

// ConfigFromEnv reads the OpenTelemetry environment variables the SDKs use:
//
//	OTEL_SDK_DISABLED=true               everything off
//	OTEL_EXPORTER_OTLP_ENDPOINT          base URL; /v1/traces and /v1/metrics are added
//	OTEL_EXPORTER_OTLP_TRACES_ENDPOINT   full traces URL (wins over the base)
//	OTEL_EXPORTER_OTLP_METRICS_ENDPOINT  full metrics URL (wins over the base)
//	OTEL_EXPORTER_OTLP_HEADERS           k=v,k2=v2 (values URL-decoded); _TRACES_ and _METRICS_ variants
//	OTEL_EXPORTER_OTLP_PROTOCOL          only http/json is spoken; other values are warned about
//	OTEL_TRACES_EXPORTER=none            no traces
//	OTEL_METRICS_EXPORTER=otlp           push metrics too (off by default: Prometheus scrapes /metrics)
//	OTEL_TRACES_SAMPLER                  always_on, always_off, traceidratio, parentbased_* (children follow the parent)
//	OTEL_TRACES_SAMPLER_ARG              ratio for traceidratio
//	OTEL_METRIC_EXPORT_INTERVAL          push interval in ms
//	OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES
//
// service and version name the process when OTEL_SERVICE_NAME is unset.
// The returned warnings are for the log.
func ConfigFromEnv(service, version string, getenv func(string) string) (Config, []string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	var warns []string
	cfg := Config{SampleRatio: 1, Headers: map[string]string{}, MetricHeaders: map[string]string{}}
	if strings.EqualFold(strings.TrimSpace(getenv("OTEL_SDK_DISABLED")), "true") {
		return Config{}, nil
	}
	base := strings.TrimRight(strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT")), "/")
	endpoint := func(specific, suffix string) string {
		if v := strings.TrimSpace(getenv(specific)); v != "" {
			return v
		}
		if base != "" {
			return base + suffix
		}
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(getenv("OTEL_TRACES_EXPORTER")), "none") {
		cfg.TracesURL = endpoint("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "/v1/traces")
	}
	if strings.EqualFold(strings.TrimSpace(getenv("OTEL_METRICS_EXPORTER")), "otlp") {
		cfg.MetricsURL = endpoint("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "/v1/metrics")
	}
	for _, u := range []*string{&cfg.TracesURL, &cfg.MetricsURL} {
		if *u == "" {
			continue
		}
		p, err := url.Parse(*u)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			warns = append(warns, "telemetry: ignoring OTLP endpoint "+strconv.Quote(*u)+": not an http(s) URL")
			*u = ""
		}
	}
	if p := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_PROTOCOL")); p != "" && p != "http/json" {
		warns = append(warns, "telemetry: OTEL_EXPORTER_OTLP_PROTOCOL="+p+" is not spoken; sending http/json (an OpenTelemetry Collector accepts it on port 4318)")
	}
	parseHeaders(getenv("OTEL_EXPORTER_OTLP_HEADERS"), cfg.Headers)
	parseHeaders(getenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS"), cfg.Headers)
	parseHeaders(getenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS"), cfg.MetricHeaders)

	switch s := strings.TrimSpace(getenv("OTEL_TRACES_SAMPLER")); s {
	case "", "always_on", "parentbased_always_on":
	case "always_off", "parentbased_always_off":
		cfg.SampleRatio = 0
	case "traceidratio", "parentbased_traceidratio":
		r, err := strconv.ParseFloat(strings.TrimSpace(getenv("OTEL_TRACES_SAMPLER_ARG")), 64)
		if err != nil || r < 0 || r > 1 {
			warns = append(warns, "telemetry: OTEL_TRACES_SAMPLER_ARG must be a ratio from 0 to 1; sampling everything")
		} else {
			cfg.SampleRatio = r
		}
	default:
		warns = append(warns, "telemetry: OTEL_TRACES_SAMPLER="+s+" is not supported; sampling everything")
	}
	if ms, err := strconv.Atoi(strings.TrimSpace(getenv("OTEL_METRIC_EXPORT_INTERVAL"))); err == nil && ms > 0 {
		cfg.MetricsEvery = time.Duration(ms) * time.Millisecond
	}

	name := strings.TrimSpace(getenv("OTEL_SERVICE_NAME"))
	if name == "" {
		name = service
	}
	res := map[string]string{}
	parseHeaders(getenv("OTEL_RESOURCE_ATTRIBUTES"), res)
	res["service.name"] = name
	if version != "" {
		if _, ok := res["service.version"]; !ok {
			res["service.version"] = version
		}
	}
	if h, err := os.Hostname(); err == nil {
		if _, ok := res["host.name"]; !ok {
			res["host.name"] = h
		}
	}
	res["service.instance.id"] = newSpanID().String() + newSpanID().String()
	keys := make([]string, 0, len(res))
	for k := range res {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cfg.Resource = append(cfg.Resource, String(k, res[k]))
	}
	return cfg, warns
}

// parseHeaders reads k=v,k2=v2 with URL-encoded values into into.
func parseHeaders(s string, into map[string]string) {
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		if dv, err := url.QueryUnescape(strings.TrimSpace(v)); err == nil {
			v = dv
		}
		into[k] = strings.TrimSpace(v)
	}
}

// NewTracer starts a tracer exporting to cfg.TracesURL; with no URL it
// records nothing.
func NewTracer(cfg Config) *Tracer {
	if cfg.TracesURL == "" {
		return &Tracer{}
	}
	return &Tracer{exp: newExporter(cfg), ratio: cfg.SampleRatio}
}

// Setup configures process telemetry from the environment: the tracer
// (Default) and, when asked for, the metric push. It returns a shutdown
// that flushes both, bounded by its context. With no endpoint set it does
// nothing and the shutdown is a no-op.
func Setup(service, version string, warn func(string), metrics MetricsSource) func(context.Context) {
	cfg, warns := ConfigFromEnv(service, version, nil)
	if warn == nil {
		warn = func(string) {}
	}
	cfg.Warn = warn
	for _, w := range warns {
		warn(w)
	}
	var stops []func(context.Context)
	if cfg.TracesURL != "" {
		t := NewTracer(cfg)
		SetDefault(t)
		stops = append(stops, t.Shutdown)
	}
	if cfg.MetricsURL != "" && metrics != nil {
		p := StartMetricsPush(cfg, metrics)
		stops = append(stops, p.Stop)
	}
	return func(ctx context.Context) {
		for _, s := range stops {
			s(ctx)
		}
	}
}
