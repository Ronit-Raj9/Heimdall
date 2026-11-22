package handlers

// Test my endpoint (lane X10, J3): POST /agents/test-endpoint sends one real
// sample task to an HTTP or OpenAI-compatible endpoint agent, exactly as a
// run's launcher would (adapters/remotehttp.py, adapters/openaiagent.py:
// the same body, auth header and HMAC signature), and returns the answer
// with a step-by-step trace, in at most EndpointTestMaxWait. It works on a
// saved agent ({"agent": id}) or on the form before Create ({"template":
// {...}}, rendered like a dry run). Secrets come from the org's secrets;
// hosted servers only reach public https addresses.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/remotemap"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// EndpointTestMaxWait caps one endpoint test, polling included.
const EndpointTestMaxWait = 110 * time.Second

// EndpointTestInstruction is the sample task every test sends.
const EndpointTestInstruction = "This is a connection test from Heimdall. Reply with the single word READY."

const endpointTestMaxBody = 1 << 20

// EndpointTestRequest is POST /agents/test-endpoint: a saved agent or the
// new-agent form (a template request; it is rendered, never written).
type EndpointTestRequest struct {
	Agent    string                    `json:"agent,omitempty"`
	Template *AgentFromTemplateRequest `json:"template,omitempty"`
}

// EndpointTestStep is one row of the trace.
type EndpointTestStep struct {
	AtMS   int64  `json:"at_ms"`
	Kind   string `json:"kind"` // request | response | poll | answer | event | note | error
	Detail string `json:"detail"`
}

// EndpointTestResult is the answer of POST /agents/test-endpoint. OK means
// the endpoint answered the sample task; Matched that the answer holds
// READY. Reported is what the endpoint said about itself (never trusted).
type EndpointTestResult struct {
	OK        bool               `json:"ok"`
	Matched   bool               `json:"matched"`
	Adapter   string             `json:"adapter"`
	URL       string             `json:"url"`
	Status    int                `json:"status,omitempty"`
	LatencyMS int64              `json:"latency_ms"`
	Answer    string             `json:"answer,omitempty"`
	Error     string             `json:"error,omitempty"`
	Reported  map[string]any     `json:"reported,omitempty"`
	Trace     []EndpointTestStep `json:"trace"`
	Notes     []string           `json:"notes"`
	// Requests are the requests sent (secret values masked), RawResponse
	// the endpoint's last answer as received (capped) and Reads how the
	// answer was read (lane Y10, a mapped endpoint).
	Requests    []remotemap.Sent `json:"requests,omitempty"`
	RawResponse string           `json:"raw_response,omitempty"`
	Reads       string           `json:"reads,omitempty"`
}

type agentTestHandler struct {
	env    OrgEnv
	client func(hosted bool, timeout time.Duration) *http.Client
	now    func() time.Time
}

func init() {
	RegisterOrgArea("agenttest", func(env OrgEnv, r *router.Router) {
		h := &agentTestHandler{env: env, client: endpointTestClient, now: time.Now}
		r.POST(APIPrefix+"/agents/test-endpoint", env.W(h.post))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/test-endpoint", Tag: "agents",
			Summary: "Send one sample task to an HTTP, OpenAI-compatible or A2A endpoint agent (saved, or the new-agent form) and return its answer and a trace",
			Request: EndpointTestRequest{}, Response: EndpointTestResult{}},
	)
}

// endpointTestClient refuses private and local addresses on hosted servers
// (checked again at dial time) and never follows redirects.
func endpointTestClient(hosted bool, timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if hosted {
		d = webhooks.GuardedDialer(10 * time.Second)
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, Proxy: nil}
	return &http.Client{Timeout: timeout, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *agentTestHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *agentTestHandler) post(ctx *fasthttp.RequestCtx) {
	var req EndpointTestRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	var cfg *schemas.AgentConfig
	switch {
	case req.Template != nil && req.Agent == "":
		t, ok := findTemplate(req.Template.Template)
		if !ok {
			h.bad(ctx, fmt.Sprintf("unknown template %q", req.Template.Template))
			return
		}
		id := strings.TrimSpace(req.Template.ID)
		if !agentIDRe.MatchString(id) {
			id = "endpoint-test"
		}
		r := *req.Template
		r.ID = id
		files, err := renderAgentTemplate(t, r)
		if err != nil {
			h.bad(ctx, err.Error())
			return
		}
		if cfg, err = decodeAgent(files[0].Data, "agents/"+id+".yaml"); err != nil {
			h.bad(ctx, "the agent does not validate: "+err.Error())
			return
		}
	case req.Agent != "" && req.Template == nil:
		if !agentOpsNamePattern.MatchString(req.Agent) {
			h.bad(ctx, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", req.Agent))
			return
		}
		c, err := (&agentCompatHandler{env: h.env}).agentConfig(ctx, req.Agent)
		if err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
			return
		}
		cfg = c
	default:
		h.bad(ctx, "send agent (a saved agent) or template (the new-agent form), not both")
		return
	}
	work, cancel := context.WithTimeout(context.Background(), EndpointTestMaxWait+5*time.Second)
	defer cancel()
	res := h.run(work, cfg)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, res)
}

// secrets resolves the named secrets from the org's secrets; a local server
// without them falls back to its own environment (as runs do).
func (h *agentTestHandler) secrets(ctx context.Context, names ...string) (map[string]string, error) {
	var want []string
	for _, n := range names {
		if n != "" {
			want = append(want, n)
		}
	}
	out, err := toolproxy.OrgSecretEnv(ctx, h.env.Services.Keys, h.env.Services.OrgID, want)
	if err != nil {
		return nil, err
	}
	for _, n := range want {
		if out[n] == "" && !h.env.Services.Hosted {
			if v := os.Getenv(n); v != "" {
				out[n] = v
			}
		}
		if out[n] == "" {
			return nil, fmt.Errorf("secret %s is not set: add it on Settings > Secrets", n)
		}
	}
	return out, nil
}

// run tests cfg. It never returns an error: what went wrong is in the
// result, with the trace up to that point.
func (h *agentTestHandler) run(ctx context.Context, cfg *schemas.AgentConfig) *EndpointTestResult {
	res := &EndpointTestResult{Adapter: cfg.Adapter, Trace: []EndpointTestStep{}, Notes: []string{}}
	start := h.now()
	// A dial error can carry the address a name resolved to (N104): the
	// error and, on a hosted server, every trace line show private addresses
	// as "a private address".
	step := func(kind, format string, a ...any) {
		detail := fmt.Sprintf(format, a...)
		if h.env.Services.Hosted {
			detail = utils.HidePrivateAddresses(detail)
		}
		res.Trace = append(res.Trace, EndpointTestStep{AtMS: h.now().Sub(start).Milliseconds(), Kind: kind, Detail: detail})
	}
	fail := func(format string, a ...any) *EndpointTestResult {
		res.Error = utils.HidePrivateAddresses(fmt.Sprintf(format, a...))
		step("error", "%s", res.Error)
		res.LatencyMS = h.now().Sub(start).Milliseconds()
		return res
	}
	switch {
	case utils.IsOpenAIAgent(cfg) && cfg.OpenAI != nil:
		return h.runOpenAI(ctx, cfg, res, step, fail, start)
	case cfg.Remote != nil && (cfg.Adapter == "remote_http" || cfg.Adapter == "") && remotemap.Mapped(cfg.Remote):
		return h.runMapped(ctx, cfg, res, step, fail, start)
	case cfg.Remote != nil && (cfg.Adapter == "remote_http" || cfg.Adapter == ""):
		return h.runRemote(ctx, cfg.Remote, res, step, fail, start)
	case cfg.VoicePlatform != nil: // agenttestvoice.go
		return h.runVoicePlatform(ctx, cfg.VoicePlatform, res, step, fail, start)
	case utils.IsA2A(cfg):
		return h.runA2A(ctx, cfg.A2A, res, step, fail, start) // Y03: straight to the agent's card and JSON-RPC endpoint (agenttest_a2a.go)
	}
	return fail("this agent is not an HTTP or OpenAI-compatible endpoint: Heimdall starts it in a container, so run Check on the agent's page instead")
}

func (h *agentTestHandler) checkURL(raw string) error {
	if err := webhooks.ValidateURL(raw, !h.env.Services.Hosted); err != nil {
		return errors.New(strings.TrimPrefix(strings.Replace(err.Error(), "this server only delivers to", "this server only calls", 1), webhooks.ErrInvalid.Error()+": "))
	}
	return nil
}

func endpointTimeout(s int) time.Duration {
	d := time.Duration(s) * time.Second
	if d <= 0 || d > EndpointTestMaxWait {
		d = EndpointTestMaxWait
	}
	return d
}

func (h *agentTestHandler) runRemote(ctx context.Context, r *schemas.RemoteAgent, res *EndpointTestResult, step func(string, string, ...any), fail func(string, ...any) *EndpointTestResult, start time.Time) *EndpointTestResult {
	res.Adapter, res.URL = "remote_http", redactEndpoint(r.URL)
	if err := h.checkURL(r.URL); err != nil {
		return fail("%v", err)
	}
	if r.AuthEnv != "" && r.AuthSecret == "" && h.env.Services.Hosted {
		return fail("auth_env reads the server's environment, which hosted orgs cannot use: set an auth token secret instead")
	}
	if r.SignEnv != "" && r.SignSecret == "" && h.env.Services.Hosted {
		return fail("sign_env reads the server's environment, which hosted orgs cannot use: set a signing secret instead")
	}
	tokName, signName := r.AuthSecret, r.SignSecret
	if tokName == "" {
		tokName = r.AuthEnv
	}
	if signName == "" {
		signName = r.SignEnv
	}
	sec, err := h.secrets(ctx, tokName, signName)
	if err != nil {
		return fail("%v", err)
	}
	body := map[string]any{"instruction": EndpointTestInstruction, "session_id": "heimdall-test-" + randHex(6), "session_index": 1, "output": schemas.OutputAnswer}
	if r.ModelTraffic == "gateway" {
		res.Notes = append(res.Notes, "A test carries no gateway block: your agent's model calls go through Heimdall only in real runs, so it should fall back to its own provider here or answer without a model.")
	}
	if r.Tools == schemas.RemoteToolsExpose {
		res.Notes = append(res.Notes, "A test carries no tools block: the task's tools are published only during real runs.")
	}
	payload, _ := json.Marshal(body)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(payload))
	if err != nil {
		return fail("build the request: %v", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json, text/plain")
	sent := []string{"Content-Type"}
	if tok := sec[tokName]; tok != "" {
		if hd := r.AuthHeader; hd != "" && !strings.EqualFold(hd, "Authorization") {
			hreq.Header.Set(hd, tok)
			sent = append(sent, hd)
		} else {
			hreq.Header.Set("Authorization", "Bearer "+tok)
			sent = append(sent, "Authorization (Bearer)")
		}
	}
	if secret := sec[signName]; secret != "" {
		if len(secret) < 16 {
			return fail("the signing secret is shorter than 16 characters")
		}
		ts := h.now().Unix()
		hreq.Header.Set("X-Heimdall-Timestamp", strconv.FormatInt(ts, 10))
		hreq.Header.Set("X-Heimdall-Signature", webhooks.Sign(secret, ts, payload))
		sent = append(sent, "X-Heimdall-Timestamp", "X-Heimdall-Signature")
	}
	wait := endpointTimeout(r.TimeoutS)
	deadline := start.Add(wait)
	client := h.client(h.env.Services.Hosted, wait)
	step("request", "POST %s with headers %s, %d byte body", res.URL, strings.Join(sent, ", "), len(payload))
	resp, raw, err := endpointDo(client, hreq)
	if err != nil {
		return fail("the endpoint did not answer: %s", etScrubURL(err, r.URL))
	}
	res.Status = resp.StatusCode
	step("response", "%d %s, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(raw))
	for resp.StatusCode == http.StatusAccepted {
		next := endpointStatusURL(r.URL, resp, raw)
		if next == "" {
			return fail("the endpoint answered 202 without a status URL (Location header, status_url or id)")
		}
		if h.now().After(deadline) {
			return fail("the endpoint was still working after %s", wait.Round(time.Second))
		}
		pause := 2 * time.Second
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 1 && s <= 30 {
			pause = time.Duration(s) * time.Second
		}
		select {
		case <-ctx.Done():
			return fail("the test ran out of time")
		case <-time.After(pause):
		}
		preq, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return fail("build the poll request: %v", err)
		}
		for _, k := range []string{"Authorization"} {
			if v := hreq.Header.Get(k); v != "" {
				preq.Header.Set(k, v)
			}
		}
		if hd := r.AuthHeader; hd != "" {
			if v := hreq.Header.Get(hd); v != "" {
				preq.Header.Set(hd, v)
			}
		}
		step("poll", "GET %s", redactEndpoint(next))
		if resp, raw, err = endpointDo(client, preq); err != nil {
			return fail("the status URL did not answer: %s", etScrubURL(err, next))
		}
		res.Status = resp.StatusCode
		step("response", "%d, %d bytes", resp.StatusCode, len(raw))
		if resp.StatusCode == http.StatusOK && remotePending(raw) {
			resp.StatusCode = http.StatusAccepted
		}
	}
	res.LatencyMS = h.now().Sub(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail("the endpoint answered %d: %s", resp.StatusCode, etSnippet(raw))
	}
	answer, reported, why := remoteAnswer(resp, raw)
	if why != "" {
		return fail("%s", why)
	}
	res.Reported = reported
	if evs, ok := reported["events"].([]any); ok {
		for i, e := range evs {
			if i == 20 {
				step("event", "%d more events", len(evs)-20)
				break
			}
			step("event", "%s", etEventLine(e))
		}
		reported["events"] = len(evs)
	}
	return finishEndpointTest(res, answer, step)
}

func finishEndpointTest(res *EndpointTestResult, answer string, step func(string, string, ...any)) *EndpointTestResult {
	res.OK, res.Answer = true, etCap(answer, 4000)
	res.Matched = strings.Contains(strings.ToUpper(answer), "READY")
	step("answer", "%s", etCap(answer, 300))
	if !res.Matched {
		res.Notes = append(res.Notes, "The endpoint answered, but not with READY. That is fine if it is not a general chat agent: the connection works.")
	}
	return res
}

func (h *agentTestHandler) runOpenAI(ctx context.Context, cfg *schemas.AgentConfig, res *EndpointTestResult, step func(string, string, ...any), fail func(string, ...any) *EndpointTestResult, start time.Time) *EndpointTestResult {
	o := cfg.OpenAI
	res.Adapter = utils.AdapterOpenAIAgent
	endpoint, err := utils.OpenAIEndpoint(o)
	if err != nil {
		return fail("%v", err)
	}
	res.URL = redactEndpoint(endpoint)
	if err := h.checkURL(endpoint); err != nil {
		return fail("%v", err)
	}
	sec, err := h.secrets(ctx, o.APIKeySecret)
	if err != nil {
		return fail("%v", err)
	}
	model := strings.TrimSpace(o.Model)
	if model == "" {
		model = cfg.Model
	}
	responses := strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/responses")
	var body map[string]any
	if responses {
		body = map[string]any{"model": model, "input": EndpointTestInstruction}
	} else {
		body = map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": EndpointTestInstruction}}}
	}
	payload, _ := json.Marshal(body)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fail("build the request: %v", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	sent := "Content-Type"
	if k := sec[o.APIKeySecret]; k != "" {
		hreq.Header.Set("Authorization", "Bearer "+k)
		sent += ", Authorization (Bearer)"
	}
	wait := endpointTimeout(o.TimeoutS)
	step("request", "POST %s (model %s) with headers %s", res.URL, model, sent)
	resp, raw, err := endpointDo(h.client(h.env.Services.Hosted, wait), hreq)
	if err != nil {
		return fail("the endpoint did not answer: %s", etScrubURL(err, endpoint))
	}
	res.Status = resp.StatusCode
	res.LatencyMS = h.now().Sub(start).Milliseconds()
	step("response", "%d %s, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(raw))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail("the endpoint answered %d: %s", resp.StatusCode, etSnippet(raw))
	}
	answer, usage, why := openAIAnswer(raw, responses)
	if why != "" {
		return fail("%s", why)
	}
	if usage != nil {
		res.Reported = map[string]any{"usage": usage}
	}
	return finishEndpointTest(res, answer, step)
}

func endpointDo(c *http.Client, req *http.Request) (*http.Response, []byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, endpointTestMaxBody+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > endpointTestMaxBody {
		return nil, nil, fmt.Errorf("the answer is larger than %d bytes", endpointTestMaxBody)
	}
	return resp, raw, nil
}

// endpointStatusURL is where a 202 answer is polled, on the same origin as
// base only (the launcher's rule): Location, else status_url, else
// <base>/<id>.
func endpointStatusURL(base string, resp *http.Response, raw []byte) string {
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	cand := resp.Header.Get("Location")
	if cand == "" {
		cand, _ = doc["status_url"].(string)
	}
	if cand == "" {
		if id, ok := doc["id"].(string); ok && id != "" {
			cand = strings.TrimRight(base, "/") + "/" + url.PathEscape(id)
		}
	}
	if cand == "" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u, err := b.Parse(cand)
	if err != nil || u.Scheme != b.Scheme || u.Host != b.Host {
		return ""
	}
	return u.String()
}

func remotePending(raw []byte) bool {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	s, _ := doc["status"].(string)
	return s == "queued" || s == "pending" || s == "running"
}

// remoteAnswer reads a remote_http answer: a JSON object with "answer" or a
// text/plain body. reported holds usage, events, version and build.
func remoteAnswer(resp *http.Response, raw []byte) (string, map[string]any, string) {
	ct := resp.Header.Get("Content-Type")
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		if strings.HasPrefix(ct, "text/plain") || !strings.Contains(ct, "json") {
			if s := strings.TrimSpace(string(raw)); s != "" {
				return s, nil, ""
			}
			return "", nil, "the endpoint answered with an empty body"
		}
		return "", nil, "the endpoint's answer is not a JSON object: " + etSnippet(raw)
	}
	if s, _ := doc["status"].(string); s == "failed" {
		return "", nil, "the endpoint reported status failed: " + etSnippet(raw)
	}
	reported := map[string]any{}
	for _, k := range []string{"usage", "events", "version", "build"} {
		if v, ok := doc[k]; ok {
			reported[k] = v
		}
	}
	switch a := doc["answer"].(type) {
	case string:
		return a, reported, ""
	case nil:
		return "", reported, `the endpoint's JSON has no "answer" field`
	default:
		b, _ := json.Marshal(a)
		return string(b), reported, ""
	}
}

// openAIAnswer reads a chat/completions or Responses answer.
func openAIAnswer(raw []byte, responses bool) (string, any, string) {
	var doc struct {
		Choices []struct {
			Message struct {
				Content   any   `json:"content"`
				ToolCalls []any `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage any `json:"usage"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", nil, "the endpoint's answer is not JSON: " + etSnippet(raw)
	}
	if responses {
		text := doc.OutputText
		for _, o := range doc.Output {
			for _, c := range o.Content {
				text += c.Text
			}
		}
		if text == "" {
			return "", doc.Usage, "the Responses answer has no output text"
		}
		return text, doc.Usage, ""
	}
	if len(doc.Choices) == 0 {
		return "", doc.Usage, "the answer has no choices"
	}
	switch c := doc.Choices[0].Message.Content.(type) {
	case string:
		if c != "" {
			return c, doc.Usage, ""
		}
	case []any:
		var b strings.Builder
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		if b.Len() > 0 {
			return b.String(), doc.Usage, ""
		}
	}
	if len(doc.Choices[0].Message.ToolCalls) > 0 {
		return "(the endpoint asked for a tool call)", doc.Usage, ""
	}
	return "", doc.Usage, "the first choice has no message content"
}

func etEventLine(e any) string {
	m, ok := e.(map[string]any)
	if !ok {
		return etCap(fmt.Sprint(e), 200)
	}
	typ, _ := m["type"].(string)
	name, _ := m["name"].(string)
	if name != "" {
		return etCap(typ+" "+name, 200)
	}
	return etCap(typ, 200)
}

// redactEndpoint drops user info and the query (which may carry a key).
func redactEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unreadable URL)"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

func etScrubURL(err error, raw string) string {
	msg := err.Error()
	if raw != "" {
		msg = strings.ReplaceAll(msg, raw, redactEndpoint(raw))
	}
	return msg
}

func etSnippet(raw []byte) string {
	return etCap(strings.TrimSpace(string(raw)), 300)
}

func etCap(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
