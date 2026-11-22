package connect

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Probe limits (section 4.3): at most 10 requests, 3 seconds each, GET
// before POST, at most 3 POSTs. No redirect is followed (the client's
// rule) and every probe stays on the URL's host.
const (
	DefaultMaxRequests = 10
	DefaultMaxPOSTs    = 3
	DefaultProbeWait   = 3 * time.Second
)

// Options configure Detect.
type Options struct {
	// Client sends every probe. It must refuse private addresses on a
	// hosted server (webhooks.GuardedDialer) and never follow redirects.
	Client      *http.Client
	MaxRequests int
	MaxPOSTs    int
	// Secrets are secret values the probe may use, by secret name: the
	// token a pasted curl carried, or a value the user just typed. They are
	// never written into the proposal.
	Secrets map[string]string
	// Target names where the probes leave from, for messages ("the shared
	// worker", "your worker").
	Target string
	Now    func() time.Time
}

func (o *Options) fill() {
	if o.MaxRequests <= 0 {
		o.MaxRequests = DefaultMaxRequests
	}
	if o.MaxPOSTs <= 0 {
		o.MaxPOSTs = DefaultMaxPOSTs
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: DefaultProbeWait, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if o.Target == "" {
		o.Target = "the shared worker"
	}
	if o.Secrets == nil {
		o.Secrets = map[string]string{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// NewNonce is a fresh check code, HMD- and five letters or digits.
func NewNonce() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b [5]byte
	_, _ = rand.Read(b[:])
	out := []byte("HMD-")
	for _, c := range b {
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}

// NoncePrompt is the instruction a nonce probe sends.
func NoncePrompt(nonce string) string {
	return "Heimdall connection check. Reply with exactly this code and nothing else: " + nonce
}

type prober struct {
	ctx      context.Context
	opt      Options
	prop     *Proposal
	requests int
	posts    int
}

var errBudget = errors.New("the request budget is spent")

func (p *prober) allow(method string) bool {
	if p.requests >= p.opt.MaxRequests {
		return false
	}
	if method != http.MethodGet && p.posts >= p.opt.MaxPOSTs {
		return false
	}
	return true
}

func (p *prober) count(method string) {
	p.requests++
	if method != http.MethodGet {
		p.posts++
	}
}

// get fetches a JSON document; doc is nil when the answer is not JSON.
func (p *prober) get(u *url.URL, note string) (int, any, error) {
	if !p.allow(http.MethodGet) {
		return 0, nil, errBudget
	}
	p.count(http.MethodGet)
	ctx, cancel := context.WithTimeout(p.ctx, DefaultProbeWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	start := p.opt.Now()
	resp, err := p.opt.Client.Do(req)
	pr := Probe{Method: "GET", URL: redactURL(u.String()), MS: p.opt.Now().Sub(start).Milliseconds(), Note: note}
	if err != nil {
		pr.Note = capText(scrub(err.Error(), p.opt.Secrets), 160)
		p.prop.Probes = append(p.prop.Probes, pr)
		return 0, nil, err
	}
	defer resp.Body.Close()
	pr.Status = resp.StatusCode
	p.prop.Probes = append(p.prop.Probes, pr)
	var doc any
	if resp.StatusCode == 200 {
		dec := json.NewDecoder(io.LimitReader(resp.Body, maxBody))
		if dec.Decode(&doc) != nil {
			doc = nil
		}
	}
	return resp.StatusCode, doc, nil
}

// Detect classifies the input and, for URLs and curl commands, runs the
// URL ladder and the nonce probe. It never returns nil.
func Detect(ctx context.Context, in Input, opt Options) *Proposal {
	opt.fill()
	raw := strings.TrimSpace(in.Raw)
	prop := &Proposal{Class: Classify(raw), Probes: []Probe{}, Candidates: []Candidate{}}
	if in.Platform != "" {
		prop.Class = ClassPlatform
		prop.Candidates = append(prop.Candidates, platformCandidate(Input{Platform: in.Platform, ID: firstNonEmpty(in.ID, raw)}))
		return prop
	}
	p := &prober{ctx: ctx, opt: opt, prop: prop}
	switch prop.Class {
	case ClassCurl:
		c, err := ParseCurl(raw)
		if err != nil {
			prop.Candidates = append(prop.Candidates, Candidate{Kind: KindHTTP, Label: "curl command", Message: err.Error()})
			return prop
		}
		cand, secrets := curlCandidate(c)
		for k, v := range secrets {
			if _, set := p.opt.Secrets[k]; !set {
				p.opt.Secrets[k] = v
			}
		}
		u, _ := url.Parse(c.URL)
		if fp := fingerprintURL(u); fp != nil && fp.mapping != nil && cand.Mapping == nil {
			cand.Preset, cand.Mapping = fp.preset, fp.mapping
			cand.Evidence = append(cand.Evidence, fp.evidence)
		}
		if !p.reach(u) {
			prop.Candidates = append(prop.Candidates, cand)
			return prop
		}
		p.nonceHTTP(&cand)
		prop.Candidates = append(prop.Candidates, cand)
	case ClassURL:
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			prop.Candidates = append(prop.Candidates, Candidate{Kind: KindHTTP, Label: "URL", Message: "this is not a URL Heimdall can read"})
			return prop
		}
		p.ladder(u)
	case ClassPlatform:
		if c, ok := arnCandidate(raw); ok {
			prop.Candidates = append(prop.Candidates, c)
		}
	case ClassRepository:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: KindRepo, Label: "Repository", Confidence: 0.5, Supported: true, Template: TemplateWrap,
			Params:   map[string]string{"repo_url": raw},
			Evidence: []string{"a git repository"},
			Message:  "Heimdall builds the repository into the agent image and starts your server next to each task. Give the start command and port; on a laptop, `heimdall serve module:object` runs it on your machine instead.",
			Questions: []Question{{Kind: "text", Param: "command", Label: "Start command", Help: "For example: uvicorn app:api --host 127.0.0.1 --port 8000"},
				{Kind: "text", Param: "port", Label: "Port"}}})
	case ClassImage:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: KindImage, Label: "Docker image", Confidence: 0.5, Supported: true, Template: TemplateWrap,
			Params:   map[string]string{"image": raw},
			Evidence: []string{"an image reference"},
			Message:  "Heimdall starts your image next to each task. Hosted runs need the image pinned by digest (@sha256:) and approved.",
			Questions: []Question{{Kind: "text", Param: "command", Label: "Start command"},
				{Kind: "text", Param: "port", Label: "Port"}}})
	case ClassPullObject:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: KindPull, Label: "Python object", Confidence: 0.6, Supported: false,
			Evidence: []string{"a module:object reference"}, Command: "heimdall serve " + raw,
			Message: "Code with no server runs on your machine: start `heimdall serve " + raw + "`; it connects out to Heimdall and answers each turn. Then connect the agent it registers."})
	case ClassSocket:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: KindPlatform, Label: "WebSocket", Supported: false, Evidence: []string{"a websocket URL"},
			Message: "Voice and chat sockets are connected on the agent page's voice platform form (websocket framing)."})
	case ClassPhone:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: KindPlatform, Label: "Phone number", Supported: false, Evidence: []string{"a phone number or SIP address"},
			Message: "Phone and SIP are not supported yet; connect through Vapi, Retell, ElevenLabs or a websocket endpoint."})
	case ClassTrace:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: KindHTTP, Label: "Trace", Supported: false, Evidence: []string{"an OTLP trace"},
			Message: "Send traces to your organization's OTLP endpoint and Heimdall proposes the agent from them."})
	default:
		prop.Candidates = append(prop.Candidates, Candidate{Kind: "", Label: "Unknown", Supported: false,
			Message: "Paste an endpoint URL, a curl command, a git repository, a Docker image or a platform id."})
	}
	return prop
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// reach checks that the host resolves and is not private on a hosted
// server; it records the dns or private_address failure.
func (p *prober) reach(u *url.URL) bool {
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(p.ctx, DefaultProbeWait)
	defer cancel()
	if _, err := net.DefaultResolver.LookupHost(ctx, u.Hostname()); err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && (de.IsNotFound || de.IsTemporary) {
			p.prop.Failures = append(p.prop.Failures, Lookup(FailDNS, map[string]string{"host": u.Hostname(), "target": p.opt.Target,
				"url": redactURL(u.String()), "fix": "heimdall serve " + redactURL(u.String()) + " --map auto"}))
			return false
		}
	}
	return true
}

// dialFailure maps a transport error to its catalog entry.
func (p *prober) dialFailure(err error, u *url.URL) Failure {
	if f, ok := ExplainRunError(err.Error()); ok {
		if f.Code == FailPrivateAddress || f.Code == FailDNS {
			return Lookup(f.Code, map[string]string{"host": u.Hostname(), "target": p.opt.Target, "url": redactURL(u.String()),
				"ip": ipInText.FindString(err.Error()), "fix": "heimdall serve " + redactURL(u.String()) + " --map auto"})
		}
		if f.Code == FailTLS {
			return Lookup(FailTLS, map[string]string{"host": u.Hostname(), "detail": capText(err.Error(), 120)})
		}
		if f.Code == FailTimeout {
			return Lookup(FailTimeout, map[string]string{"timeout": "3"})
		}
		return f
	}
	return Failure{Code: "unreachable", Title: "No answer", Message: "The endpoint did not answer: " + capText(scrub(err.Error(), p.opt.Secrets), 200)}
}

// ladder runs the URL ladder (section 4.3) and appends what it found.
func (p *prober) ladder(u *url.URL) {
	origin := &url.URL{Scheme: u.Scheme, Host: u.Host}
	if fp := fingerprintURL(u); fp != nil {
		c := fromFingerprint(fp, u.String())
		if fp.final {
			p.prop.Candidates = append(p.prop.Candidates, c)
			return
		}
		if !p.reach(u) {
			p.prop.Candidates = append(p.prop.Candidates, c)
			return
		}
		p.nonceHTTP(&c)
		p.prop.Candidates = append(p.prop.Candidates, c)
		return
	}
	if !p.reach(u) {
		return
	}
	// 2. A2A card.
	if c, ok := p.a2aCard(origin, u); ok {
		p.prop.Candidates = append(p.prop.Candidates, c)
		return
	}
	// 3. OpenAPI (FastAPI, LangGraph Server, ADK api_server).
	var cands []Candidate
	if c, ok := p.openAPI(origin, u); ok {
		cands = append(cands, c)
	}
	// 4. OpenAI-compatible.
	if len(cands) == 0 {
		if c, ok := p.openAIModels(u); ok {
			cands = append(cands, c)
		}
	}
	// 4b. Mastra.
	if len(cands) == 0 && (strings.HasPrefix(u.Path, "/api/agents") || u.Path == "" || u.Path == "/") {
		if c, ok := p.mastra(origin, u); ok {
			cands = append(cands, c)
		}
	}
	// 4c. MCP server card.
	if len(cands) == 0 && strings.Contains(strings.ToLower(u.Path), "mcp") {
		card := *u
		card.Path = strings.TrimRight(u.Path, "/") + "/server-card"
		if st, doc, err := p.get(&card, "MCP server card"); err == nil && st == 200 && doc != nil {
			p.prop.Candidates = append(p.prop.Candidates, Candidate{Kind: KindMCP, Label: "MCP server", Confidence: 0.8, Supported: false,
				Evidence: []string{"GET " + redactURL(card.String()) + " answered 200 with an MCP server card"},
				Message:  "This is an MCP server, not an agent. Connect the agent that uses it; benchmarking an MCP server itself is the Test my MCP server mode."})
			return
		}
	}
	// 5/6. Nonce shape inference on the URL itself.
	if len(cands) == 0 {
		cands = append(cands, Candidate{Kind: KindHTTP, Preset: PresetCustom, Label: "HTTP endpoint", Confidence: 0.4, Supported: true,
			Template: TemplateHTTP, URL: u.String(), Params: map[string]string{"url": u.String()}, Evidence: []string{}})
	}
	for i := range cands {
		switch cands[i].Kind {
		case KindOpenAI:
			p.nonceOpenAI(&cands[i])
		case KindHTTP:
			p.nonceHTTP(&cands[i])
		}
	}
	p.prop.Candidates = append(p.prop.Candidates, cands...)
}

func (p *prober) a2aCard(origin, u *url.URL) (Candidate, bool) {
	paths := []string{"/.well-known/agent-card.json", "/.well-known/agent.json"}
	if strings.HasSuffix(u.Path, ".json") && strings.Contains(u.Path, "agent") {
		paths = []string{u.Path}
	}
	for _, path := range paths {
		cu := *origin
		cu.Path = path
		st, doc, err := p.get(&cu, "A2A agent card")
		if err != nil {
			if !errors.Is(err, errBudget) {
				p.prop.Failures = append(p.prop.Failures, p.dialFailure(err, u))
			}
			return Candidate{}, false
		}
		m, ok := doc.(map[string]any)
		if st != 200 || !ok || m["name"] == nil || (m["url"] == nil && m["supportedInterfaces"] == nil) {
			continue
		}
		ver, _ := m["protocolVersion"].(string)
		c := Candidate{Kind: KindA2A, Preset: "a2a", Label: "A2A agent", Confidence: 0.95, Supported: true, Template: TemplateA2A,
			URL: cu.String(), Params: map[string]string{"card_url": cu.String()},
			Evidence: []string{"GET " + path + " answered 200" + optional(", protocolVersion ", ver)}}
		if name, _ := m["name"].(string); name != "" {
			c.Label = "A2A agent " + name
		}
		if caps, ok := m["capabilities"].(map[string]any); ok && caps["streaming"] == true {
			c.Params["streaming"] = "true"
			c.Evidence = append(c.Evidence, "the card says it streams")
		}
		if sk, ok := m["skills"].([]any); ok {
			for _, s := range sk {
				if sm, ok := s.(map[string]any); ok {
					if tags, ok := sm["tags"].([]any); ok {
						for _, t := range tags {
							if ts, ok := t.(string); ok && len(c.Capabilities) < 10 {
								c.Capabilities = append(c.Capabilities, ts)
							}
						}
					}
				}
			}
		}
		if ver != "" && versionBelow(ver, 1) {
			p.prop.Failures = append(p.prop.Failures, Lookup(FailA2AVersion, map[string]string{"version": ver}))
		}
		if ss, ok := m["securitySchemes"].(map[string]any); ok && len(ss) > 0 {
			c.Evidence = append(c.Evidence, "the card declares security schemes: "+strings.Join(sortedKeys(ss), ", "))
		}
		return c, true
	}
	return Candidate{}, false
}

func optional(prefix, v string) string {
	if v == "" {
		return ""
	}
	return prefix + v
}

func versionBelow(v string, major int) bool {
	v = strings.TrimPrefix(v, "v")
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n < major
}

// openAPI reads <origin>/openapi.json: LangGraph Server, ADK api_server or
// the best POST operation of any FastAPI-style service.
func (p *prober) openAPI(origin, u *url.URL) (Candidate, bool) {
	ou := *origin
	ou.Path = "/openapi.json"
	st, doc, err := p.get(&ou, "OpenAPI document")
	if err != nil || st != 200 {
		return Candidate{}, false
	}
	m, _ := doc.(map[string]any)
	paths, _ := m["paths"].(map[string]any)
	if len(paths) == 0 {
		return Candidate{}, false
	}
	has := func(p string) bool { _, ok := paths[p]; return ok }
	ev := "GET /openapi.json answered 200 with " + itoa(len(paths)) + " paths"
	switch {
	case has("/runs/wait") && has("/assistants/search"):
		run := *origin
		run.Path = "/runs/wait"
		c := Candidate{Kind: KindHTTP, Preset: PresetLangGraph, Label: "LangGraph Server", Confidence: 0.95, Supported: true, Template: TemplateHTTP,
			URL: run.String(), Params: map[string]string{"url": run.String()}, Mapping: langGraphMapping(p.langGraphAssistant(origin)),
			Evidence: []string{ev, "paths /runs/wait and /assistants/search: a LangGraph Server"}}
		return c, true
	case has("/list-apps") && has("/run"):
		app := p.adkApp(origin)
		run := *origin
		run.Path = "/run"
		c := Candidate{Kind: KindHTTP, Preset: PresetADK, Label: "Google ADK api_server", Confidence: 0.9, Supported: true, Template: TemplateHTTP,
			URL: run.String(), Params: map[string]string{"url": run.String()}, Mapping: adkMapping(app, "heimdall"),
			Evidence: []string{ev, "paths /list-apps and /run: an ADK api_server" + optional(", app ", app)}}
		if app == "" {
			c.Questions = append(c.Questions, Question{Kind: "text", Param: "preset_app_name", Label: "App name", Help: "The agent folder name the server lists at /list-apps."})
		}
		return c, true
	}
	comps, _ := m["components"].(map[string]any)
	defs, _ := comps["schemas"].(map[string]any)
	op, field, ok := pickOperation(paths, defs, u.Path)
	if !ok {
		return Candidate{}, false
	}
	run := *origin
	run.Path = op
	c := Candidate{Kind: KindHTTP, Preset: PresetCustom, Label: "HTTP endpoint (from OpenAPI)", Confidence: 0.7, Supported: true, Template: TemplateHTTP,
		URL: run.String(), Params: map[string]string{"url": run.String()}, Evidence: []string{ev, "POST " + op + " takes " + field}}
	if field == "messages" {
		c.Mapping = &schemas.RemoteMapping{Preset: PresetCustom, Body: `{"messages": "{{messages}}"}`}
	} else if field != "instruction" {
		c.Mapping = &schemas.RemoteMapping{Preset: PresetCustom, Body: `{"` + jsonEsc(field) + `": "{{instruction}}"}`}
	}
	if sec, ok := m["components"].(map[string]any); ok {
		if ss, ok := sec["securitySchemes"].(map[string]any); ok {
			for _, name := range sortedKeys(ss) {
				s, _ := ss[name].(map[string]any)
				if s["type"] == "apiKey" && s["in"] == "header" {
					h, _ := s["name"].(string)
					sn := secretNameFor(u.Hostname(), "KEY")
					c.Params["auth_header"], c.Params["auth_secret"] = h, sn
					c.Questions = append(c.Questions, Question{Kind: "secret", Param: "auth_secret", Label: h, Secret: sn, Header: h})
				}
				if s["type"] == "http" && strings.EqualFold(textOf(s["scheme"]), "bearer") {
					sn := secretNameFor(u.Hostname(), "TOKEN")
					c.Params["auth_secret"] = sn
					c.Questions = append(c.Questions, Question{Kind: "secret", Param: "auth_secret", Label: "Bearer token", Secret: sn, Header: "Authorization"})
				}
			}
		}
	}
	return c, true
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

var ipInText = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b|\[?[0-9a-fA-F:]*::[0-9a-fA-F:]*\]?`)

var preferPath = regexp.MustCompile(`(?i)(run|invoke|chat|answer|generate|ask|query|message|agent)`)

// pickOperation picks the POST operation whose JSON request body has a
// prompt-like string property or a messages array (section 4.3 step 5).
// The pasted path wins when it is one of them.
func pickOperation(paths, defs map[string]any, want string) (string, string, bool) {
	type hit struct {
		path, field string
		score       int
	}
	var best *hit
	for _, path := range sortedKeys(paths) {
		item, _ := paths[path].(map[string]any)
		post, _ := item["post"].(map[string]any)
		if post == nil || strings.Contains(path, "{") {
			continue
		}
		props := requestProps(post, defs)
		field := ""
		for _, f := range promptFields {
			if pr, ok := props[f].(map[string]any); ok && (pr["type"] == "string" || pr["type"] == nil) {
				field = f
				break
			}
		}
		if pr, ok := props["messages"].(map[string]any); ok && pr["type"] == "array" && field == "" {
			field = "messages"
		}
		if field == "" {
			continue
		}
		score := 1
		if preferPath.MatchString(path) {
			score += 2
		}
		if path == want {
			score += 10
		}
		if best == nil || score > best.score {
			best = &hit{path, field, score}
		}
	}
	if best == nil {
		return "", "", false
	}
	return best.path, best.field, true
}

// requestProps reads the properties of an operation's JSON body schema,
// following one local $ref (FastAPI puts models under components).
func requestProps(op, defs map[string]any) map[string]any {
	rb, _ := op["requestBody"].(map[string]any)
	content, _ := rb["content"].(map[string]any)
	js, _ := content["application/json"].(map[string]any)
	schema, _ := js["schema"].(map[string]any)
	if ref, ok := schema["$ref"].(string); ok {
		schema, _ = defs[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	}
	props, _ := schema["properties"].(map[string]any)
	return props
}

// langGraphAssistant is the graph id to call. It is in langgraph.json,
// which the server does not expose; "agent" is the default of langgraph
// new, and the form keeps it editable.
func (p *prober) langGraphAssistant(*url.URL) string { return "agent" }

func (p *prober) adkApp(origin *url.URL) string {
	lu := *origin
	lu.Path = "/list-apps"
	st, doc, err := p.get(&lu, "ADK apps")
	if err != nil || st != 200 {
		return ""
	}
	if list, ok := doc.([]any); ok && len(list) > 0 {
		if s, ok := list[0].(string); ok {
			return s
		}
	}
	return ""
}

// openAIModels checks GET <base>/v1/models (Hayhooks, LiteLLM, vLLM, any
// OpenAI-compatible server).
func (p *prober) openAIModels(u *url.URL) (Candidate, bool) {
	base := *u
	path := strings.TrimRight(u.Path, "/")
	for _, suf := range []string{"/chat/completions", "/responses", "/models"} {
		path = strings.TrimSuffix(path, suf)
	}
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	base.Path, base.RawQuery = path, ""
	mu := base
	mu.Path = path + "/models"
	st, doc, err := p.get(&mu, "OpenAI-compatible models list")
	if err != nil || st != 200 {
		if err == nil && (st == 401 || st == 403) && strings.Contains(u.Path, "/v1") {
			c := Candidate{Kind: KindOpenAI, Preset: PresetChat, Label: "OpenAI-compatible endpoint", Confidence: 0.6, Supported: true, Template: TemplateOpenAI,
				URL: base.String(), Params: map[string]string{"url": base.String(), "api_key_secret": secretNameFor(u.Hostname(), "API_KEY")},
				Evidence:  []string{"GET " + path + "/models answered " + itoa(st)},
				Questions: []Question{{Kind: "secret", Param: "api_key_secret", Label: "API key", Secret: secretNameFor(u.Hostname(), "API_KEY")}}}
			return c, true
		}
		return Candidate{}, false
	}
	m, _ := doc.(map[string]any)
	data, _ := m["data"].([]any)
	if m == nil || (m["object"] != "list" && data == nil) {
		return Candidate{}, false
	}
	model := ""
	if len(data) > 0 {
		if dm, ok := data[0].(map[string]any); ok {
			model, _ = dm["id"].(string)
		}
	}
	c := Candidate{Kind: KindOpenAI, Preset: PresetChat, Label: "OpenAI-compatible endpoint", Confidence: 0.85, Supported: true, Template: TemplateOpenAI,
		URL: base.String(), Params: map[string]string{"url": base.String()},
		Evidence: []string{"GET " + path + "/models answered 200 with " + itoa(len(data)) + " models" + optional(", first ", model)}}
	if model != "" {
		c.Params["model"] = model
	}
	return c, true
}

func (p *prober) mastra(origin, u *url.URL) (Candidate, bool) {
	au := *origin
	au.Path = "/api/agents"
	st, doc, err := p.get(&au, "Mastra agents")
	if err != nil || st != 200 {
		return Candidate{}, false
	}
	m, ok := doc.(map[string]any)
	if !ok || len(m) == 0 {
		return Candidate{}, false
	}
	id := ""
	if rest, ok := strings.CutPrefix(u.Path, "/api/agents/"); ok {
		id = strings.SplitN(rest, "/", 2)[0]
	}
	if _, known := m[id]; !known {
		id = sortedKeys(m)[0]
	}
	run := *origin
	run.Path = "/api/agents/" + id + "/generate"
	return Candidate{Kind: KindHTTP, Preset: PresetMastra, Label: "Mastra agent " + id, Confidence: 0.85, Supported: true, Template: TemplateHTTP,
		URL: run.String(), Params: map[string]string{"url": run.String()}, Mapping: mastraMapping(),
		Evidence: []string{"GET /api/agents answered 200 with " + itoa(len(m)) + " agents"}}, true
}

// configOf is the agent config a candidate describes, for probing.
func configOf(c *Candidate) *schemas.AgentConfig {
	switch c.Template {
	case TemplateOpenAI:
		return &schemas.AgentConfig{Adapter: "openai_agent", Model: c.Params["model"], OpenAI: &schemas.OpenAIAgent{URL: c.Params["url"], Model: c.Params["model"], APIKeySecret: c.Params["api_key_secret"]}}
	case TemplateA2A:
		return &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: c.Params["card_url"]}}
	}
	return &schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: c.Params["url"], AuthSecret: c.Params["auth_secret"],
		AuthHeader: c.Params["auth_header"], Mapping: c.Mapping, Auth: c.Auth}}
}

func (p *prober) sender(c *Candidate) *Sender {
	return &Sender{Client: p.opt.Client, Config: configOf(c), Secrets: p.opt.Secrets, Now: p.opt.Now,
		Log: func(pr Probe) { p.prop.Probes = append(p.prop.Probes, pr) }}
}

// send is one counted nonce request.
func (p *prober) send(s *Sender, nonce string) (Exchange, bool) {
	if !p.allow(http.MethodPost) {
		return Exchange{}, false
	}
	p.count(http.MethodPost)
	if m := s.Config.Remote; m != nil && m.Mapping != nil && m.Mapping.Start != nil {
		p.count(http.MethodPost) // the start step is a request too
	}
	ctx, cancel := context.WithTimeout(p.ctx, 4*DefaultProbeWait)
	defer cancel()
	ex := s.Send(ctx, Vars{Instruction: NoncePrompt(nonce), SessionID: "heimdall-connect-" + newID(), SessionIndex: 1})
	return ex, true
}

// nonceOpenAI sends one chat with the check code.
func (p *prober) nonceOpenAI(c *Candidate) {
	nonce := NewNonce()
	p.prop.Nonce = nonce
	ex, ok := p.send(p.sender(c), nonce)
	if !ok {
		return
	}
	if p.httpFailure(c, ex) {
		return
	}
	if ex.Found && strings.Contains(ex.Answer, nonce) {
		c.NonceFound = true
		c.Confidence = 0.95
		c.Evidence = append(c.Evidence, "the check code "+nonce+" came back in choices[0].message.content")
		return
	}
	c.Evidence = append(c.Evidence, "the chat answered, but without the check code")
}

// fieldFromError reads the field a 4xx validation error names: FastAPI
// and Pydantic ({"detail":[{"loc":["body","query"]}]}), zod ({"issues":
// [{"path":["message"]}]}) or a plain message.
func fieldFromError(doc any, body []byte) string {
	if m, ok := doc.(map[string]any); ok {
		if det, ok := m["detail"].([]any); ok {
			for _, d := range det {
				if dm, ok := d.(map[string]any); ok {
					if loc, ok := dm["loc"].([]any); ok && len(loc) >= 2 && loc[0] == "body" {
						if s, ok := loc[len(loc)-1].(string); ok {
							return s
						}
					}
				}
			}
		}
		issues, _ := m["issues"].([]any)
		if e, ok := m["error"].(map[string]any); ok && issues == nil {
			issues, _ = e["issues"].([]any)
		}
		for _, is := range issues {
			if im, ok := is.(map[string]any); ok {
				if path, ok := im["path"].([]any); ok && len(path) > 0 {
					if s, ok := path[0].(string); ok {
						return s
					}
				}
			}
		}
	}
	if mm := missingFieldRe.FindSubmatch(body); mm != nil {
		for _, g := range mm[1:] {
			if len(g) > 0 {
				return string(g)
			}
		}
	}
	return ""
}

var missingFieldRe = regexp.MustCompile("(?i)(?:missing field [`'\"]([A-Za-z_][A-Za-z0-9_]*)[`'\"]|[\"'`]([A-Za-z_][A-Za-z0-9_]*)[\"'`] is required|field [\"'`]?([A-Za-z_][A-Za-z0-9_]*)[\"'`]? (?:is )?required)")

// httpFailure records the failure of a non-2xx answer or a transport
// error; true means stop.
func (p *prober) httpFailure(c *Candidate, ex Exchange) bool {
	u, _ := url.Parse(c.Params["url"])
	if u == nil {
		u = &url.URL{}
	}
	if ex.Err != nil {
		p.prop.Failures = append(p.prop.Failures, p.dialFailure(ex.Err, u))
		return true
	}
	hdr := map[string]string{"x-amzn-errortype": ex.Header.Get("x-amzn-errortype"), "www-authenticate": ex.Header.Get("WWW-Authenticate")}
	secret := c.Params["auth_secret"]
	if secret == "" {
		secret = c.Params["api_key_secret"]
	}
	if secret == "" {
		secret = secretNameFor(u.Hostname(), "TOKEN")
	}
	if f, ok := statusFailure(ex.Status, hdr, ex.SentToken, map[string]string{"secret": secret, "snippet": capText(string(ex.Body), 80), "n": itoa(p.requests), "fix": secret}); ok {
		p.prop.Failures = append(p.prop.Failures, f)
		if f.Code == FailAuthMissing && c.Params != nil && c.Template == TemplateHTTP {
			c.Params["auth_secret"] = secret
			if !hasQuestion(c, "auth_secret") {
				c.Questions = append(c.Questions, Question{Kind: "secret", Param: "auth_secret", Label: "Bearer token", Secret: secret, Header: "Authorization",
					Help: "Stored as the org secret " + secret + "; the agent config keeps the name only."})
			}
		}
		return true
	}
	return false
}

func hasQuestion(c *Candidate, param string) bool {
	for _, q := range c.Questions {
		if q.Param == param {
			return true
		}
	}
	return false
}

// nonceHTTP infers the request field and the answer path of an HTTP
// candidate with up to three POSTs (section 4.3 steps 6 to 8).
func (p *prober) nonceHTTP(c *Candidate) {
	nonce := NewNonce()
	p.prop.Nonce = nonce
	prompt := NoncePrompt(nonce)
	shapes := []*schemas.RemoteMapping{c.Mapping}
	tried := map[string]bool{}
	for len(shapes) > 0 {
		m := shapes[0]
		shapes = shapes[1:]
		key := ""
		if m != nil {
			key = m.Body
		}
		if tried[key] {
			continue
		}
		tried[key] = true
		c.Mapping = m
		ex, ok := p.send(p.sender(c), nonce)
		if !ok {
			c.Evidence = append(c.Evidence, "the request budget ran out before the answer was found")
			return
		}
		switch {
		case ex.Err == nil && (ex.Status == 400 || ex.Status == 422):
			if f := fieldFromError(ex.Doc, ex.Body); f != "" {
				nm := &schemas.RemoteMapping{Preset: presetOf(m), Body: `{"` + jsonEsc(f) + `": "{{instruction}}"}`}
				if f == "messages" {
					nm.Body = `{"messages": "{{messages}}"}`
				}
				p.prop.Failures = append(p.prop.Failures, Lookup(FailField, map[string]string{"field": f, "type": "string", "fix": f}))
				c.Evidence = append(c.Evidence, "HTTP "+itoa(ex.Status)+" names the field "+f)
				shapes = append([]*schemas.RemoteMapping{nm}, shapes...)
				continue
			}
			if m == nil || m.Body == "" {
				shapes = append(shapes, &schemas.RemoteMapping{Preset: PresetCustom, Body: `{"messages": "{{messages}}"}`})
			}
			continue
		case ex.Err == nil && (ex.Status == 404 || ex.Status == 405):
			p.prop.Failures = append(p.prop.Failures, Lookup(FailPath, map[string]string{"method": ex.Method, "path": pathOf(ex.URL), "status": itoa(ex.Status),
				"other": "nothing else", "kind": "a different route"}))
			c.Evidence = append(c.Evidence, ex.Method+" "+pathOf(ex.URL)+" answered "+itoa(ex.Status))
			return
		}
		if p.httpFailure(c, ex) {
			return
		}
		if ex.Status < 200 || ex.Status > 299 {
			c.Evidence = append(c.Evidence, "HTTP "+itoa(ex.Status)+": "+capText(string(ex.Body), 120))
			return
		}
		p.locate(c, ex, nonce, prompt)
		return
	}
	c.Evidence = append(c.Evidence, "the endpoint refused every request shape tried: write the request template with {{instruction}} where the task goes")
	if !hasQuestion(c, "body") {
		c.Questions = append(c.Questions, Question{Kind: "text", Param: "body", Label: "Request template", Help: "JSON with {{instruction}} where the task goes."})
	}
}

func presetOf(m *schemas.RemoteMapping) string {
	if m == nil || m.Preset == "" {
		return PresetCustom
	}
	return m.Preset
}

func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "/"
	}
	return u.Path
}

// locate finds the check code in a 2xx answer and sets the answer path or
// the stream path.
func (p *prober) locate(c *Candidate, ex Exchange, nonce, prompt string) {
	if ex.Streamed {
		path, ok, echo := streamPathFor(ex.Events, nonce, prompt)
		switch {
		case ok:
			m := withMapping(c.Mapping)
			m.Stream, m.StreamPath, m.AnswerPath = schemas.RemoteStreamSSE, path, ""
			c.Mapping, c.NonceFound, c.Confidence = m, true, maxf(c.Confidence, 0.9)
			c.Evidence = append(c.Evidence, "the check code "+nonce+" came back in the event stream"+optional(" at ", path))
		case echo:
			p.prop.Failures = append(p.prop.Failures, Lookup(FailEcho, nil))
		default:
			p.prop.Failures = append(p.prop.Failures, Lookup(FailNoAnswer, map[string]string{"code": nonce}))
		}
		return
	}
	if ex.Doc == nil {
		text := strings.TrimSpace(string(ex.Body))
		switch {
		case strings.Contains(text, prompt):
			p.prop.Failures = append(p.prop.Failures, Lookup(FailEcho, nil))
		case strings.Contains(text, nonce):
			c.NonceFound, c.Confidence = true, maxf(c.Confidence, 0.9)
			if c.Mapping != nil && c.Mapping.Body == "" {
				c.Mapping = nil
			}
			c.Evidence = append(c.Evidence, "the check code "+nonce+" came back as the plain text body")
		default:
			p.prop.Failures = append(p.prop.Failures, Lookup(FailNoAnswer, map[string]string{"code": nonce}))
		}
		return
	}
	path, echo, fields := LocateNonce(ex.Doc, nonce, prompt)
	switch {
	case path != "":
		c.NonceFound, c.Confidence = true, maxf(c.Confidence, 0.9)
		if path == "answer" && (c.Mapping == nil || c.Mapping.Body == "") {
			c.Mapping = nil // Heimdall's own shape, end to end
			c.Evidence = append(c.Evidence, "the endpoint speaks Heimdall's own shape: the check code came back in answer")
			return
		}
		m := withMapping(c.Mapping)
		m.AnswerPath = path
		c.Mapping = m
		c.Evidence = append(c.Evidence, "the check code "+nonce+" came back at "+path)
	case echo:
		p.prop.Failures = append(p.prop.Failures, Lookup(FailEcho, nil))
	default:
		f := Lookup(FailNoAnswer, map[string]string{"code": nonce})
		f.Options = fields
		p.prop.Failures = append(p.prop.Failures, f)
		c.Questions = append(c.Questions, Question{Kind: "pick_field", Param: "answer_path", Label: "Field that holds the reply", Options: fields})
	}
}

// withMapping returns a copy of m with Heimdall's own body made explicit
// when m has none (an answer path needs a mapping).
func withMapping(m *schemas.RemoteMapping) *schemas.RemoteMapping {
	out := &schemas.RemoteMapping{Preset: PresetCustom}
	if m != nil {
		cp := *m
		out = &cp
	}
	if out.Body == "" {
		out.Body = ownTemplate
	}
	return out
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
