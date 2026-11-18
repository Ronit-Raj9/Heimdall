package traceinbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTP limits for the source clients.
const (
	maxAnswerBytes = 32 << 20 // one API answer
	maxAttempts    = 4        // a request is tried at most this often
	maxRetryWait   = 60 * time.Second
	errorSnippet   = 300
	defaultTimeout = 60 * time.Second
	userAgent      = "heimdall-inbox/1"
)

// apiClient sends requests to a source's API with retries on rate limits
// and transient server errors. It never puts a credential in an error.
type apiClient struct {
	http  *http.Client
	sleep func(ctx context.Context, d time.Duration) error
}

func newAPIClient(c *http.Client, sleep func(context.Context, time.Duration) error) *apiClient {
	if c == nil {
		c = &http.Client{Timeout: defaultTimeout}
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	return &apiClient{http: c, sleep: sleep}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// httpError is a non-2xx answer.
type httpError struct {
	Method, URL string
	Status      int
	Body        string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("%s %s: status %d: %s", e.Method, e.URL, e.Status, e.Body)
}

// doJSON sends one request (body encoded as JSON when not nil) and decodes
// the JSON answer into out. header sets the auth headers. A 429 or a 502,
// 503 or 504 is retried after Retry-After (capped) or a doubling backoff.
func (c *apiClient) doJSON(ctx context.Context, method, rawURL string, header http.Header, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("%s %s: encode body: %w", method, redactURL(rawURL), err)
		}
	}
	wait := time.Second
	for attempt := 1; ; attempt++ {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, redactURL(rawURL), err)
		}
		for k, v := range header {
			req.Header[k] = v
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil || attempt >= maxAttempts {
				return fmt.Errorf("%s %s: %w", method, redactURL(rawURL), err)
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			wait *= 2
			continue
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
		resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("%s %s: read answer: %w", method, redactURL(rawURL), rerr)
		}
		if retryable(resp.StatusCode) && attempt < maxAttempts {
			d := retryAfter(resp.Header.Get("Retry-After"), wait)
			if err := c.sleep(ctx, d); err != nil {
				return err
			}
			wait *= 2
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return &httpError{Method: method, URL: redactURL(rawURL), Status: resp.StatusCode, Body: snippet(raw)}
		}
		if len(raw) > maxAnswerBytes {
			return fmt.Errorf("%s %s: answer over %d bytes", method, redactURL(rawURL), maxAnswerBytes)
		}
		if out == nil {
			return nil
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("%s %s: decode answer: %w", method, redactURL(rawURL), err)
		}
		return nil
	}
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// retryAfter reads a Retry-After header (seconds or an HTTP date), capped
// at maxRetryWait; without one it is fallback.
func retryAfter(h string, fallback time.Duration) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return min(fallback, maxRetryWait)
	}
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		return min(time.Duration(n)*time.Second, maxRetryWait)
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(0, min(time.Until(t), maxRetryWait))
	}
	return min(fallback, maxRetryWait)
}

// redactURL drops the query string from a URL in an error, so no filter
// value (or a key someone put in a query) reaches a log.
func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > errorSnippet {
		s = s[:errorSnippet] + "..."
	}
	return s
}

// isStatus reports whether err is an answer with the given status.
func isStatus(err error, status int) bool {
	var he *httpError
	return errors.As(err, &he) && he.Status == status
}
