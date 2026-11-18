package export

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

// PostTrace sends one encoded trace to a collector's traces URL with the
// given headers and content type (traceexport.Post sends OTLP/JSON only). A
// 2xx answer is success; otherwise the error carries the status and the
// start of the answer, never a header value.
func PostTrace(ctx context.Context, client *http.Client, tracesURL string, headers map[string]string, body []byte, contentType string) error {
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
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post traces to %s: %w", redact(tracesURL), err)
	}
	defer resp.Body.Close()
	msg, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return fmt.Errorf("post traces to %s: read answer: %w", redact(tracesURL), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		text := strings.Map(func(r rune) rune {
			if r < 0x20 {
				return ' '
			}
			return r
		}, strings.TrimSpace(string(msg)))
		return fmt.Errorf("post traces to %s: status %d: %s", redact(tracesURL), resp.StatusCode, text)
	}
	return nil
}

// redact drops the query and credentials of a URL for messages.
func redact(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "collector"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}
