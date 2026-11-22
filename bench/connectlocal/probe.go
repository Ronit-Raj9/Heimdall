package connectlocal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// UserAgent is sent on every probe, so an agent's owner can recognise and
// allow Heimdall's connection checks.
const UserAgent = "heimdall-connect/1"

// Probe limits: a handful of small requests, never a load test.
const (
	maxProbeRequests = 24
	probeBodyCap     = 1 << 20
)

// ProbeOptions are the inputs of Probe.
type ProbeOptions struct {
	// URL is the agent's address: a full endpoint or a server root.
	URL string
	// Headers go on every probe (auth), after ${VAR} expansion.
	Headers map[string]string
	// Curl, when set, is the sample request to start from instead of the ladder.
	Curl *CurlRequest
	// Client sends the probes; nil uses one with a 15 s timeout that never
	// follows redirects.
	Client *http.Client
	// Getenv expands ${VAR} in headers; nil is os.Getenv.
	Getenv func(string) string
	// Nonce overrides the random code (tests).
	Nonce string
}

// ProbeResult is the mapping found, with the evidence lines that decided it.
type ProbeResult struct {
	Mapping  *Mapping `json:"mapping,omitempty"`
	Evidence []string `json:"evidence"`
	// Warnings are things the user should know (the agent echoes its input).
	Warnings []string `json:"warnings,omitempty"`
	// Fields lists the string fields of the last JSON answer when the code
	// was not found, so a person can pick the answer by hand.
	Fields []string `json:"fields,omitempty"`
	// Code is a failure code when no mapping was found: unreachable, auth,
	// path, shape, answer.
	Code string `json:"code,omitempty"`
}

// NoRedirectClient never follows redirects: a probe stays on the host it
// was given.
func NoRedirectClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// NewNonce returns a fresh connection check code, HMD- and five characters.
func NewNonce() string {
	const alpha = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return "HMD-" + string(b)
}

// NoncePrompt is the probe's task.
func NoncePrompt(nonce string) string {
	return "Heimdall connection check. Reply with exactly this code and nothing else: " + nonce
}

type prober struct {
	opts   ProbeOptions
	client *http.Client
	nonce  string
	prompt string
	sent   int
	res    *ProbeResult
}

// Probe finds how to call an HTTP agent from this machine: an
// OpenAI-compatible server (GET /v1/models), the pasted curl request, or
// the nonce ladder (a few request shapes per likely path, steered by what
// validation errors name). A mapping is accepted only when the answer holds
// the fresh code; nothing a model says is taken on trust.
func Probe(ctx context.Context, opts ProbeOptions) *ProbeResult {
	p := &prober{opts: opts, client: opts.Client, res: &ProbeResult{}}
	if p.client == nil {
		p.client = NoRedirectClient(15 * time.Second)
	}
	p.nonce = opts.Nonce
	if p.nonce == "" {
		p.nonce = NewNonce()
	}
	p.prompt = NoncePrompt(p.nonce)
	target := opts.URL
	if opts.Curl != nil && target == "" {
		target = opts.Curl.URL
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.res.Code = "unreachable"
		p.res.Evidence = append(p.res.Evidence, fmt.Sprintf("%q is not an http(s) URL", target))
		return p.res
	}
	headers := map[string]string{}
	for k, v := range opts.Headers {
		headers[k] = v
	}
	if opts.Curl != nil {
		for k, v := range opts.Curl.Headers {
			if _, set := headers[k]; !set {
				headers[k] = v
			}
		}
	}
	if opts.Curl != nil && opts.Curl.Body != "" {
		if m := p.fromCurl(ctx, opts.Curl, headers); m != nil {
			p.res.Mapping = m
			return p.res
		}
		if p.res.Code == "unreachable" || p.res.Code == "auth" {
			return p.res
		}
	}
	if m := p.openAI(ctx, u, headers); m != nil {
		p.res.Mapping = m
		return p.res
	}
	if p.res.Code == "unreachable" || p.res.Code == "auth" {
		return p.res
	}
	if m := p.ladder(ctx, u, headers); m != nil {
		p.res.Mapping = m
		p.res.Code = ""
		return p.res
	}
	if p.res.Code == "" {
		p.res.Code = "shape"
	}
	return p.res
}

func (p *prober) say(format string, args ...any) {
	p.res.Evidence = append(p.res.Evidence, fmt.Sprintf(format, args...))
}

type probeAnswer struct {
	status int
	ctype  string
	body   []byte
	err    error
}

func (p *prober) send(ctx context.Context, method, target string, headers map[string]string, body []byte) probeAnswer {
	if p.sent >= maxProbeRequests {
		return probeAnswer{err: errors.New("probe limit reached")}
	}
	p.sent++
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return probeAnswer{err: err}
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json, text/event-stream, text/plain")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	m := &Mapping{Headers: headers}
	for k, v := range m.ExpandedHeaders(p.opts.Getenv) {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return probeAnswer{err: err}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyCap))
	return probeAnswer{status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: data}
}

// classify records the transport and auth failures that end the probe.
func (p *prober) classify(method, target string, a probeAnswer) bool {
	switch {
	case a.err != nil:
		p.say("%s %s failed: %v", method, target, a.err)
		if p.res.Code == "" {
			p.res.Code = "unreachable"
		}
		return true
	case a.status == 401 || a.status == 403:
		p.say("%s %s answered %d: the agent needs credentials (pass -H 'Authorization: Bearer ${MY_AGENT_TOKEN}')", method, target, a.status)
		p.res.Code = "auth"
		return true
	}
	return false
}

// accept checks an answer for the nonce and builds the mapping.
func (p *prober) accept(method, target string, a probeAnswer, m *Mapping) bool {
	if a.status/100 != 2 {
		return false
	}
	stream := strings.HasPrefix(strings.ToLower(a.ctype), "text/event-stream")
	if stream {
		for _, path := range []string{"", "choices.0.delta.content", "delta", "text", "content", "token", "data"} {
			m.Answer, m.Stream = path, true
			if got, err := m.Extract(a.body, a.ctype); err == nil && strings.Contains(got, p.nonce) {
				p.say("%s %s answered %d with server-sent events; the code is in %s", method, target, a.status, orWhole(path))
				return true
			}
		}
		m.Stream = false
		return false
	}
	var doc any
	if json.Unmarshal(a.body, &doc) == nil {
		if _, isObj := doc.(map[string]any); isObj || isList(doc) {
			path, ok := FindString(doc, p.nonce, p.prompt)
			if !ok {
				if _, echo := FindString(doc, p.nonce, ""); echo {
					p.res.Warnings = append(p.res.Warnings, fmt.Sprintf("%s %s only echoed the prompt back; it did not answer it", method, target))
				}
				p.res.Fields = stringFields(doc)
				p.say("%s %s answered %d, but the code %s is not in the answer", method, target, a.status, p.nonce)
				if p.res.Code == "" || p.res.Code == "path" || p.res.Code == "shape" {
					p.res.Code = "answer"
				}
				return false
			}
			m.Answer = path
			p.say("%s %s answered %d; the code is at %s", method, target, a.status, path)
			return true
		}
	}
	text := string(a.body)
	if strings.Contains(text, p.nonce) {
		if strings.Contains(text, p.prompt) {
			p.res.Warnings = append(p.res.Warnings, "the agent repeats the whole prompt in its answer")
		}
		m.Answer = ""
		p.say("%s %s answered %d in plain text holding the code", method, target, a.status)
		return true
	}
	p.say("%s %s answered %d, but the code %s is not in the answer", method, target, a.status, p.nonce)
	p.res.Code = "answer"
	return false
}

func orWhole(path string) string {
	if path == "" {
		return "the event data"
	}
	return path
}

func isList(v any) bool { _, ok := v.([]any); return ok }

func stringFields(doc any) []string {
	var out []string
	var walk func(v any, path []string)
	walk = func(v any, path []string) {
		if len(out) >= 20 {
			return
		}
		switch t := v.(type) {
		case string:
			out = append(out, strings.Join(path, "."))
		case map[string]any:
			for k, val := range t {
				walk(val, append(append([]string(nil), path...), k))
			}
		case []any:
			for i, e := range t {
				walk(e, append(append([]string(nil), path...), fmt.Sprint(i)))
			}
		}
	}
	walk(doc, nil)
	return out
}

func (p *prober) fromCurl(ctx context.Context, c *CurlRequest, headers map[string]string) *Mapping {
	kind, tpl, field, err := TemplateFromBody(c.Body)
	if err != nil {
		p.say("%v", err)
		return nil
	}
	m := &Mapping{Kind: kind, Method: c.Method, URL: c.URL, Headers: headers, Body: tpl, History: "none"}
	if kind == KindMessages {
		m.History = "client"
	}
	body, _ := m.Render(p.prompt, nil, "heimdall-connect")
	a := p.send(ctx, c.Method, c.URL, headers, body)
	if p.classify(c.Method, c.URL, a) {
		return nil
	}
	p.say("the curl body carries the task in %s", field)
	if p.accept(c.Method, c.URL, a, m) {
		return m
	}
	if a.status/100 != 2 {
		p.say("%s %s answered %d: %s", c.Method, c.URL, a.status, snippet(a.body))
	}
	return nil
}

func (p *prober) openAI(ctx context.Context, u *url.URL, headers map[string]string) *Mapping {
	base := strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	if !strings.HasSuffix(base, "/v1") {
		base = strings.TrimRight(u.Scheme+"://"+u.Host, "/") + "/v1"
	}
	a := p.send(ctx, "GET", base+"/models", headers, nil)
	if p.classify("GET", base+"/models", a) {
		return nil
	}
	if a.status != 200 {
		p.say("GET %s/models answered %d: not an OpenAI-compatible server", base, a.status)
		return nil
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(a.body, &models) != nil || len(models.Data) == 0 || models.Data[0].ID == "" {
		p.say("GET %s/models answered 200 without a model list", base)
		return nil
	}
	model := models.Data[0].ID
	p.say("GET %s/models listed %d models: an OpenAI-compatible server; using %s", base, len(models.Data), model)
	tpl, _ := json.Marshal(map[string]any{"model": model, "messages": MessagesVar})
	m := &Mapping{Kind: KindOpenAI, Method: "POST", URL: base + "/chat/completions", Headers: headers, Body: tpl, History: "client", Model: model}
	body, _ := m.Render(p.prompt, nil, "")
	r := p.send(ctx, "POST", m.URL, headers, body)
	if p.classify("POST", m.URL, r) {
		return nil
	}
	if p.accept("POST", m.URL, r, m) {
		return m
	}
	return nil
}

// ladderPaths are tried in order when the URL names no path.
var ladderPaths = []string{"/chat", "/invoke", "/run", "/api/chat", "/query", "/ask", "/agent", "/message", "/generate", "/"}

// fieldRE pulls a field name out of a validation error.
var (
	pydanticLoc = regexp.MustCompile(`"loc"\s*:\s*\[\s*"body"\s*,\s*"([A-Za-z_][A-Za-z0-9_]*)"`)
	zodPath     = regexp.MustCompile(`"path"\s*:\s*\[\s*"([A-Za-z_][A-Za-z0-9_]*)"`)
	requiredRE  = regexp.MustCompile(`(?i)["'\x60]?([A-Za-z_][A-Za-z0-9_]*)["'\x60]?\s+(?:is\s+)?(?:required|missing|must be provided)`)
)

// FieldFromError returns the request field a 4xx body names, if any.
func FieldFromError(body []byte) string {
	for _, re := range []*regexp.Regexp{pydanticLoc, zodPath, requiredRE} {
		if m := re.FindSubmatch(body); m != nil {
			f := string(m[1])
			switch strings.ToLower(f) {
			case "field", "body", "value", "request", "this", "it", "parameter", "property":
				continue
			}
			return f
		}
	}
	return ""
}

func (p *prober) ladder(ctx context.Context, u *url.URL, headers map[string]string) *Mapping {
	origin := u.Scheme + "://" + u.Host
	paths := ladderPaths
	if u.Path != "" && u.Path != "/" {
		paths = []string{u.Path}
	}
	query := ""
	if u.RawQuery != "" {
		query = "?" + u.RawQuery
	}
	for _, path := range paths {
		target := origin + path + query
		shapes := []struct {
			kind  string
			field string
		}{{KindField, "message"}, {KindMessages, ""}, {KindField, "input"}, {KindField, "query"}, {KindField, "prompt"}, {KindField, "question"}}
		tried := map[string]bool{}
		for i := 0; i < len(shapes); i++ {
			s := shapes[i]
			key := s.kind + ":" + s.field
			if tried[key] {
				continue
			}
			tried[key] = true
			m := &Mapping{Kind: s.kind, Method: "POST", URL: target, Headers: headers, History: "none"}
			if s.kind == KindMessages {
				m.Body, _ = json.Marshal(map[string]any{"messages": MessagesVar})
				m.History = "client"
			} else {
				m.Body, _ = json.Marshal(map[string]any{s.field: InputVar})
			}
			body, _ := m.Render(p.prompt, nil, "")
			a := p.send(ctx, "POST", target, headers, body)
			if p.classify("POST", target, a) {
				return nil
			}
			if a.status == 404 || a.status == 405 {
				p.say("POST %s answered %d", target, a.status)
				if p.res.Code == "" {
					p.res.Code = "path"
				}
				break // no such route: next path
			}
			if p.accept("POST", target, a, m) {
				if m.Kind == KindField {
					p.trySession(ctx, m, s.field)
				}
				return m
			}
			if a.status/100 == 4 {
				if f := FieldFromError(a.body); f != "" && !tried[KindField+":"+f] && !(f == "messages" && tried[KindMessages+":"]) {
					p.say("POST %s answered %d naming the field %s", target, a.status, f)
					// Try the named field next; a messages field is the chat shape.
					next := struct {
						kind  string
						field string
					}{KindField, f}
					if f == "messages" {
						next.kind, next.field = KindMessages, ""
					}
					shapes = append(shapes[:i+1], append([]struct {
						kind  string
						field string
					}{next}, shapes[i+1:]...)...)
				} else {
					p.say("POST %s answered %d: %s", target, a.status, snippet(a.body))
				}
				p.res.Code = "shape"
			}
			if p.sent >= maxProbeRequests {
				return nil
			}
		}
	}
	return nil
}

// trySession checks whether the agent takes a session id next to the
// input, so a multi-turn chat keeps its context on the agent's side.
func (p *prober) trySession(ctx context.Context, m *Mapping, field string) {
	for _, sf := range []string{"session_id", "thread_id", "conversation_id"} {
		tpl, _ := json.Marshal(map[string]any{field: InputVar, sf: SessionVar})
		try := *m
		try.Body = tpl
		body, _ := try.Render(p.prompt, nil, "heimdall-connect-check")
		a := p.send(ctx, "POST", m.URL, m.Headers, body)
		if a.err != nil || a.status/100 != 2 {
			continue
		}
		save := p.res.Evidence
		if p.accept("POST", m.URL, a, &try) {
			p.res.Evidence = append(save, fmt.Sprintf("the agent also takes %s: chat turns keep their session", sf))
			m.Body, m.History, m.Answer = tpl, "server", try.Answer
			return
		}
		p.res.Evidence = save
		return
	}
}

func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	if s == "" {
		s = "(empty body)"
	}
	return s
}
