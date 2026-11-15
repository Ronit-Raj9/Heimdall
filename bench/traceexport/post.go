package traceexport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// postTimeout bounds one POST to a collector.
const postTimeout = 30 * time.Second

// maxErrorBody is the most bytes of a collector's error answer kept in the
// returned error.
const maxErrorBody = 512

// TracesURL returns the URL to POST traces to. An endpoint with no path (or
// "/") is a collector base URL and gets the OTLP/HTTP traces path
// /v1/traces, as OTEL_EXPORTER_OTLP_ENDPOINT does; an endpoint with a path is
// used as given, as OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is (Langfuse's
// /api/public/otel/v1/traces, Maxim's /v1/otel). Only http and https are
// accepted.
func TracesURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("endpoint %q: scheme must be http or https", endpoint)
	}
	if u.Host == "" {
		return "", fmt.Errorf("endpoint %q: no host", endpoint)
	}
	if u.User != nil {
		return "", fmt.Errorf("endpoint: credentials in the URL are not accepted; pass them as a header with -header-env")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return u.String(), nil
}

// ParseHeaders reads headers in the OTEL_EXPORTER_OTLP_HEADERS format:
// comma-separated key=value pairs, each value percent-decoded, for example
// "Authorization=Basic%20cGs6c2s=,x-langfuse-ingestion-version=4". Errors
// never include a value.
func ParseHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	for i, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("header entry %d is not key=value", i+1)
		}
		dec, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("header %s: value is not percent-encoded correctly", k)
		}
		if strings.ContainsAny(k+dec, "\r\n") {
			return nil, fmt.Errorf("header %s: line breaks are not allowed", k)
		}
		out[k] = dec
	}
	return out, nil
}

// Post sends one OTLP/JSON body to a collector's traces URL (TracesURL) with
// the given headers. A 2xx answer is success; anything else is an error
// carrying the status and the start of the answer, never the headers. A nil
// client uses a default with a 30 second timeout.
func Post(ctx context.Context, client *http.Client, tracesURL string, headers map[string]string, body []byte) error {
	if client == nil {
		client = &http.Client{Timeout: postTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tracesURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post traces: %w", err)
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		req.Header.Set(k, headers[k])
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post traces to %s: %w", redactURL(tracesURL), err)
	}
	defer resp.Body.Close()
	msg, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("post traces to %s: read answer: %w", redactURL(tracesURL), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("post traces to %s: status %d: %s", redactURL(tracesURL), resp.StatusCode, clean(strings.TrimSpace(string(msg)), maxErrorBody))
	}
	return nil
}

// redactURL drops the query of a URL for messages, since some collectors
// take a token there.
func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "collector"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}
