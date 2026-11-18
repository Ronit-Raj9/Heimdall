package monitorcert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxHTTPAnswer caps an http monitor's response body.
const maxHTTPAnswer = 1 << 20

// httpMonitor POSTs each request as JSON to an operator-run endpoint and
// reads one JSON answer per request. The bench does not sandbox the
// endpoint: whatever it runs on, with whatever network, is the operator's,
// and a certificate records network "operator-managed". The transcript
// leaves the bench host when the URL is not local.
type httpMonitor struct {
	url     string
	client  *http.Client
	timeout time.Duration
}

func newHTTPMonitor(c *schemas.MonitorConfig, client *http.Client) *httpMonitor {
	timeout := time.Duration(c.TimeoutS) * time.Second
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &httpMonitor{url: c.URL, client: client, timeout: timeout}
}

// Score implements Monitor: one POST per request, in order.
func (m *httpMonitor) Score(ctx context.Context, reqs []schemas.MonitorRequest) ([]schemas.MonitorResponse, error) {
	out := make([]schemas.MonitorResponse, 0, len(reqs))
	for i := range reqs {
		r, err := m.post(ctx, &reqs[i])
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		out = append(out, *r)
	}
	return out, nil
}

func (m *httpMonitor) post(ctx context.Context, req *schemas.MonitorRequest) (*schemas.MonitorResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode monitor request: %w", err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build monitor request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("post to monitor: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPAnswer+1))
	if err != nil {
		return nil, fmt.Errorf("read monitor answer: %w", err)
	}
	if len(b) > maxHTTPAnswer {
		return nil, fmt.Errorf("monitor answer over %d bytes", maxHTTPAnswer)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("monitor answered status %d: %s", resp.StatusCode, excerpt(string(b), 200))
	}
	var r schemas.MonitorResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("decode monitor answer: %w", err)
	}
	return &r, nil
}

// excerpt trims s to at most n bytes for an error message.
func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
