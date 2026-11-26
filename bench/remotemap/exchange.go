package remotemap

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MaxAnswerBytes caps one response body.
const MaxAnswerBytes = 16 << 20

// MaxPolls caps the polls of one answer.
const MaxPolls = 600

// Sent is one request as shown to the user: secret values masked.
type Sent struct {
	Step    string     `json:"step"` // start | request | poll | end
	Method  string     `json:"method"`
	URL     string     `json:"url"`
	Headers [][]string `json:"headers"` // [name, value] in send order
	Body    string     `json:"body,omitempty"`
}

// Exchange is the outcome of one mapped call.
type Exchange struct {
	Sent        []Sent
	Status      int
	ContentType string
	Raw         []byte // the last answer's body (the stream as received for a streamed answer)
	// Answer is the text the mapping read. Mapped is false when the
	// mapping names no answer path, stream or poll: the caller reads Raw in
	// Heimdall's own shape.
	Answer string
	Mapped bool
	Events int
	Polls  int
	Header http.Header
}

// Caller sends a remote agent's mapped requests (the endpoint test; runs
// use the launcher). Client builds the HTTP client for a TLS config (nil:
// default TLS). Step, when set, gets each trace line. Sign, when set, signs
// the main request's body (HMAC, remote.sign_secret).
type Caller struct {
	Client func(tlsCfg *tls.Config) *http.Client
	Auth   *Authorizer
	// AuthHeader is where a fetched token goes (remote.auth_header).
	AuthHeader string
	// Token is the static token of remote.auth_secret ("" none); a token
	// from the auth block wins.
	Token string
	Sign  func(req *http.Request, body []byte)
	Step  func(kind, detail string)
	Sleep func(ctx context.Context, d time.Duration) error
	// DefaultBody is Heimdall's own body, sent when the mapping has none.
	DefaultBody []byte
}

func (c *Caller) step(kind, format string, a ...any) {
	if c.Step != nil {
		c.Step(kind, fmt.Sprintf(format, a...))
	}
}

func (c *Caller) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// request is one rendered request.
type request struct {
	step    string
	method  string
	url     *url.URL
	headers [][]string // real values
	shown   [][]string // secret values masked
	body    []byte
}

func masked(secrets map[string]string) map[string]string {
	out := make(map[string]string, len(secrets))
	for k := range secrets {
		out[k] = "[secret " + k + "]"
	}
	return out
}

// renderRequest renders one request of the mapping.
func renderRequest(step, method, rawURL string, base *url.URL, headers, query map[string]string, body string, def []byte, v *Vars) (*request, error) {
	if method == "" {
		method = http.MethodPost
	}
	u, err := RenderURL(rawURL, base, v)
	if err != nil {
		return nil, err
	}
	if !SameOrigin(base, u) {
		return nil, fmt.Errorf("%s url %s is not on the agent URL's host", step, u.Host)
	}
	if len(query) > 0 {
		u2 := *u
		q := u2.Query()
		for _, k := range sortedKeys(query) {
			val, err := RenderText(query[k], v, true, nil)
			if err != nil {
				return nil, fmt.Errorf("query %s: %v", k, err)
			}
			q.Set(k, val)
		}
		u2.RawQuery = q.Encode()
		u = &u2
	}
	r := &request{step: step, method: strings.ToUpper(method), url: u}
	shownVars := *v
	shownVars.Secrets = masked(v.Secrets)
	for _, k := range sortedKeys(headers) {
		val, err := RenderText(headers[k], v, true, nil)
		if err != nil {
			return nil, fmt.Errorf("header %s: %v", k, err)
		}
		sv, _ := RenderText(headers[k], &shownVars, true, nil)
		r.headers = append(r.headers, []string{k, val})
		r.shown = append(r.shown, []string{k, sv})
	}
	switch {
	case body != "":
		b, err := RenderBody(body, v)
		if err != nil {
			return nil, err
		}
		r.body = b
	case def != nil && (r.method == http.MethodPost || r.method == http.MethodPut || r.method == http.MethodPatch):
		r.body = def
	}
	return r, nil
}

// Plan renders every request of a mapping without sending any: the
// connect form's preview. Secret values are masked; auth headers are
// described, not fetched.
func Plan(r *schemas.RemoteAgent, v *Vars, def []byte) ([]Sent, error) {
	if err := Check(r); err != nil {
		return nil, err
	}
	base, err := url.Parse(r.URL)
	if err != nil {
		return nil, err
	}
	vv := *v
	names := map[string]string{}
	for _, n := range r.SecretNames() {
		names[n] = n
	}
	vv.Secrets = masked(names)
	if vv.StartID == "" {
		vv.StartID = "<id from the start step>"
	}
	if vv.ReplyID == "" {
		vv.ReplyID = "<id from the answer>"
	}
	m := mappingOf(r)
	var out []Sent
	add := func(req *request) {
		s := Sent{Step: req.step, Method: req.method, URL: redactURL(req.url), Headers: append([][]string{}, req.shown...), Body: string(req.body)}
		if len(req.body) > 0 {
			s.Headers = append(s.Headers, []string{"Content-Type", "application/json"})
		}
		s.Headers = append(s.Headers, authShown(r)...)
		out = append(out, s)
	}
	if st := m.Start; st != nil {
		req, err := renderRequest("start", st.Method, st.URL, base, mergeHeaders(m.Headers, st.Headers), nil, st.Body, nil, &vv)
		if err != nil {
			return nil, fmt.Errorf("start: %v", err)
		}
		add(req)
	}
	req, err := renderRequest("request", m.Method, r.URL, base, m.Headers, m.Query, m.Body, def, &vv)
	if err != nil {
		return nil, err
	}
	add(req)
	if p := m.Poll; p != nil {
		req, err := renderRequest("poll", http.MethodGet, p.URL, base, m.Headers, nil, "", nil, &vv)
		if err != nil {
			return nil, fmt.Errorf("poll: %v", err)
		}
		add(req)
	}
	if e := m.End; e != nil {
		req, err := renderRequest("end", orDefault(e.Method, http.MethodDelete), e.URL, base, mergeHeaders(m.Headers, e.Headers), nil, e.Body, nil, &vv)
		if err != nil {
			return nil, fmt.Errorf("end: %v", err)
		}
		add(req)
	}
	return out, nil
}

func authShown(r *schemas.RemoteAgent) [][]string {
	hdr := r.AuthHeader
	if hdr == "" {
		hdr = "Authorization"
	}
	bearer := func(what string) [][]string {
		if strings.EqualFold(hdr, "Authorization") {
			return [][]string{{"Authorization", "Bearer <" + what + ">"}}
		}
		return [][]string{{hdr, "<" + what + ">"}}
	}
	if a := r.Auth; a != nil {
		switch a.Type {
		case schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra:
			return bearer("access token from " + hostOf(TokenURL(a)))
		case schemas.RemoteAuthGCPIDToken:
			return bearer("Google ID token for " + orDefault(a.Audience, "the service URL"))
		case schemas.RemoteAuthSigV4:
			return [][]string{{"Authorization", "AWS4-HMAC-SHA256 <signature, " + a.Region + "/" + a.Service + ">"}, {"X-Amz-Date", "<request time>"}}
		}
	}
	if r.AuthSecret != "" {
		return bearer("secret " + r.AuthSecret)
	}
	return nil
}

func mappingOf(r *schemas.RemoteAgent) *schemas.RemoteMapping {
	if r.Mapping != nil {
		return r.Mapping
	}
	return &schemas.RemoteMapping{}
}

func mergeHeaders(a, b map[string]string) map[string]string {
	if len(b) == 0 {
		return a
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// redactURL drops user info and masks query values that look like
// credentials.
func redactURL(u *url.URL) string {
	c := *u
	c.User = nil
	q := c.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "sig") || strings.Contains(lk, "code") {
			q.Set(k, "[redacted]")
		}
	}
	c.RawQuery = q.Encode()
	return c.String()
}

// Call sends the mapping's requests: start, the main request, then the
// stream or the polls, then end (best effort), and reads the answer.
func (c *Caller) Call(ctx context.Context, r *schemas.RemoteAgent, v *Vars) (*Exchange, error) {
	if err := Check(r); err != nil {
		return nil, err
	}
	base, err := url.Parse(r.URL)
	if err != nil {
		return nil, err
	}
	m := mappingOf(r)
	var tlsCfg *tls.Config
	if c.Auth != nil {
		if tlsCfg, err = c.Auth.TLSConfig(); err != nil {
			return nil, err
		}
	}
	client := c.Client(tlsCfg)
	ex := &Exchange{}
	vv := *v
	if st := m.Start; st != nil {
		req, err := renderRequest("start", st.Method, st.URL, base, mergeHeaders(m.Headers, st.Headers), nil, st.Body, nil, &vv)
		if err != nil {
			return ex, fmt.Errorf("start step: %v", err)
		}
		resp, raw, err := c.send(ctx, client, req, ex, false)
		if err != nil {
			return ex, fmt.Errorf("start step: %v", err)
		}
		switch {
		case resp.StatusCode == http.StatusConflict:
			c.step("note", "the start step answered 409: the session already exists, so it is reused")
		case resp.StatusCode/100 != 2:
			return ex, fmt.Errorf("the start step answered %d: %s", resp.StatusCode, Snippet(raw))
		case st.IDPath != "":
			doc, derr := Decode(raw)
			if derr != nil {
				return ex, errors.New("the start step's answer is not JSON, so id_path cannot read it")
			}
			p, _ := ParsePath(st.IDPath)
			if vv.StartID = Text(p.Eval(doc), ""); vv.StartID == "" {
				return ex, fmt.Errorf("the start step's answer has nothing at %s", st.IDPath)
			}
			c.step("note", "start id read from %s", st.IDPath)
		}
	}
	defer func() {
		if e := m.End; e != nil {
			req, err := renderRequest("end", orDefault(e.Method, http.MethodDelete), e.URL, base, mergeHeaders(m.Headers, e.Headers), nil, e.Body, nil, &vv)
			if err == nil {
				ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				_, _, _ = c.send(ectx, client, req, ex, false)
				cancel()
			}
		}
	}()
	req, err := renderRequest("request", m.Method, r.URL, base, m.Headers, m.Query, m.Body, c.DefaultBody, &vv)
	if err != nil {
		return ex, err
	}
	if m.Stream != "" {
		return c.stream(ctx, client, req, m, ex)
	}
	resp, raw, err := c.send(ctx, client, req, ex, true)
	if err != nil {
		return ex, err
	}
	ex.Status, ex.Raw, ex.ContentType, ex.Header = resp.StatusCode, raw, resp.Header.Get("Content-Type"), resp.Header
	if resp.StatusCode/100 != 2 {
		return ex, fmt.Errorf("the endpoint answered %d: %s", resp.StatusCode, Snippet(raw))
	}
	if p := m.Poll; p != nil {
		return c.poll(ctx, client, base, m, &vv, raw, ex)
	}
	if m.AnswerPath == "" {
		return ex, nil
	}
	ex.Mapped = true
	doc, err := Decode(raw)
	if err != nil {
		return ex, fmt.Errorf("the answer is not JSON, so answer_path %s cannot read it: %s", m.AnswerPath, Snippet(raw))
	}
	p, _ := ParsePath(m.AnswerPath)
	matches := p.Eval(doc)
	if len(matches) == 0 {
		return ex, fmt.Errorf("the answer has nothing at %s: %s", m.AnswerPath, Snippet(raw))
	}
	ex.Answer = Text(matches, "\n")
	return ex, nil
}

func (c *Caller) send(ctx context.Context, client *http.Client, r *request, ex *Exchange, main bool) (*http.Response, []byte, error) {
	resp, err := c.open(ctx, client, r, ex, main)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxAnswerBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read the answer: %v", err)
	}
	if len(raw) > MaxAnswerBytes {
		return nil, nil, fmt.Errorf("the answer is larger than %d bytes", MaxAnswerBytes)
	}
	c.step("response", "%s %d %s, %d bytes", r.step, resp.StatusCode, resp.Header.Get("Content-Type"), len(raw))
	return resp, raw, nil
}

// open sends r and returns the open response.
func (c *Caller) open(ctx context.Context, client *http.Client, r *request, ex *Exchange, main bool) (*http.Response, error) {
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, r.url.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build the %s request: %v", r.step, err)
	}
	req.Header.Set("Accept", "application/json, text/event-stream, text/plain")
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range r.headers {
		req.Header.Set(h[0], h[1])
	}
	if c.Token != "" {
		if c.AuthHeader == "" || strings.EqualFold(c.AuthHeader, "Authorization") {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		} else {
			req.Header.Set(c.AuthHeader, c.Token)
		}
	}
	if main && c.Sign != nil && r.body != nil {
		c.Sign(req, r.body)
	}
	if c.Auth != nil {
		before := c.Auth.Fetches
		if err := c.Auth.Apply(ctx, req, r.body, c.AuthHeader); err != nil {
			return nil, err
		}
		if c.Auth.Fetches > before {
			c.step("auth", "fetched a token from %s", hostOf(TokenURL(c.Auth.spec)))
		}
	}
	shown := Sent{Step: r.step, Method: r.method, URL: redactURL(r.url), Headers: append([][]string{}, r.shown...), Body: string(r.body)}
	for _, k := range []string{"Authorization", "X-Amz-Date", c.AuthHeader} {
		if k != "" && req.Header.Get(k) != "" {
			shown.Headers = append(shown.Headers, []string{k, "[redacted]"})
		}
	}
	ex.Sent = append(ex.Sent, shown)
	c.step(r.step, "%s %s, %d byte body", r.method, redactURL(r.url), len(r.body))
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the %s request did not answer: %s", r.step, scrubErr(err, r.url.String()))
	}
	return resp, nil
}

func (c *Caller) stream(ctx context.Context, client *http.Client, r *request, m *schemas.RemoteMapping, ex *Exchange) (*Exchange, error) {
	resp, err := c.open(ctx, client, r, ex, true)
	if err != nil {
		return ex, err
	}
	defer resp.Body.Close()
	ex.Status, ex.ContentType, ex.Header, ex.Mapped = resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header, true
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		ex.Raw = raw
		return ex, fmt.Errorf("the endpoint answered %d: %s", resp.StatusCode, Snippet(raw))
	}
	var rd StreamReader
	if m.StreamPath != "" {
		rd.Path, _ = ParsePath(m.StreamPath)
	}
	rd.Decode = m.StreamDecode
	var keep bytes.Buffer
	body := io.TeeReader(io.LimitReader(resp.Body, MaxAnswerBytes), &capWriter{b: &keep, max: 64 << 10})
	var text string
	if m.Stream == schemas.RemoteStreamAWSEventStream {
		text, ex.Events, err = ReadAWSEventStream(body, rd)
	} else {
		text, ex.Events, err = ReadSSE(body, rd)
	}
	ex.Raw = keep.Bytes()
	c.step("response", "stream %d, %d events", resp.StatusCode, ex.Events)
	if err != nil {
		return ex, err
	}
	if text == "" {
		return ex, fmt.Errorf("the stream carried no text at %s (%d events)", orDefault(m.StreamPath, "its data lines"), ex.Events)
	}
	ex.Answer = text
	return ex, nil
}

func (c *Caller) poll(ctx context.Context, client *http.Client, base *url.URL, m *schemas.RemoteMapping, v *Vars, first []byte, ex *Exchange) (*Exchange, error) {
	p := m.Poll
	ex.Mapped = true
	if p.ReplyIDPath != "" {
		doc, err := Decode(first)
		if err != nil {
			return ex, errors.New("the answer is not JSON, so poll reply_id_path cannot read it")
		}
		rp, _ := ParsePath(p.ReplyIDPath)
		if v.ReplyID = Text(rp.Eval(doc), ""); v.ReplyID == "" {
			return ex, fmt.Errorf("the answer has nothing at %s", p.ReplyIDPath)
		}
	}
	pathText, err := RenderText(p.AnswerPath, v, false, nil)
	if err != nil {
		return ex, err
	}
	ap, err := ParsePath(pathText)
	if err != nil {
		return ex, fmt.Errorf("poll answer_path: %v", err)
	}
	statusPath, err := pollPath("status_path", p.StatusPath, v)
	if err != nil {
		return ex, err
	}
	completePath, err := pollPath("complete_path", p.CompletePath, v)
	if err != nil {
		return ex, err
	}
	explicit := statusPath != nil || completePath != nil
	interval := time.Duration(max(p.IntervalS, 1)) * time.Second
	settled := ""
	for i := 0; i < MaxPolls; i++ {
		if err := c.sleep(ctx, interval); err != nil {
			return ex, incompleteAnswer(ex.Polls)
		}
		req, err := renderRequest("poll", http.MethodGet, p.URL, base, m.Headers, nil, "", nil, v)
		if err != nil {
			return ex, err
		}
		resp, raw, err := c.send(ctx, client, req, ex, false)
		if err != nil {
			return ex, err
		}
		ex.Polls++
		ex.Status, ex.Raw, ex.ContentType, ex.Header = resp.StatusCode, raw, resp.Header.Get("Content-Type"), resp.Header
		if resp.StatusCode/100 != 2 {
			return ex, fmt.Errorf("the poll answered %d: %s", resp.StatusCode, Snippet(raw))
		}
		doc, err := Decode(raw)
		if err != nil {
			continue
		}
		text := Text(ap.Eval(doc), "\n")
		if explicit {
			done := false
			if statusPath != nil {
				state := strings.ToLower(strings.TrimSpace(Text(statusPath.Eval(doc), "")))
				if hasState(p.FailStates, state) {
					return ex, fmt.Errorf("the agent said %s", state)
				}
				done = hasState(p.DoneStates, state)
			}
			if completePath != nil && truthy(Text(completePath.Eval(doc), "")) {
				done = true
			}
			if done && text != "" {
				ex.Answer = text
				return ex, nil
			}
			continue
		}
		if text == "" {
			continue
		}
		if text == settled { // the same answer twice: the agent is done
			ex.Answer = text
			return ex, nil
		}
		settled = text
	}
	// Running out of polls without a finished answer is an incomplete run,
	// never a graded one: a half answer would count as a model failure.
	return ex, incompleteAnswer(ex.Polls)
}

// incompleteAnswer is the error of a poll that ran out of time.
func incompleteAnswer(polls int) error {
	return fmt.Errorf("incomplete answer: the agent did not finish within the time limit (%d polls)", polls)
}

// pollPath renders and parses an optional poll path; "" gives nil.
func pollPath(name, raw string, v *Vars) (*Path, error) {
	if raw == "" {
		return nil, nil
	}
	text, err := RenderText(raw, v, false, nil)
	if err != nil {
		return nil, err
	}
	pp, err := ParsePath(text)
	if err != nil {
		return nil, fmt.Errorf("poll %s: %v", name, err)
	}
	return pp, nil
}

// hasState reports whether state is one of states, without case.
func hasState(states []string, state string) bool {
	for _, s := range states {
		if strings.EqualFold(strings.TrimSpace(s), state) {
			return state != ""
		}
	}
	return false
}

// truthy is the complete_path rule: true, or a non-empty string.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "null", "0":
		return false
	}
	return true
}

type capWriter struct {
	b   *bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.b.Len(); room > 0 {
		w.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// Snippet is a short, single-line, printable excerpt of a body.
func Snippet(raw []byte) string {
	s := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 {
			return -1
		}
		return r
	}, string(raw))
	return truncate(strings.TrimSpace(s), 300)
}
