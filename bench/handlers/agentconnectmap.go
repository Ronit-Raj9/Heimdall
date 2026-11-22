package handlers

// Request and response mapping in "Connect your HTTP endpoint" (lane Y10,
// gap G1 P0-3 and G4-13): a platform preset, the request template, answer
// path, stream and headers, and OAuth 2, Entra ID, SigV4, Google ID token
// or mutual TLS auth, all from Settings > Secrets. The form's preview
// renders the request for a sample task (POST /agents/endpoint-preview);
// "Test my endpoint" sends it and shows the parsed answer.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/remotemap"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// EndpointPresetList is GET /agents/endpoint-presets.
type EndpointPresetList struct {
	Presets []remotemap.Preset `json:"presets"`
}

// EndpointPreview is POST /agents/endpoint-preview: the requests a sample
// task sends, secret values masked.
type EndpointPreview struct {
	OK       bool             `json:"ok"`
	Error    string           `json:"error,omitempty"`
	Requests []remotemap.Sent `json:"requests"`
	// Reads says how the answer is read, in words.
	Reads string `json:"reads"`
}

// Auth types of the form's auth_type select.
var connectAuthTypes = []string{"none", schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra, schemas.RemoteAuthSigV4, schemas.RemoteAuthGCPIDToken}

func showWhen(param string, values ...string) map[string][]string {
	return map[string][]string{param: values}
}

// connectMappingParams are the preset, mapping and auth fields of the HTTP
// endpoint form.
func connectMappingParams() []TemplateParam {
	presetOpts := []string{remotemap.PresetCustom}
	labels := map[string]string{remotemap.PresetCustom: "Heimdall's own shape or a custom mapping"}
	inputs := map[string]*TemplateParam{}
	var inputOrder []string
	for _, p := range remotemap.Presets {
		presetOpts = append(presetOpts, p.ID)
		labels[p.ID] = p.Label
		for _, in := range p.Inputs {
			name := "preset_" + in.Name
			if tp, ok := inputs[name]; ok {
				tp.ShowWhen["preset"] = append(tp.ShowWhen["preset"], p.ID)
				continue
			}
			inputs[name] = &TemplateParam{Name: name, Label: in.Label, Placeholder: in.Placeholder, Help: in.Help, Default: in.Default,
				ShowWhen: showWhen("preset", p.ID)}
			inputOrder = append(inputOrder, name)
		}
	}
	out := []TemplateParam{{Name: "preset", Label: "Platform", Kind: ParamSelect, Options: presetOpts, OptionLabels: labels, Default: remotemap.PresetCustom,
		Help: "Pick the platform your agent runs on and Heimdall fills the request, the answer path and the auth. Every field stays editable."}}
	for _, n := range inputOrder {
		out = append(out, *inputs[n])
	}
	authFor := func(types ...string) map[string][]string { return showWhen("auth_type", types...) }
	tokenTypes := []string{schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra}
	out = append(out,
		TemplateParam{Name: "method", Label: "Method", Kind: ParamSelect, Options: []string{"POST", "PUT", "PATCH", "GET"}, Default: "POST", Group: "mapping"},
		TemplateParam{Name: "body", Label: "Request template", Kind: ParamMultiline, Group: "mapping",
			Placeholder: `{"input": "{{instruction}}", "session": "{{session_id}}"}`,
			Help:        "JSON with placeholders: {{instruction}}, {{messages}}, {{tools}}, {{metadata}}, {{session_id}}, {{session_index}}, {{model}}, {{uuid}}. Empty sends the preset's template, or Heimdall's own body."},
		TemplateParam{Name: "answer_path", Label: "Answer path", Placeholder: "choices[0].message.content", Group: "mapping",
			Help: "Where the answer is in the JSON response: a.b, [0], [-1], [*], [?(@.type=='message')]. Empty reads the preset's path, or an answer field or a text body."},
		TemplateParam{Name: "stream", Label: "Streamed answer", Kind: ParamSelect, Options: []string{"none", schemas.RemoteStreamSSE, schemas.RemoteStreamAWSEventStream},
			OptionLabels: map[string]string{"none": "Not streamed", schemas.RemoteStreamSSE: "Server-sent events", schemas.RemoteStreamAWSEventStream: "AWS event stream"}, Group: "mapping"},
		TemplateParam{Name: "stream_path", Label: "Text of each event", Placeholder: "choices[0].delta.content", Group: "mapping", ShowWhen: showWhen("stream", schemas.RemoteStreamSSE, schemas.RemoteStreamAWSEventStream),
			Help: "The path of each event's text; the pieces are joined in order."},
		TemplateParam{Name: "headers", Label: "Headers", Kind: ParamMultiline, Placeholder: "X-Tenant: acme\nX-Api-Key: {{secret.MY_AGENT_KEY}}", Group: "mapping",
			Help: "One Name: value per line. A value may name a secret as {{secret.NAME}}."},
		TemplateParam{Name: "query", Label: "Query parameters", Kind: ParamMultiline, Placeholder: "api-version=2025-01-01", Group: "mapping",
			Help: "One name=value per line."},
		TemplateParam{Name: "auth_type", Label: "Sign-in method", Kind: ParamSelect, Options: connectAuthTypes, Default: "none", Group: "auth",
			OptionLabels: map[string]string{"none": "Token from a secret, or none", schemas.RemoteAuthOAuth2: "OAuth 2 client credentials", schemas.RemoteAuthEntra: "Microsoft Entra ID",
				schemas.RemoteAuthSigV4: "AWS Signature Version 4", schemas.RemoteAuthGCPIDToken: "Google ID token (service account)"},
			Help: "How Heimdall signs in to your endpoint. Tokens are fetched once per run and refreshed before they expire."},
		TemplateParam{Name: "token_url", Label: "Token URL", Placeholder: "https://login.example.com/oauth/token", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra, schemas.RemoteAuthGCPIDToken),
			Help: "OAuth 2: your token endpoint. Entra and Google: leave empty for the standard one."},
		TemplateParam{Name: "tenant_id", Label: "Tenant id", Placeholder: "contoso.onmicrosoft.com", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthEntra)},
		TemplateParam{Name: "client_id_secret", Label: "Client id", Kind: ParamSecret, Placeholder: "AGENT_CLIENT_ID", Group: "auth", ShowWhen: authFor(tokenTypes...)},
		TemplateParam{Name: "client_secret_secret", Label: "Client secret", Kind: ParamSecret, Placeholder: "AGENT_CLIENT_SECRET", Group: "auth", ShowWhen: authFor(tokenTypes...)},
		TemplateParam{Name: "scope", Label: "Scope", Placeholder: "api://my-agent/.default", Group: "auth", ShowWhen: authFor(tokenTypes...)},
		TemplateParam{Name: "audience", Label: "Audience", Placeholder: "https://my-agent-abc123-uc.a.run.app", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthOAuth2, schemas.RemoteAuthGCPIDToken),
			Help: "Google: the service URL the ID token is for. OAuth 2: the audience parameter, if your provider wants one."},
		TemplateParam{Name: "service_account_secret", Label: "Service account key", Kind: ParamSecret, Placeholder: "GCP_SERVICE_ACCOUNT_JSON", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthGCPIDToken),
			Help: "A secret holding the service account's JSON key. It needs roles/run.invoker (or IAP access) on the service."},
		TemplateParam{Name: "access_key_secret", Label: "AWS access key id", Kind: ParamSecret, Placeholder: "AWS_ACCESS_KEY_ID", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthSigV4)},
		TemplateParam{Name: "secret_key_secret", Label: "AWS secret access key", Kind: ParamSecret, Placeholder: "AWS_SECRET_ACCESS_KEY", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthSigV4)},
		TemplateParam{Name: "session_token_secret", Label: "AWS session token", Kind: ParamSecret, Placeholder: "AWS_SESSION_TOKEN", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthSigV4),
			Help: "Only for temporary credentials."},
		TemplateParam{Name: "aws_region", Label: "AWS region", Placeholder: "us-east-1", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthSigV4)},
		TemplateParam{Name: "aws_service", Label: "AWS signing name", Placeholder: "bedrock", Group: "auth", ShowWhen: authFor(schemas.RemoteAuthSigV4),
			Help: "bedrock for Bedrock Agents, execute-api for API Gateway, lambda for a function URL."},
		TemplateParam{Name: "client_cert_secret", Label: "Client certificate (mutual TLS)", Kind: ParamSecret, Placeholder: "AGENT_CLIENT_CERT", Group: "auth",
			Help: "Optional, with any sign-in method: a secret holding a PEM client certificate."},
		TemplateParam{Name: "client_key_secret", Label: "Client key (mutual TLS)", Kind: ParamSecret, Placeholder: "AGENT_CLIENT_KEY", Group: "auth"},
		TemplateParam{Name: "ca_cert_secret", Label: "Server CA (mutual TLS)", Kind: ParamSecret, Placeholder: "AGENT_SERVER_CA", Group: "auth",
			Help: "Optional: a PEM CA bundle your endpoint's certificate is checked against."},
	)
	return out
}

// remotemapUserURLPresets are the presets whose URL the user gives.
func remotemapUserURLPresets() []string {
	out := []string{remotemap.PresetCustom}
	for _, p := range remotemap.Presets {
		if p.URL == "" {
			out = append(out, p.ID)
		}
	}
	return out
}

// connectLines parses "Name: value" (sep ':') or "name=value" (sep '=')
// lines.
func connectLines(v, label string, sep byte) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(v, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, sep)
		if i <= 0 {
			return nil, fmt.Errorf("%s line %q: want name%cvalue", label, line, sep)
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// connectPresetURL is the agent URL of the form: the preset's own URL when
// it has one, else the url field.
func connectPresetURL(params map[string]string) (string, bool) {
	p, ok := remotemap.FindPreset(strings.TrimSpace(params["preset"]))
	if !ok || p.URL == "" {
		return "", false
	}
	u, _, _, err := p.Fill("", presetInputs(params))
	return u, err == nil
}

func presetInputs(params map[string]string) map[string]string {
	in := map[string]string{}
	for k, v := range params {
		if n, ok := strings.CutPrefix(k, "preset_"); ok {
			in[n] = v
		}
	}
	return in
}

// connectMapping builds the mapping and auth blocks from the form and
// returns the agent URL (a preset may set it). Nil blocks mean Heimdall's
// own shape and no auth block.
func connectMapping(u string, params map[string]string) (string, *schemas.RemoteMapping, *schemas.RemoteAuth, error) {
	var m *schemas.RemoteMapping
	var auth *schemas.RemoteAuth
	preset := strings.TrimSpace(params["preset"])
	if preset != "" && preset != remotemap.PresetCustom {
		p, ok := remotemap.FindPreset(preset)
		if !ok {
			return "", nil, nil, fmt.Errorf("unknown platform %q", preset)
		}
		var err error
		if u, m, auth, err = p.Fill(u, presetInputs(params)); err != nil {
			return "", nil, nil, err
		}
		if strings.TrimSpace(u) == "" {
			return "", nil, nil, fmt.Errorf("%s needs the endpoint URL", p.Label)
		}
	}
	ensure := func() *schemas.RemoteMapping {
		if m == nil {
			m = &schemas.RemoteMapping{Preset: remotemap.PresetCustom}
		}
		return m
	}
	if v := strings.ToUpper(strings.TrimSpace(params["method"])); v != "" && v != "POST" {
		ensure().Method = v
	}
	if v := strings.TrimSpace(params["body"]); v != "" {
		ensure().Body = v
	}
	if v := strings.TrimSpace(params["answer_path"]); v != "" {
		mm := ensure()
		mm.AnswerPath = v
		if params["stream"] == "" || params["stream"] == "none" {
			mm.Stream, mm.StreamPath, mm.StreamDecode = "", "", ""
		}
	}
	switch v := strings.TrimSpace(params["stream"]); v {
	case "":
	case "none":
		if m != nil && params["answer_path"] != "" {
			m.Stream, m.StreamPath, m.StreamDecode = "", "", ""
		}
	case schemas.RemoteStreamSSE, schemas.RemoteStreamAWSEventStream:
		mm := ensure()
		if mm.Stream != v {
			mm.StreamDecode = ""
		}
		mm.Stream, mm.AnswerPath = v, ""
	default:
		return "", nil, nil, fmt.Errorf("streamed answer %q: want none, sse or aws_eventstream", v)
	}
	if v := strings.TrimSpace(params["stream_path"]); v != "" {
		ensure().StreamPath = v
	}
	if v := params["headers"]; strings.TrimSpace(v) != "" {
		h, err := connectLines(v, "headers", ':')
		if err != nil {
			return "", nil, nil, err
		}
		mm := ensure()
		if mm.Headers == nil {
			mm.Headers = map[string]string{}
		}
		for k, val := range h {
			mm.Headers[k] = val
		}
	}
	if v := params["query"]; strings.TrimSpace(v) != "" {
		q, err := connectLines(v, "query parameters", '=')
		if err != nil {
			return "", nil, nil, err
		}
		ensure().Query = q
	}
	at := strings.TrimSpace(params["auth_type"])
	switch {
	case at == "none":
		auth = nil
	case at != "":
		if auth == nil || auth.Type != at {
			auth = &schemas.RemoteAuth{Type: at}
		}
	}
	str := func(name string) string { return strings.TrimSpace(params[name]) }
	if auth != nil {
		for _, f := range []struct {
			param string
			dst   *string
		}{{"token_url", &auth.TokenURL}, {"tenant_id", &auth.TenantID}, {"scope", &auth.Scope}, {"audience", &auth.Audience},
			{"aws_region", &auth.Region}, {"aws_service", &auth.Service}} {
			if v := str(f.param); v != "" {
				*f.dst = v
			}
		}
		if auth.Type == schemas.RemoteAuthGCPIDToken && auth.Audience == "" {
			if pu := strings.SplitN(u, "/", 4); len(pu) >= 3 {
				auth.Audience = strings.Join(pu[:3], "/")
			}
		}
	}
	for _, f := range []struct{ param, label string }{{"client_id_secret", "client id"}, {"client_secret_secret", "client secret"},
		{"service_account_secret", "service account key"}, {"access_key_secret", "AWS access key id"}, {"secret_key_secret", "AWS secret access key"},
		{"session_token_secret", "AWS session token"}, {"client_cert_secret", "client certificate"}, {"client_key_secret", "client key"}, {"ca_cert_secret", "server CA"}} {
		v, err := connectSecretName(params, f.param, f.label+" secret")
		if err != nil {
			return "", nil, nil, err
		}
		if v == "" {
			continue
		}
		mtls := strings.HasPrefix(f.param, "client_cert") || strings.HasPrefix(f.param, "client_key") || strings.HasPrefix(f.param, "ca_cert")
		if auth == nil {
			if !mtls {
				continue // a field of another sign-in method the form kept
			}
			auth = &schemas.RemoteAuth{}
		}
		switch f.param {
		case "client_id_secret":
			auth.ClientIDSecret = v
		case "client_secret_secret":
			auth.ClientSecretSecret = v
		case "service_account_secret":
			auth.ServiceAccountSecret = v
		case "access_key_secret":
			auth.AccessKeySecret = v
		case "secret_key_secret":
			auth.SecretKeySecret = v
		case "session_token_secret":
			auth.SessionTokenSecret = v
		case "client_cert_secret":
			auth.ClientCertSecret = v
		case "client_key_secret":
			auth.ClientKeySecret = v
		case "ca_cert_secret":
			auth.CACertSecret = v
		}
	}
	if auth != nil {
		clearForeign(auth)
	}
	r := &schemas.RemoteAgent{URL: u, Mapping: m, Auth: auth}
	if err := remotemap.Check(r); err != nil {
		return "", nil, nil, err
	}
	return u, m, auth, nil
}

// clearForeign drops the fields of other sign-in methods.
func clearForeign(a *schemas.RemoteAuth) {
	switch a.Type {
	case schemas.RemoteAuthOAuth2, schemas.RemoteAuthEntra:
		a.AccessKeySecret, a.SecretKeySecret, a.SessionTokenSecret, a.Region, a.Service, a.ServiceAccountSecret = "", "", "", "", "", ""
		if a.Type == schemas.RemoteAuthOAuth2 {
			a.TenantID = ""
		} else {
			a.Audience = ""
		}
	case schemas.RemoteAuthSigV4:
		a.TokenURL, a.TenantID, a.ClientIDSecret, a.ClientSecretSecret, a.Scope, a.Audience, a.ServiceAccountSecret = "", "", "", "", "", "", ""
	case schemas.RemoteAuthGCPIDToken:
		a.TenantID, a.ClientIDSecret, a.ClientSecretSecret, a.Scope, a.AccessKeySecret, a.SecretKeySecret, a.SessionTokenSecret, a.Region, a.Service = "", "", "", "", "", "", "", "", ""
	case "":
		*a = schemas.RemoteAuth{ClientCertSecret: a.ClientCertSecret, ClientKeySecret: a.ClientKeySecret, CACertSecret: a.CACertSecret}
	}
}

// toYAMLMap turns a struct into the map form the agent YAML is built from.
func toYAMLMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

type agentConnectMapHandler struct {
	env OrgEnv
	t   *agentTestHandler
}

func init() {
	RegisterOrgArea("agentconnectmap", func(env OrgEnv, r *router.Router) {
		h := &agentConnectMapHandler{env: env, t: &agentTestHandler{env: env, client: endpointTestClient, now: time.Now}}
		r.GET(APIPrefix+"/agents/endpoint-presets", env.R(h.presets))
		r.POST(APIPrefix+"/agents/endpoint-preview", env.R(h.preview))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/endpoint-presets", Tag: "agents",
			Summary: "List the platform presets of Connect your HTTP endpoint (request template, answer path, stream, session steps and suggested auth)", Response: EndpointPresetList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/endpoint-preview", Tag: "agents",
			Summary: "Render the requests an HTTP endpoint agent (saved, or the new-agent form) is sent for a sample task, without sending them; secret values are masked",
			Request: EndpointTestRequest{}, Response: EndpointPreview{}},
	)
}

func (h *agentConnectMapHandler) presets(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, EndpointPresetList{Presets: remotemap.Presets})
}

func (h *agentConnectMapHandler) preview(ctx *fasthttp.RequestCtx) {
	var req EndpointTestRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	cfg, status, msg := h.t.resolve(ctx, req)
	out := EndpointPreview{Requests: []remotemap.Sent{}}
	if cfg == nil {
		if status != fasthttp.StatusBadRequest {
			lib.SendError(ctx, h.env.Mode, status, lib.ErrTypeNotFound, msg)
			return
		}
		out.Error = msg // a form still being filled in: show why, not a failure
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	r := cfg.Remote
	if r == nil || (cfg.Adapter != "remote_http" && cfg.Adapter != "") {
		out.Error = "only an HTTP endpoint agent has a request to preview"
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	v := endpointVars(cfg)
	sent, err := remotemap.Plan(r, v, defaultTestBody(v))
	if err != nil {
		out.Error = err.Error()
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	if !remotemap.Mapped(r) {
		sent[0].Headers = append(sent[0].Headers, signShown(r)...)
	}
	out.OK, out.Requests, out.Reads = true, sent, answerReads(r)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func signShown(r *schemas.RemoteAgent) [][]string {
	if r.SignSecret == "" && r.SignEnv == "" {
		return nil
	}
	return [][]string{{"X-Heimdall-Timestamp", "<unix seconds>"}, {"X-Heimdall-Signature", "sha256=<HMAC of the body>"}}
}

// answerReads describes how the answer is read.
func answerReads(r *schemas.RemoteAgent) string {
	m := r.Mapping
	switch {
	case m == nil:
		return `the "answer" field of a JSON response, or a text/plain body`
	case m.Stream != "":
		kind := "server-sent events"
		if m.Stream == schemas.RemoteStreamAWSEventStream {
			kind = "AWS event stream"
		}
		at := "each event's data"
		if m.StreamPath != "" {
			at = m.StreamPath + " of each event"
		}
		return "a streamed answer (" + kind + "): " + at + ", joined in order"
	case m.Poll != nil:
		return "polls " + m.Poll.URL + " until " + m.Poll.AnswerPath + " has the answer"
	case m.AnswerPath != "":
		return m.AnswerPath + " of the JSON response"
	}
	return `the "answer" field of a JSON response, or a text/plain body`
}

// endpointVars are the sample task's values.
func endpointVars(cfg *schemas.AgentConfig) *remotemap.Vars {
	return &remotemap.Vars{Instruction: EndpointTestInstruction, SessionID: "heimdall-test-" + randHex(6), SessionIndex: 1,
		Output: schemas.OutputAnswer, Model: cfg.Model, Metadata: map[string]any{"test": true}}
}

// defaultTestBody is Heimdall's own body for the sample task.
func defaultTestBody(v *remotemap.Vars) []byte {
	b, _ := json.Marshal(map[string]any{"instruction": v.Instruction, "session_id": v.SessionID, "session_index": v.SessionIndex, "output": v.Output})
	return b
}

// resolve is the agent config a test or preview request names, or a status
// and message.
func (h *agentTestHandler) resolve(ctx *fasthttp.RequestCtx, req EndpointTestRequest) (*schemas.AgentConfig, int, string) {
	switch {
	case req.Template != nil && req.Agent == "":
		t, ok := findTemplate(req.Template.Template)
		if !ok {
			return nil, fasthttp.StatusBadRequest, fmt.Sprintf("unknown template %q", req.Template.Template)
		}
		id := strings.TrimSpace(req.Template.ID)
		if !agentIDRe.MatchString(id) {
			id = "endpoint-test"
		}
		r := *req.Template
		r.ID = id
		files, err := renderAgentTemplate(t, r)
		if err != nil {
			return nil, fasthttp.StatusBadRequest, err.Error()
		}
		cfg, err := decodeAgent(files[0].Data, "agents/"+id+".yaml")
		if err != nil {
			return nil, fasthttp.StatusBadRequest, "the agent does not validate: " + err.Error()
		}
		return cfg, 0, ""
	case req.Agent != "" && req.Template == nil:
		if !agentOpsNamePattern.MatchString(req.Agent) {
			return nil, fasthttp.StatusBadRequest, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", req.Agent)
		}
		c, err := (&agentCompatHandler{env: h.env}).agentConfig(ctx, req.Agent)
		if err != nil {
			return nil, fasthttp.StatusNotFound, err.Error()
		}
		return c, 0, ""
	}
	return nil, fasthttp.StatusBadRequest, "send agent (a saved agent) or template (the new-agent form), not both"
}

// runMapped is the endpoint test of a remote agent with a mapping or an
// auth block: the same requests a run sends, through remotemap.
func (h *agentTestHandler) runMapped(ctx context.Context, cfg *schemas.AgentConfig, res *EndpointTestResult, step func(string, string, ...any), fail func(string, ...any) *EndpointTestResult, start time.Time) *EndpointTestResult {
	r := cfg.Remote
	res.Adapter, res.URL = "remote_http", redactEndpoint(r.URL)
	if err := h.checkURL(strings.NewReplacer("{{", "x", "}}", "x").Replace(r.URL)); err != nil {
		return fail("%v", err)
	}
	if tu := remotemap.TokenURL(r.Auth); tu != "" {
		if err := h.checkURL(tu); err != nil {
			return fail("token URL: %v", err)
		}
	}
	if r.AuthEnv != "" && r.AuthSecret == "" && h.env.Services.Hosted {
		return fail("auth_env reads the server's environment, which hosted orgs cannot use: set an auth token secret instead")
	}
	names := r.SecretNames()
	if r.AuthEnv != "" && r.AuthSecret == "" {
		names = append(names, r.AuthEnv)
	}
	sec, err := h.secrets(ctx, names...)
	if err != nil {
		return fail("%v", err)
	}
	wait := endpointTimeout(r.TimeoutS)
	plain := h.client(h.env.Services.Hosted, wait)
	var auth *remotemap.Authorizer
	if r.Auth != nil {
		if auth, err = remotemap.NewAuthorizer(r.Auth, sec, plain, h.now); err != nil {
			return fail("%v", err)
		}
	}
	tokName := r.AuthSecret
	if tokName == "" {
		tokName = r.AuthEnv
	}
	c := &remotemap.Caller{
		Auth: auth, AuthHeader: r.AuthHeader, Token: sec[tokName],
		Client: func(tlsCfg *tls.Config) *http.Client {
			return mappedTestClient(h.env.Services.Hosted, wait, tlsCfg, plain)
		},
		Step: func(kind, detail string) { step(kind, "%s", detail) },
	}
	if r.SignSecret != "" {
		secret := sec[r.SignSecret]
		if len(secret) < 16 {
			return fail("the signing secret is shorter than 16 characters")
		}
		c.Sign = func(req *http.Request, body []byte) {
			ts := h.now().Unix()
			req.Header.Set("X-Heimdall-Timestamp", fmt.Sprint(ts))
			req.Header.Set("X-Heimdall-Signature", webhooks.Sign(secret, ts, body))
		}
	}
	if r.ModelTraffic == "gateway" {
		res.Notes = append(res.Notes, "A test carries no gateway block: your agent's model calls go through Heimdall only in real runs.")
	}
	if r.Tools == schemas.RemoteToolsExpose {
		res.Notes = append(res.Notes, "A test carries no tools: the task's tools are published only during real runs.")
	}
	v := endpointVars(cfg)
	v.Secrets = sec // {{secret.NAME}} in headers and query
	c.DefaultBody = defaultTestBody(v)
	cctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ex, err := c.Call(cctx, r, v)
	if ex != nil {
		res.Requests, res.Status = ex.Sent, ex.Status
		res.RawResponse = etCap(remotemap.Snippet(ex.Raw), 2000)
		if ex.Raw != nil {
			res.RawResponse = etCap(string(ex.Raw), 4000)
		}
	}
	res.LatencyMS = h.now().Sub(start).Milliseconds()
	if err != nil {
		return fail("%v", err)
	}
	answer := ex.Answer
	if !ex.Mapped {
		resp := &http.Response{StatusCode: ex.Status, Header: ex.Header}
		var reported map[string]any
		var why string
		answer, reported, why = remoteAnswer(resp, ex.Raw)
		if why != "" {
			return fail("%s", why)
		}
		res.Reported = reported
	}
	if auth != nil && auth.Fetches > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("Heimdall fetched %d token(s) from %s; runs fetch one per run and refresh it before it expires.", auth.Fetches, hostOnly(remotemap.TokenURL(r.Auth))))
	}
	res.Reads = answerReads(r)
	return finishEndpointTest(res, answer, step)
}

func hostOnly(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// mappedTestClient is the endpoint test's client with a client certificate.
func mappedTestClient(hosted bool, timeout time.Duration, tlsCfg *tls.Config, plain *http.Client) *http.Client {
	if tlsCfg == nil {
		return plain
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	if hosted {
		d = webhooks.GuardedDialer(10 * time.Second)
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, Proxy: nil, TLSClientConfig: tlsCfg}
	return &http.Client{Timeout: timeout, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// sortedParamNames is for tests and docs.
func sortedParamNames(ps []TemplateParam) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}
