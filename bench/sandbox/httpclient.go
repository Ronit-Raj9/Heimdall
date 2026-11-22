package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/utils"
)

// Response body caps: API answers are small; file downloads have their own.
const (
	maxAPIBody   = 4 << 20
	maxErrorBody = 2 << 10
)

// defaultHTTPClient has no overall timeout: every call carries a context
// with its own deadline (a command may run for minutes). Its transport
// refuses, at connect time, every private, loopback, link-local, metadata
// and other non-public address (G3 B6): the endpoint and every URL a
// platform answers with (Daytona's toolbox proxy) are org-supplied, and the
// server and the shared worker sit on compose networks with internal
// services. Tests that talk to a local fake set AllowPrivateEndpoints.
var defaultHTTPClient = &http.Client{Transport: guardedTransport{}}

// AllowPrivateEndpoints lets platform clients built without their own HTTP
// client reach private and loopback addresses and plain http endpoints.
// Only tests and a single-user local install set it; hosted servers and the
// shared worker never do.
var AllowPrivateEndpoints = false

// guardedTransport picks the guarded or the plain transport per request, so
// AllowPrivateEndpoints takes effect without rebuilding clients.
type guardedTransport struct{}

var publicTransport = utils.GuardedTransport()

func (guardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if AllowPrivateEndpoints {
		return http.DefaultTransport.RoundTrip(r)
	}
	if r.URL.Scheme != "https" {
		return nil, fmt.Errorf("only https platform URLs are allowed, not %s", r.URL.Scheme)
	}
	return publicTransport.RoundTrip(r)
}

// checkEndpoint refuses an org-supplied endpoint that names an internal
// host or is not https, unless AllowPrivateEndpoints. A bare domain (E2B)
// or a name (the Modal environment) is checked as a host when it looks like
// one; anything with a scheme is checked as a URL.
func checkEndpoint(title, endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || AllowPrivateEndpoints {
		return nil
	}
	if strings.Contains(endpoint, "://") {
		if err := utils.CheckPublicURL(endpoint, false); err != nil {
			return fmt.Errorf("%s: the endpoint is refused: %v", title, err)
		}
	}
	return nil
}

// apiClient is the small JSON-over-HTTP client every platform uses. It
// never puts its secrets in an error: each error string has them replaced.
type apiClient struct {
	http    *http.Client
	base    string
	headers map[string]string // set on every request (auth)
	secrets []string          // removed from every error
	title   string            // "Daytona", for error messages
}

// HTTPError is a non-2xx answer. Body holds at most maxErrorBody bytes of
// the answer for code that parses it; Error never includes it (G3 B6: an
// org-supplied endpoint must not turn the server into a reader of internal
// answers), only the status and its standard text.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if t := http.StatusText(e.Status); t != "" {
		return fmt.Sprintf("HTTP %d %s", e.Status, t)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// statusOf returns the HTTP status of err, or 0.
func statusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

// scrub removes every secret from s.
func scrub(s string, secrets []string) string {
	for _, sec := range secrets {
		if len(sec) >= 4 {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return s
}

// scrubbedError carries an error whose text had the secrets removed, while
// errors.Is still finds the sentinel errors under it.
type scrubbedError struct {
	msg   string
	inner error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.inner }

func (c *apiClient) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	switch statusOf(err) {
	case http.StatusUnauthorized, http.StatusForbidden:
		err = fmt.Errorf("%w (%v)", ErrUnauthorized, err)
	case http.StatusNotFound:
		err = fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return &scrubbedError{msg: scrub(fmt.Sprintf("%s %s: %v", c.title, op, err), c.secrets), inner: err}
}

// request builds a request to base+path (or path when it is a full URL).
func (c *apiClient) request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = strings.TrimRight(c.base, "/") + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "heimdall-bench-sandbox/1")
	return req, nil
}

// do sends req and returns the body of a 2xx answer, at most max bytes.
func (c *apiClient) do(req *http.Request, max int64) ([]byte, error) {
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

func (c *apiClient) client() *http.Client {
	if c.http != nil {
		return c.http
	}
	return defaultHTTPClient
}

// json sends in (nil: no body) and decodes a 2xx answer into out (nil:
// ignored).
func (c *apiClient) json(ctx context.Context, op, method, path string, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return c.wrap(op, err)
		}
		body, ct = bytes.NewReader(b), "application/json"
	}
	req, err := c.request(ctx, method, path, body, ct)
	if err != nil {
		return c.wrap(op, err)
	}
	req.Header.Set("Accept", "application/json")
	b, err := c.do(req, maxAPIBody)
	if err != nil {
		return c.wrap(op, err)
	}
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return c.wrap(op, fmt.Errorf("decode answer: %w", err))
		}
	}
	return nil
}

// raw sends a raw body and returns the raw answer (at most max bytes).
func (c *apiClient) raw(ctx context.Context, op, method, path string, body []byte, max int64) ([]byte, error) {
	var r io.Reader
	ct := ""
	if body != nil {
		r, ct = bytes.NewReader(body), "application/octet-stream"
	}
	req, err := c.request(ctx, method, path, r, ct)
	if err != nil {
		return nil, c.wrap(op, err)
	}
	b, err := c.do(req, max)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return nil, fmt.Errorf("%s %s: %w (over %d bytes)", c.title, op, ErrTooLarge, max)
		}
		return nil, c.wrap(op, err)
	}
	return b, nil
}

// poll calls f every interval until it reports done, f fails or ctx ends.
func poll(ctx context.Context, interval time.Duration, f func() (bool, error)) error {
	for {
		done, err := f()
		if err != nil || done {
			return err
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// commandTimeout is c.Timeout, or a minute.
func commandTimeout(c Command) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return time.Minute
}

// lifetimeSeconds is a Spec's lifetime in whole seconds, at least 5 minutes.
func lifetimeSeconds(s Spec) int {
	sec := int(s.Lifetime / time.Second)
	if sec < 300 {
		sec = 300
	}
	return sec
}
