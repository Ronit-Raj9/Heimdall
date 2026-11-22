package platformimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody caps every platform response the importer reads.
const maxBody = 2 << 20

// Client fetches agent definitions from the platforms. HTTP must refuse
// private addresses on a hosted server (the handler passes a guarded
// client) and must not follow redirects.
type Client struct {
	HTTP *http.Client
	Now  func() time.Time
	// Rewrite, when set, changes every outgoing request URL just before it
	// is sent (tests point the platform hosts at httptest servers). The
	// imported agent config always keeps the real platform URLs.
	Rewrite func(u *url.URL)
	// CheckURL, when set, vets every address the user typed (a LangGraph
	// deployment, a Dataverse environment) before it is called; the
	// handler passes the webhook URL guard on a hosted server.
	CheckURL func(raw string) error
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// call is one platform request.
type call struct {
	method  string
	url     string
	header  http.Header
	body    []byte
	form    url.Values // sent as application/x-www-form-urlencoded when set
	sign    func(*http.Request, []byte) error
	what    string // "the Vapi assistant" for error messages
	secrets []string
}

// StatusError is a platform answer other than 2xx.
type StatusError struct {
	Status int
	What   string
	Detail string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s answered %d", e.What, e.Status)
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += ": the key was refused (check the secret and its permissions)"
	case http.StatusNotFound:
		msg += ": not found (check the id, region or project)"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// do sends one request and returns the raw body of a 2xx answer.
func (c *Client) do(ctx context.Context, k call) ([]byte, http.Header, error) {
	body := k.body
	if k.form != nil {
		body = []byte(k.form.Encode())
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, k.method, k.url, rd)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: build the request: %v", k.what, err)
	}
	for name, vs := range k.header {
		for _, v := range vs {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "heimdall-agent-import/1")
	if k.form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if k.sign != nil {
		if err := k.sign(req, body); err != nil {
			return nil, nil, fmt.Errorf("%s: %v", k.what, err)
		}
	}
	if c.Rewrite != nil {
		host := req.Host
		if host == "" {
			host = req.URL.Host
		}
		c.Rewrite(req.URL)
		req.Host = host // the signed host stays the platform's
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s did not answer: %s", k.what, scrub(err.Error(), k.secrets))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: read the answer: %v", k.what, err)
	}
	if len(raw) > maxBody {
		return nil, nil, fmt.Errorf("%s: the answer is over %d MiB", k.what, maxBody>>20)
	}
	if resp.StatusCode/100 == 3 {
		return nil, nil, fmt.Errorf("%s answered a redirect (%d) to %s: give the final address", k.what, resp.StatusCode, hostOnly(resp.Header.Get("Location")))
	}
	if resp.StatusCode/100 != 2 {
		return nil, nil, &StatusError{Status: resp.StatusCode, What: k.what, Detail: scrub(errorDetail(raw), k.secrets)}
	}
	return raw, resp.Header, nil
}

// getJSON sends k and decodes a JSON object answer.
func (c *Client) getJSON(ctx context.Context, k call) (map[string]any, error) {
	raw, _, err := c.do(ctx, k)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s answered something that is not a JSON object", k.what)
	}
	return out, nil
}

// errorDetail pulls a short message out of an error body.
func errorDetail(raw []byte) string {
	var doc any
	if json.Unmarshal(raw, &doc) == nil {
		for _, p := range []string{"message", "error.message", "error_description", "detail", "error", "Message", "[0].message"} {
			if s := str(doc, p); s != "" {
				return truncate(s, 200)
			}
		}
		if m, ok := doc.(map[string]any); ok {
			if d, ok := m["detail"].([]any); ok && len(d) > 0 {
				return truncate(str(d[0], "msg"), 200)
			}
		}
		return ""
	}
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return truncate(s, 200)
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// scrub removes every secret value from s.
func scrub(s string, secrets []string) string {
	for _, v := range secrets {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "[secret]")
		}
	}
	return s
}

func hostOnly(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "another address"
}

// isStatus reports whether err is a platform answer with one of codes.
func isStatus(err error, codes ...int) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	for _, c := range codes {
		if se.Status == c {
			return true
		}
	}
	return false
}

// str reads a dotted path out of decoded JSON: a.b, a[0].b, [0].c. A
// number or bool is formatted; a missing value is "".
func str(v any, path string) string {
	switch x := lookup(v, path).(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case bool:
		return fmt.Sprintf("%t", x)
	}
	return ""
}

// list reads a dotted path that holds an array.
func list(v any, path string) []any {
	a, _ := lookup(v, path).([]any)
	return a
}

// obj reads a dotted path that holds an object.
func obj(v any, path string) map[string]any {
	m, _ := lookup(v, path).(map[string]any)
	return m
}

func lookup(v any, path string) any {
	if path == "" {
		return v
	}
	for _, part := range strings.Split(path, ".") {
		name, idx := part, -1
		if i := strings.IndexByte(part, '['); i >= 0 && strings.HasSuffix(part, "]") {
			name = part[:i]
			fmt.Sscanf(part[i+1:len(part)-1], "%d", &idx)
		}
		if name != "" {
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[name]
		}
		if idx >= 0 {
			a, ok := v.([]any)
			if !ok || idx >= len(a) {
				return nil
			}
			v = a[idx]
		}
	}
	return v
}
