package connect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// One request of a config, sent the way a run's launcher sends it: the Y10
// mapping (body template, headers, query, answer path, stream, start, poll
// and end steps) or Heimdall's own shape, the static token or OAuth 2 client
// credentials, the HMAC signature. Used by the nonce probe and by Verify.

// maxBody caps one answer read.
const maxBody = 1 << 20

// UserAgent is sent on every connect request so a customer can recognise
// and allow it (section 9).
const UserAgent = "heimdall-connect/1"

// Vars are the values of one request's placeholders.
type Vars struct {
	Instruction  string
	Messages     []map[string]string // the conversation so far, the new user turn last
	SessionID    string
	SessionIndex int
	Model        string
	StartID      string
}

// Exchange is one request and what came back.
type Exchange struct {
	Method    string
	URL       string // redacted
	Status    int
	Header    http.Header
	Body      []byte
	Doc       any   // the JSON answer, nil when not JSON
	Events    []any // stream events (JSON or text)
	Answer    string
	Found     bool // the answer path (or Heimdall's own answer field) matched
	Streamed  bool
	SentToken bool
	TTFBMS    int64
	TotalMS   int64
	Version   string // X-Agent-Version header or the answer's version field
	Err       error
}

// Sender sends requests of one agent config.
type Sender struct {
	Client  *http.Client
	Config  *schemas.AgentConfig
	Secrets map[string]string
	Now     func() time.Time
	// Log records every request as a probe (evidence).
	Log func(Probe)

	token string // OAuth 2 token, fetched once
}

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// placeholder value lookup. secrets allows {{secret.NAME}}.
func (s *Sender) lookup(name string, v Vars, secrets bool) (any, error) {
	switch name {
	case "instruction":
		return v.Instruction, nil
	case "messages":
		msgs := v.Messages
		if len(msgs) == 0 {
			msgs = []map[string]string{{"role": "user", "content": v.Instruction}}
		}
		out := make([]any, 0, len(msgs))
		for _, m := range msgs {
			out = append(out, map[string]any{"role": m["role"], "content": m["content"]})
		}
		return out, nil
	case "tools":
		return map[string]any{}, nil
	case "metadata":
		return map[string]any{"session_id": v.SessionID, "session_index": v.SessionIndex, "output": schemas.OutputAnswer, "task": "connect-check", "run": "connect-check"}, nil
	case "gateway":
		return nil, nil
	case "session_id":
		return v.SessionID, nil
	case "session_index":
		return v.SessionIndex, nil
	case "output":
		return schemas.OutputAnswer, nil
	case "model":
		if s.Config != nil {
			return s.Config.Model, nil
		}
		return "", nil
	case "uuid":
		return newID(), nil
	case "start.id":
		return v.StartID, nil
	case "reply.id":
		return "", nil
	}
	if n, ok := strings.CutPrefix(name, "secret."); ok {
		if !secrets {
			return nil, fmt.Errorf("{{secret.%s}} is allowed in headers and query only", n)
		}
		val, ok := s.Secrets[n]
		if !ok || val == "" {
			return nil, fmt.Errorf("secret %s is not set: add it on Settings > Secrets", n)
		}
		return val, nil
	}
	return nil, fmt.Errorf("unknown placeholder {{%s}}", name)
}

// renderText fills every placeholder in t as text.
func (s *Sender) renderText(t string, v Vars, secrets bool, escape func(string) string) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(t, "{{")
		if i < 0 {
			b.WriteString(t)
			return b.String(), nil
		}
		j := strings.Index(t[i:], "}}")
		if j < 0 {
			b.WriteString(t)
			return b.String(), nil
		}
		b.WriteString(t[:i])
		val, err := s.lookup(strings.TrimSpace(t[i+2:i+j]), v, secrets)
		if err != nil {
			return "", err
		}
		txt := textOf(val)
		if escape != nil {
			txt = escape(txt)
		}
		b.WriteString(txt)
		t = t[i+j+2:]
	}
}

// renderBody fills a JSON body template: a string that is exactly one
// placeholder takes the placeholder's JSON value.
func (s *Sender) renderBody(tmpl string, v Vars) ([]byte, error) {
	var doc any
	if err := json.Unmarshal([]byte(tmpl), &doc); err != nil {
		return nil, fmt.Errorf("the request template is not JSON: %v", err)
	}
	var walk func(n any) (any, error)
	walk = func(n any) (any, error) {
		switch t := n.(type) {
		case string:
			trim := strings.TrimSpace(t)
			if strings.HasPrefix(trim, "{{") && strings.HasSuffix(trim, "}}") && strings.Count(trim, "{{") == 1 {
				return s.lookup(strings.TrimSpace(trim[2:len(trim)-2]), v, false)
			}
			return s.renderText(t, v, false, nil)
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, c := range t {
				r, err := walk(c)
				if err != nil {
					return nil, err
				}
				out[k] = r
			}
			return out, nil
		case []any:
			out := make([]any, len(t))
			for i, c := range t {
				r, err := walk(c)
				if err != nil {
					return nil, err
				}
				out[i] = r
			}
			return out, nil
		}
		return n, nil
	}
	r, err := walk(doc)
	if err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// ownBody is Heimdall's own request shape (docs/remote-execution.md).
func ownBody(v Vars) []byte {
	body := map[string]any{"instruction": v.Instruction, "session_id": v.SessionID, "session_index": v.SessionIndex, "output": schemas.OutputAnswer}
	if len(v.Messages) > 1 {
		body["messages"] = v.Messages
	}
	b, _ := json.Marshal(body)
	return b
}

// Send sends one turn of the config's agent and reads the answer.
func (s *Sender) Send(ctx context.Context, v Vars) Exchange {
	cfg := s.Config
	switch {
	case cfg == nil:
		return Exchange{Err: errors.New("no agent config")}
	case cfg.OpenAI != nil && (cfg.Adapter == "openai_agent" || cfg.Remote == nil):
		return s.sendOpenAI(ctx, v)
	case cfg.A2A != nil && cfg.Adapter == "a2a":
		return s.sendA2A(ctx, v)
	case cfg.Remote != nil:
		return s.sendRemote(ctx, v)
	}
	return Exchange{Err: errors.New("this agent is started in a container by Heimdall: it has no endpoint to call; the real task row checks it")}
}

func (s *Sender) stepURL(raw string, base *url.URL, v Vars) (*url.URL, error) {
	r, err := s.renderText(raw, v, false, url.PathEscape)
	if err != nil {
		return nil, err
	}
	u, err := base.Parse(r)
	if err != nil {
		return nil, err
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return nil, fmt.Errorf("step URL %s leaves the agent's host %s", redactURL(u.String()), base.Host)
	}
	return u, nil
}

func (s *Sender) sendRemote(ctx context.Context, v Vars) Exchange {
	r := s.Config.Remote
	m := r.Mapping
	if m == nil {
		m = &schemas.RemoteMapping{}
	}
	base, err := url.Parse(r.URL)
	if err != nil || base.Host == "" {
		return Exchange{Err: fmt.Errorf("the agent URL %q is not a URL", redactURL(r.URL))}
	}
	if m.Start != nil {
		u, err := s.stepURL(m.Start.URL, base, v)
		if err != nil {
			return Exchange{Err: err}
		}
		body := []byte(nil)
		if m.Start.Body != "" {
			if body, err = s.renderBody(m.Start.Body, v); err != nil {
				return Exchange{Err: err}
			}
		}
		ex := s.do(ctx, methodOr(m.Start.Method, "POST"), u, m.Start.Headers, body, v)
		if ex.Err != nil || ex.Status < 200 || ex.Status > 299 {
			if ex.Err == nil {
				ex.Err = fmt.Errorf("the start step answered %d: %s", ex.Status, capText(string(ex.Body), 200))
			}
			return ex
		}
		if m.Start.IDPath != "" {
			id, ok := PathText(ex.Doc, m.Start.IDPath)
			if !ok {
				ex.Err = fmt.Errorf("the start step's answer has nothing at %s", m.Start.IDPath)
				return ex
			}
			v.StartID = id
		}
	}
	main := *base
	if len(m.Query) > 0 {
		q := main.Query()
		for _, k := range sortedKeys(m.Query) {
			val, err := s.renderText(m.Query[k], v, true, nil)
			if err != nil {
				return Exchange{Err: err}
			}
			q.Set(k, val)
		}
		main.RawQuery = q.Encode()
	}
	var body []byte
	if m.Body != "" {
		if body, err = s.renderBody(m.Body, v); err != nil {
			return Exchange{Err: err}
		}
	} else {
		body = ownBody(v)
	}
	method := methodOr(m.Method, "POST")
	if method == http.MethodGet {
		body = nil
	}
	ex := s.do(ctx, method, &main, m.Headers, body, v)
	if ex.Err != nil || ex.Status < 200 || ex.Status > 299 {
		return ex
	}
	s.readAnswer(&ex, m)
	if m.Poll != nil && !ex.Found {
		s.poll(ctx, &ex, m.Poll, base, v)
	}
	if m.End != nil {
		if u, err := s.stepURL(m.End.URL, base, v); err == nil {
			var eb []byte
			if m.End.Body != "" {
				eb, _ = s.renderBody(m.End.Body, v)
			}
			_ = s.do(ctx, methodOr(m.End.Method, "DELETE"), u, m.End.Headers, eb, v)
		}
	}
	return ex
}

func methodOr(m, def string) string {
	if m = strings.ToUpper(strings.TrimSpace(m)); m != "" {
		return m
	}
	return def
}

func (s *Sender) poll(ctx context.Context, ex *Exchange, p *schemas.RemotePoll, base *url.URL, v Vars) {
	every := time.Duration(p.IntervalS) * time.Second
	if every <= 0 {
		every = time.Second
	}
	for i := 0; i < 20; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		u, err := s.stepURL(p.URL, base, v)
		if err != nil {
			ex.Err = err
			return
		}
		pe := s.do(ctx, http.MethodGet, u, nil, nil, v)
		if pe.Err != nil {
			ex.Err = pe.Err
			return
		}
		if txt, ok := PathText(pe.Doc, p.AnswerPath); ok && strings.TrimSpace(txt) != "" {
			ex.Answer, ex.Found, ex.Doc, ex.TotalMS = txt, true, pe.Doc, pe.TotalMS+ex.TotalMS
			return
		}
	}
}

// readAnswer reads ex's answer by the mapping, or by Heimdall's own shape.
func (s *Sender) readAnswer(ex *Exchange, m *schemas.RemoteMapping) {
	if ex.Streamed || m.Stream != "" {
		var parts []string
		for _, e := range ex.Events {
			var piece string
			switch {
			case m.StreamPath != "":
				t, ok := PathText(e, m.StreamPath)
				if !ok {
					continue
				}
				piece = t
			default:
				if str, ok := e.(string); ok {
					piece = str
				}
			}
			if m.StreamDecode == "base64" {
				if b, err := base64.StdEncoding.DecodeString(piece); err == nil {
					piece = string(b)
				}
			}
			parts = append(parts, piece)
		}
		ex.Answer = strings.Join(parts, "")
		ex.Found = len(parts) > 0
		return
	}
	if m.AnswerPath != "" {
		if ex.Doc == nil {
			return
		}
		ex.Answer, ex.Found = PathText(ex.Doc, m.AnswerPath)
		return
	}
	if obj, ok := ex.Doc.(map[string]any); ok {
		if a, ok := obj["answer"]; ok {
			ex.Answer, ex.Found = textOf(a), true
		}
		return
	}
	if ex.Doc == nil && !strings.Contains(ex.Header.Get("Content-Type"), "json") {
		ex.Answer = strings.TrimSpace(string(ex.Body))
		ex.Found = ex.Answer != ""
	}
}

// do sends one request with the config's auth and signature and reads the
// answer (JSON, a stream or text).
func (s *Sender) do(ctx context.Context, method string, u *url.URL, headers map[string]string, body []byte, v Vars) Exchange {
	ex := Exchange{Method: method, URL: redactURL(u.String())}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		ex.Err = err
		return ex
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json, text/event-stream, text/plain")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, k := range sortedKeys(headers) {
		val, err := s.renderText(headers[k], v, true, nil)
		if err != nil {
			ex.Err = err
			return ex
		}
		req.Header.Set(k, val)
	}
	if err := s.authorize(ctx, req, body); err != nil {
		ex.Err = err
		return ex
	}
	ex.SentToken = req.Header.Get("Authorization") != "" || (s.Config.Remote != nil && s.Config.Remote.AuthHeader != "" && req.Header.Get(s.Config.Remote.AuthHeader) != "")
	start := s.now()
	resp, err := s.Client.Do(req)
	if err != nil {
		ex.Err = err
		ex.TotalMS = s.now().Sub(start).Milliseconds()
		s.log(ex, "")
		return ex
	}
	defer resp.Body.Close()
	ex.TTFBMS = s.now().Sub(start).Milliseconds()
	ex.Status, ex.Header = resp.StatusCode, resp.Header
	ex.Version = resp.Header.Get("X-Agent-Version")
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		ex.Streamed = true
		ex.Events, ex.Body, ex.Err = readSSE(io.LimitReader(resp.Body, maxBody))
	} else {
		ex.Body, ex.Err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
		var doc any
		if json.Unmarshal(ex.Body, &doc) == nil {
			ex.Doc = doc
			if obj, ok := doc.(map[string]any); ok && ex.Version == "" {
				if vv, ok := obj["version"].(string); ok {
					ex.Version = vv
				}
			}
		}
	}
	ex.TotalMS = s.now().Sub(start).Milliseconds()
	s.log(ex, "")
	return ex
}

func (s *Sender) log(ex Exchange, note string) {
	if s.Log == nil {
		return
	}
	if ex.Err != nil && note == "" {
		note = capText(scrub(ex.Err.Error(), s.Secrets), 200)
	}
	s.Log(Probe{Method: ex.Method, URL: ex.URL, Status: ex.Status, MS: ex.TotalMS, Note: note})
}

// scrub removes every secret value from text.
func scrub(text string, secrets map[string]string) string {
	for _, v := range secrets {
		if len(v) >= 4 {
			text = strings.ReplaceAll(text, v, "[secret]")
		}
	}
	return text
}

// readSSE reads a text/event-stream body: each data payload as JSON when it
// parses, else as text. [DONE] ends the stream.
func readSSE(r io.Reader) ([]any, []byte, error) {
	var events []any
	var raw bytes.Buffer
	sc := bufio.NewScanner(io.TeeReader(r, &raw))
	sc.Buffer(make([]byte, 64<<10), maxBody)
	var data []string
	flush := func() {
		if len(data) == 0 {
			return
		}
		payload := strings.Join(data, "\n")
		data = nil
		if strings.TrimSpace(payload) == "[DONE]" {
			return
		}
		var doc any
		if json.Unmarshal([]byte(payload), &doc) == nil {
			events = append(events, doc)
		} else {
			events = append(events, payload)
		}
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if d, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(d, " "))
		}
	}
	flush()
	return events, raw.Bytes(), sc.Err()
}

// authorize sets the config's auth: a static token from AuthSecret (in
// AuthHeader or as a bearer), an OAuth 2 or Entra client credentials token,
// and the HMAC signature of SignSecret.
func (s *Sender) authorize(ctx context.Context, req *http.Request, body []byte) error {
	cfg := s.Config
	switch {
	case cfg.OpenAI != nil && cfg.Remote == nil:
		if k := s.Secrets[cfg.OpenAI.APIKeySecret]; k != "" {
			req.Header.Set("Authorization", "Bearer "+k)
		}
		return nil
	case cfg.Remote == nil:
		return nil
	}
	r := cfg.Remote
	if a := r.Auth; a != nil && a.Type != "" {
		switch a.Type {
		case schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra:
			tok, err := s.oauthToken(ctx, a)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		default:
			return fmt.Errorf("%s auth is checked by the real task row (the launcher signs each request)", a.Type)
		}
	}
	name := r.AuthSecret
	if name == "" {
		name = r.AuthEnv
	}
	if tok := s.Secrets[name]; tok != "" {
		if h := r.AuthHeader; h != "" && !strings.EqualFold(h, "Authorization") {
			req.Header.Set(h, tok)
		} else {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	sign := r.SignSecret
	if sign == "" {
		sign = r.SignEnv
	}
	if secret := s.Secrets[sign]; secret != "" && body != nil {
		ts := s.now().Unix()
		req.Header.Set("X-Heimdall-Timestamp", strconv.FormatInt(ts, 10))
		req.Header.Set("X-Heimdall-Signature", webhooks.Sign(secret, ts, body))
	}
	return nil
}

func (s *Sender) oauthToken(ctx context.Context, a *schemas.RemoteAuth) (string, error) {
	if s.token != "" {
		return s.token, nil
	}
	tokenURL := a.TokenURL
	if tokenURL == "" && a.Type == schemas.RemoteAuthEntra && a.TenantID != "" {
		tokenURL = "https://login.microsoftonline.com/" + url.PathEscape(a.TenantID) + "/oauth2/v2.0/token"
	}
	if !strings.HasPrefix(tokenURL, "https://") {
		return "", errors.New("the token URL must be https")
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {s.Secrets[a.ClientIDSecret]}, "client_secret": {s.Secrets[a.ClientSecretSecret]}}
	if a.Scope != "" {
		form.Set("scope", a.Scope)
	}
	if a.Audience != "" {
		form.Set("audience", a.Audience)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the token endpoint did not answer: %s", scrub(err.Error(), s.Secrets))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var doc struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != 200 || json.Unmarshal(raw, &doc) != nil || doc.AccessToken == "" {
		return "", fmt.Errorf("the token endpoint answered %d without an access_token", resp.StatusCode)
	}
	s.token = doc.AccessToken
	return s.token, nil
}

// OpenAIEndpoint is the chat/completions (or responses) URL of an OpenAI
// style base URL.
func OpenAIEndpoint(base string) string {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/chat/completions") || strings.HasSuffix(b, "/responses") {
		return b
	}
	return b + "/chat/completions"
}

func (s *Sender) sendOpenAI(ctx context.Context, v Vars) Exchange {
	o := s.Config.OpenAI
	u, err := url.Parse(OpenAIEndpoint(o.URL))
	if err != nil || u.Host == "" {
		return Exchange{Err: fmt.Errorf("the base URL %q is not a URL", redactURL(o.URL))}
	}
	model := o.Model
	if model == "" {
		model = s.Config.Model
	}
	msgs := v.Messages
	if len(msgs) == 0 {
		msgs = []map[string]string{{"role": "user", "content": v.Instruction}}
	}
	var body []byte
	responses := strings.HasSuffix(u.Path, "/responses") || o.API == "responses"
	if responses {
		body, _ = json.Marshal(map[string]any{"model": model, "input": msgs})
	} else {
		body, _ = json.Marshal(map[string]any{"model": model, "messages": msgs})
	}
	ex := s.do(ctx, http.MethodPost, u, nil, body, v)
	if ex.Err != nil || ex.Status < 200 || ex.Status > 299 {
		return ex
	}
	path := "choices[0].message.content"
	if responses {
		path = "output[*].content[*].text"
		if t, ok := PathText(ex.Doc, "output_text"); ok && t != "" {
			ex.Answer, ex.Found = t, true
			return ex
		}
	}
	ex.Answer, ex.Found = PathText(ex.Doc, path)
	return ex
}

// sendA2A sends message/send (JSON-RPC) to the agent card's endpoint.
func (s *Sender) sendA2A(ctx context.Context, v Vars) Exchange {
	card, err := url.Parse(s.Config.A2A.CardURL)
	if err != nil || card.Host == "" {
		return Exchange{Err: errors.New("the agent card URL is not a URL")}
	}
	cex := s.do(ctx, http.MethodGet, card, nil, nil, v)
	if cex.Err != nil || cex.Status != 200 {
		if cex.Err == nil {
			cex.Err = fmt.Errorf("the agent card answered %d", cex.Status)
		}
		return cex
	}
	endpoint := a2aEndpoint(cex.Doc)
	if endpoint == "" {
		return Exchange{Err: errors.New("the agent card names no endpoint (url or supportedInterfaces)")}
	}
	eu, err := card.Parse(endpoint)
	if err != nil {
		return Exchange{Err: err}
	}
	rpc := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": map[string]any{
		"message": map[string]any{"role": "user", "messageId": newID(), "contextId": v.SessionID,
			"parts": []any{map[string]any{"kind": "text", "text": v.Instruction}}}}}
	body, _ := json.Marshal(rpc)
	ex := s.do(ctx, http.MethodPost, eu, nil, body, v)
	if ex.Err == nil && ex.Status >= 200 && ex.Status < 300 && ex.Doc != nil {
		var texts []string
		for _, f := range stringLeaves(ex.Doc) {
			if strings.HasSuffix(f.path, ".text") {
				texts = append(texts, f.value)
			}
		}
		ex.Answer = strings.Join(texts, "\n")
		ex.Found = len(texts) > 0
	}
	return ex
}

// a2aEndpoint reads the JSON-RPC endpoint of an agent card.
func a2aEndpoint(card any) string {
	m, ok := card.(map[string]any)
	if !ok {
		return ""
	}
	if ifs, ok := m["supportedInterfaces"].([]any); ok {
		for _, i := range ifs {
			if im, ok := i.(map[string]any); ok {
				b, _ := im["protocolBinding"].(string)
				if u, _ := im["url"].(string); u != "" && (b == "" || strings.EqualFold(b, "JSONRPC")) {
					return u
				}
			}
		}
	}
	u, _ := m["url"].(string)
	return u
}
