package handlers

// The Connect box (lane Z01, audit/INFRA_I2_AGENT_ONBOARDING.md): one paste
// box for any agent. POST /connect/detect classifies what was pasted, runs
// the URL ladder and the nonce probe and proposes a normal agent config (a
// template, its params and the lane Y10 mapping); POST /connect/agents saves
// it; POST /connect/verify starts the agent.verify job (rows streamed as job
// progress, then one real task on the org's worker); POST /connect/first-look
// picks the first-look benchmark and returns the evaluation to start.
// Every probe stays on the pasted host, is capped (10 requests, 3 POSTs, 3 s
// each), never follows a redirect and, on a hosted server, never reaches a
// private address. Secret values never enter a config.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/connect"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// ConnectVerifyKind is the job kind of POST /connect/verify
// (cmd/bifrost-bench/jobs_connect.go).
const ConnectVerifyKind = "agent.verify"

// connectDetectWait caps one detection, every probe included.
const connectDetectWait = 45 * time.Second

// ConnectDetectRequest is POST /connect/detect.
type ConnectDetectRequest struct {
	Input    string `json:"input"`
	Platform string `json:"platform,omitempty"`
	ID       string `json:"id,omitempty"`
	// Secrets are values the user just typed for a question (by secret
	// name), used by the probes only. Store them with POST /secrets.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// ConnectPreview is the agent file a candidate would write.
type ConnectPreview struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	YAML  string `json:"yaml,omitempty"`
	Error string `json:"error,omitempty"`
}

// ConnectDetectResponse is the answer of POST /connect/detect.
type ConnectDetectResponse struct {
	*connect.Proposal
	// Preview is the agent file of the best candidate, rendered and
	// validated (nothing written).
	Preview *ConnectPreview `json:"preview,omitempty"`
	// From says where the probes left from.
	From string `json:"from"`
}

// ConnectSaveRequest is POST /connect/agents: a candidate (usually as
// detect returned it, edited) saved as agents/<id>.yaml.
type ConnectSaveRequest struct {
	ID        string            `json:"id"`
	Candidate connect.Candidate `json:"candidate"`
	Model     *AgentModelRef    `json:"model,omitempty"`
	Notes     string            `json:"notes,omitempty"`
	// Suggest applies what a verification learned (timeout).
	Suggest *connect.Suggest `json:"suggest,omitempty"`
	DryRun  bool             `json:"dry_run,omitempty"`
	// Overwrite replaces an agent of the same name (a re-save after a fix).
	Overwrite bool `json:"overwrite,omitempty"`
}

// ConnectVerifyRequest is POST /connect/verify.
type ConnectVerifyRequest struct {
	Agent string `json:"agent"`           // a saved agent id
	Suite string `json:"suite,omitempty"` // the real task's benchmark; empty picks the first-look benchmark
	Task  string `json:"task,omitempty"`
	// SkipRealTask checks the connection rows only.
	SkipRealTask bool `json:"skip_real_task,omitempty"`
}

// ConnectVerifyStatus is the answer of POST /connect/verify and of GET
// /connect/verify/{id}: the job, its rows so far and, once done, its result.
type ConnectVerifyStatus struct {
	JobID  string                `json:"job_id,omitempty"`
	Status string                `json:"status"`
	Agent  string                `json:"agent"`
	Suite  string                `json:"suite,omitempty"`
	Rows   []connect.VerifyRow   `json:"rows"`
	Result *connect.VerifyResult `json:"result,omitempty"`
	Error  string                `json:"error,omitempty"`
}

// ConnectFirstLookRequest is POST /connect/first-look.
type ConnectFirstLookRequest struct {
	Agent    string `json:"agent"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// ConnectFirstLookResponse is the pick and the evaluation to start with
// POST /evaluations.
type ConnectFirstLookResponse struct {
	Pick       connect.FirstLookPick  `json:"pick"`
	Evaluation schemas.EvaluationSpec `json:"evaluation"`
	// Reference is the reference board row for this benchmark and model,
	// when the server has one.
	Reference any `json:"reference,omitempty"`
	// BareModel is the optional second card, "Compare with the bare model"
	// (G4-21 baseline): an opponent evaluation of the agent against the bare
	// model on the agent's model, same benchmark and size as the first look.
	// The first look itself (Evaluation, preset first_look) is unchanged.
	// Nil when it does not fit; BareModelReason says why.
	BareModel       *schemas.EvaluationSpec `json:"bare_model,omitempty"`
	BareModelReason string                  `json:"bare_model_reason,omitempty"`
	// EstimateUSD is what the first look is expected to cost on the
	// caller's key, by the evaluation estimate; sent only with ?estimate=1
	// and when the estimate could be made.
	EstimateUSD *EvalCostRange `json:"estimate_usd,omitempty"`
}

// ConnectExplainRequest is POST /connect/explain: a run or check error.
type ConnectExplainRequest struct {
	Text string `json:"text"`
}

// ConnectExplainResponse maps the error to its catalog entry.
type ConnectExplainResponse struct {
	Found   bool             `json:"found"`
	Failure *connect.Failure `json:"failure,omitempty"`
}

// ConnectCatalog is GET /connect/failures: the failure catalog and the
// platform list.
type ConnectCatalog struct {
	Failures  []connect.CatalogEntry `json:"failures"`
	Platforms []connect.Platform     `json:"platforms"`
}

// FirstLookReference, when set (the reference board, lane Y09), returns the
// reference row for a benchmark and model, or nil.
var FirstLookReference func(ctx context.Context, benchmark, model string) any

type connectHandler struct {
	env OrgEnv
	now func() time.Time
	// detect is connect.Detect; tests replace the client through it.
	detect func(ctx context.Context, in connect.Input, opt connect.Options) *connect.Proposal
	verify func(ctx context.Context, cfg *schemas.AgentConfig, opt connect.VerifyOptions) *connect.VerifyResult
}

func init() {
	RegisterOrgArea("connect", func(env OrgEnv, r *router.Router) {
		h := &connectHandler{env: env, now: time.Now, detect: connect.Detect, verify: connect.Verify}
		p := APIPrefix + "/connect"
		r.GET(p+"/failures", env.R(h.catalog))
		r.POST(p+"/detect", env.W(h.detectRoute))
		r.POST(p+"/agents", env.W(h.save))
		r.POST(p+"/verify", env.W(h.startVerify))
		r.GET(p+"/verify/{id}", env.R(h.getVerify))
		r.POST(p+"/first-look", env.W(h.firstLook))
		r.POST(p+"/explain", env.W(h.explain))
	})
	p := APIPrefix + "/connect"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p + "/failures", Tag: "connect", Summary: "The connect failure catalog (code, message, one-click fix) and the platform list of the Connect box", Response: ConnectCatalog{}},
		RouteDoc{Method: "POST", Path: p + "/detect", Tag: "connect", Summary: "Classify a pasted URL, curl command, repository, image or platform id, probe it with a check code and propose an agent config", Request: ConnectDetectRequest{}, Response: ConnectDetectResponse{}},
		RouteDoc{Method: "POST", Path: p + "/agents", Tag: "connect", Summary: "Save a detected candidate as agents/<id>.yaml (dry_run renders and validates only)", Request: ConnectSaveRequest{}, Status: fasthttp.StatusCreated, Response: AgentFromTemplateResponse{}},
		RouteDoc{Method: "POST", Path: p + "/verify", Tag: "connect", Summary: "Start the agent.verify job: reach, sign-in, request, answer, latency, memory, isolation, version, tools, model calls, then one real task", Request: ConnectVerifyRequest{}, Status: fasthttp.StatusAccepted, Response: ConnectVerifyStatus{}},
		RouteDoc{Method: "GET", Path: p + "/verify/{id}", Tag: "connect", Summary: "Rows so far and the result of a verification job", Response: ConnectVerifyStatus{}},
		RouteDoc{Method: "POST", Path: p + "/first-look", Tag: "connect", Summary: "Pick the first-look benchmark for an agent (fastest compatible) and return the evaluation to start", Request: ConnectFirstLookRequest{}, Response: ConnectFirstLookResponse{},
			Query: []QueryDoc{{Name: "estimate", Description: "1: add estimate_usd, the cost range of the first-look evaluation"}}},
		RouteDoc{Method: "POST", Path: p + "/explain", Tag: "connect", Summary: "Map a run or check error to its connect failure catalog entry", Request: ConnectExplainRequest{}, Response: ConnectExplainResponse{}},
	)
}

func (h *connectHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case fasthttp.StatusNotFound:
		t = lib.ErrTypeNotFound
	case fasthttp.StatusConflict:
		t = lib.ErrTypeConflict
	case fasthttp.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case fasthttp.StatusInternalServerError:
		t = lib.ErrTypeInternal
	}
	lib.SendError(ctx, h.env.Mode, status, t, msg)
}

func (h *connectHandler) from() string {
	if h.env.Services.Hosted {
		return "Heimdall's server"
	}
	return "this server"
}

func (h *connectHandler) catalog(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ConnectCatalog{Failures: connect.Catalog(), Platforms: connect.PlatformList})
}

func (h *connectHandler) explain(ctx *fasthttp.RequestCtx) {
	var req ConnectExplainRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	out := ConnectExplainResponse{}
	if f, ok := connect.ExplainRunError(capConnect(req.Text, 4000)); ok {
		out.Found, out.Failure = true, &f
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func capConnect(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (h *connectHandler) detectRoute(ctx *fasthttp.RequestCtx) {
	var req ConnectDetectRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Input) == "" && req.Platform == "" {
		h.fail(ctx, fasthttp.StatusBadRequest, "paste an endpoint URL, a curl command, a repository, an image or a platform id")
		return
	}
	if len(req.Input) > 16<<10 {
		h.fail(ctx, fasthttp.StatusBadRequest, "the input is longer than 16 KiB")
		return
	}
	if len(req.Secrets) > 10 {
		h.fail(ctx, fasthttp.StatusBadRequest, "at most 10 secret values")
		return
	}
	for k := range req.Secrets {
		if !abSecretNameRe.MatchString(k) {
			h.fail(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("secret name %q: letters, digits and _", k))
			return
		}
	}
	work, cancel := context.WithTimeout(context.Background(), connectDetectWait)
	defer cancel()
	prop := h.detect(work, connect.Input{Raw: req.Input, Platform: req.Platform, ID: req.ID},
		connect.Options{Client: endpointTestClient(h.env.Services.Hosted, connect.DefaultProbeWait), Secrets: req.Secrets, Target: h.from(),
			Hosted: h.env.Services.Hosted})
	out := ConnectDetectResponse{Proposal: prop, From: h.from()}
	if b := prop.Best(); b != nil && b.Template != "" {
		id := suggestAgentID(b)
		doc, _, err := h.render(ConnectSaveRequest{ID: id, Candidate: *b})
		pv := &ConnectPreview{ID: id, Path: "agents/" + id + ".yaml"}
		if err != nil {
			pv.Error = err.Error()
		} else {
			pv.YAML = string(doc)
		}
		out.Preview = pv
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// suggestAgentID makes an agent id from the candidate's host.
func suggestAgentID(c *connect.Candidate) string {
	raw := c.URL
	if raw == "" {
		for _, k := range []string{"url", "card_url", "repo_url", "image"} {
			if v := c.Params[k]; v != "" {
				raw = v
				break
			}
		}
	}
	host := strings.ToLower(raw)
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.SplitN(host, "/", 2)[0]
	host = strings.SplitN(host, ":", 2)[0]
	parts := strings.Split(host, ".")
	var keep []string
	for _, p := range parts {
		switch p {
		case "www", "api", "com", "io", "ai", "net", "org", "dev", "app", "co", "cloud", "":
			continue
		}
		keep = append(keep, p)
		if len(keep) == 2 {
			break
		}
	}
	id := strings.Join(keep, "-")
	if c.Preset != "" && c.Preset != connect.PresetCustom && !strings.Contains(id, c.Preset) {
		id = strings.Trim(c.Preset+"-"+id, "-")
	}
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	id = strings.Trim(b.String(), "-")
	if len(id) > 40 {
		id = id[:40]
	}
	if id == "" || !agentIDRe.MatchString(id) || id == "127-0" || id == "localhost" {
		id = "my-agent"
	}
	return id
}

// render turns a candidate into the agent file: the template rendered with
// the params it knows, then the Y10 remote.mapping and remote.auth blocks
// when the template did not write them, then validated.
func (h *connectHandler) render(req ConnectSaveRequest) ([]byte, []renderedFile, error) {
	c := req.Candidate
	if c.Template == "" {
		return nil, nil, errors.New("this candidate has no agent template: it cannot be saved as an agent")
	}
	t, ok := findTemplate(c.Template)
	if !ok {
		return nil, nil, fmt.Errorf("unknown template %q", c.Template)
	}
	params := map[string]string{}
	known := map[string]bool{}
	for _, p := range t.Params {
		known[p.Name] = true
	}
	for k, v := range c.Params {
		if known[k] {
			params[k] = v
		}
	}
	if req.Suggest != nil && req.Suggest.TimeoutS > 0 && known["timeout_s"] && params["timeout_s"] == "" {
		params["timeout_s"] = fmt.Sprint(req.Suggest.TimeoutS)
	}
	files, err := renderAgentTemplate(t, AgentFromTemplateRequest{Template: t.ID, ID: req.ID, Model: req.Model, Notes: req.Notes, Params: params})
	if err != nil {
		return nil, nil, err
	}
	doc := files[0].Data
	if c.Template == connect.TemplateHTTP && (c.Mapping != nil || c.Auth != nil) {
		doc, err = editAgentYAML(doc, func(root *yaml.Node) {
			remote := abMapGet(root, "remote")
			if remote == nil {
				return
			}
			if c.Mapping != nil && abMapGet(remote, "mapping") == nil {
				setYAMLNode(remote, "mapping", c.Mapping)
			}
			if c.Auth != nil && c.Auth.Type != "" && abMapGet(remote, "auth") == nil {
				setYAMLNode(remote, "auth", c.Auth)
			}
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if note := connectHeadNote(c); note != "" {
		if d, err := editAgentYAML(doc, func(root *yaml.Node) {
			root.HeadComment = strings.TrimSpace(root.HeadComment + "\n" + note)
		}); err == nil {
			doc = d
		}
	}
	if _, err := decodeAgent(doc, "agents/"+req.ID+".yaml"); err != nil {
		return nil, nil, fmt.Errorf("the agent does not validate: %v", err)
	}
	files[0].Data = doc
	return doc, files, nil
}

func connectHeadNote(c connect.Candidate) string {
	var parts []string
	if c.Label != "" {
		parts = append(parts, "Connected with the Connect box: "+c.Label+".")
	}
	if c.NonceFound {
		parts = append(parts, "The check code came back at the answer path when it was detected.")
	}
	return strings.Join(parts, " ")
}

// setYAMLNode sets key in mapping m to v encoded as YAML, through its JSON
// form so empty optional fields are left out (the schema yaml tags have no
// omitempty).
func setYAMLNode(m *yaml.Node, key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	var plain any
	if json.Unmarshal(b, &plain) != nil {
		return
	}
	var n yaml.Node
	if err := n.Encode(plain); err != nil {
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &n)
}

func (h *connectHandler) save(ctx *fasthttp.RequestCtx) {
	var req ConnectSaveRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if !agentIDRe.MatchString(req.ID) || slices.Contains([]string{"new", "diff", "templates", "config"}, req.ID) {
		h.fail(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("agent name %q: use lowercase letters, digits, '.', '_' or '-' (max 63), not new, diff, templates or config", req.ID))
		return
	}
	if len(req.Notes) > 4000 {
		h.fail(ctx, fasthttp.StatusBadRequest, "notes are limited to 4000 characters")
		return
	}
	if err := connectSecretValuesAbsent(req.Candidate); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	doc, files, err := h.render(req)
	if err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	rel := "agents/" + req.ID + ".yaml"
	out := AgentFromTemplateResponse{ID: req.ID, Path: rel, YAML: string(doc), DryRun: req.DryRun, Files: []string{}}
	for _, f := range files {
		out.Files = append(out.Files, "agents/"+f.Rel)
	}
	if req.DryRun {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	ps := h.env.Services.Projects
	if ps == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "project files are not available on this server: start it with a project folder (serve -root) to create agents")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if !req.Overwrite {
		for _, rp := range []string{files[0].Rel, path.Join("images", req.ID)} {
			if _, _, err := ps.Read(work, projectfs.AreaAgents, rp); err == nil || errors.Is(err, projectfs.ErrInvalid) {
				h.fail(ctx, fasthttp.StatusConflict, fmt.Sprintf("an agent named %s already exists (agents/%s): pick another name", req.ID, rp))
				return
			}
		}
	}
	opts := projectfs.WriteOpts{Actor: abActor(ctx), Note: "connect box: " + req.Candidate.Label}
	for i := len(files) - 1; i >= 0; i-- {
		if _, err := ps.Write(work, projectfs.AreaAgents, files[i].Rel, files[i].Data, opts); err != nil {
			(&agentBuilder{mode: h.env.Mode, projects: ps}).storeError(ctx, "write agents/"+files[i].Rel, err)
			return
		}
	}
	snapshotAgentSave(work, ps, files[0].Rel, req.ID, files[0].Data, opts.Actor, "connected with the Connect box")
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, out)
}

// connectSecretValuesAbsent refuses a candidate whose params or mapping
// carry a secret value pasted as text (a prefill copied into a param).
func connectSecretValuesAbsent(c connect.Candidate) error {
	var prefills []string
	for _, q := range c.Questions {
		if len(q.Prefill) >= 6 {
			prefills = append(prefills, q.Prefill)
		}
	}
	if len(prefills) == 0 {
		return nil
	}
	b, _ := json.Marshal(struct {
		P map[string]string      `json:"p"`
		M *schemas.RemoteMapping `json:"m"`
		A *schemas.RemoteAuth    `json:"a"`
	}{c.Params, c.Mapping, c.Auth})
	for _, p := range prefills {
		if strings.Contains(string(b), p) {
			return errors.New("a secret value is written into the config: store it on Settings > Secrets and keep only its name")
		}
	}
	return nil
}

func (h *connectHandler) loadAgent(ctx context.Context, id string) (*schemas.AgentConfig, error) {
	ps := h.env.Services.Projects
	if ps == nil {
		return nil, errors.New("project files are not available on this server")
	}
	for _, ext := range []string{".yaml", ".yml"} {
		data, _, err := ps.Read(ctx, projectfs.AreaAgents, id+ext)
		if err != nil {
			continue
		}
		cfg, err := decodeAgent(data, "agents/"+id+ext)
		if err != nil {
			return nil, fmt.Errorf("agent %s does not validate: %v", id, err)
		}
		if cfg.ID == "" {
			cfg.ID = id
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("agent %s not found: save it first (POST /connect/agents)", id)
}

func (h *connectHandler) startVerify(ctx *fasthttp.RequestCtx) {
	var req ConnectVerifyRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if !agentIDRe.MatchString(req.Agent) {
		h.fail(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("agent id %q is not valid", req.Agent))
		return
	}
	work, cancel := workContext(ctx, connectDetectWait)
	defer cancel()
	cfg, err := h.loadAgent(work, req.Agent)
	if err != nil {
		h.fail(ctx, fasthttp.StatusNotFound, err.Error())
		return
	}
	if req.Suite == "" && !req.SkipRealTask {
		if pick, err := h.pick(work, cfg); err == nil {
			req.Suite = pick.Benchmark
		}
	}
	if req.Suite != "" && !quickSuiteRe.MatchString(strings.SplitN(req.Suite, "@", 2)[0]) {
		h.fail(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("benchmark %q is not a valid benchmark id", req.Suite))
		return
	}
	js := h.env.Services.Jobs
	if js == nil || !slices.Contains(js.Kinds(), ConnectVerifyKind) {
		// No job runner: the connection rows run here, without the real task.
		res := h.verify(work, cfg, connect.VerifyOptions{Client: endpointTestClient(h.env.Services.Hosted, 60*time.Second), Hosted: h.env.Services.Hosted,
			Target: h.from(), Secrets: h.secretsFor(work, cfg)})
		st := ConnectVerifyStatus{Status: string(jobs.StatusSucceeded), Agent: req.Agent, Rows: res.Rows, Result: res}
		if !res.OK {
			st.Status = string(jobs.StatusFailed)
		}
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, st)
		return
	}
	params, _ := json.Marshal(map[string]any{"agent": "agents/" + req.Agent + ".yaml", "suite": req.Suite, "task": req.Task, "skip_real_task": req.SkipRealTask || req.Suite == ""})
	j, err := js.Create(work, jobs.NewJob{OrgID: quickOrgID(ctx, h.env.Services.OrgID), Kind: ConnectVerifyKind, Params: params, CreatedBy: createdBy(auth.PrincipalFrom(ctx))})
	if err != nil {
		sendJobError(ctx, h.env.Mode, "start the verification", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, verifyStatusOf(j, req.Agent))
}

// secretsFor resolves the config's secrets for an in-process verification.
func (h *connectHandler) secretsFor(ctx context.Context, cfg *schemas.AgentConfig) map[string]string {
	var names []string
	if cfg.Remote != nil {
		names = append(names, cfg.Remote.SecretNames()...)
		names = append(names, cfg.Remote.AuthEnv, cfg.Remote.SignEnv)
	}
	if cfg.OpenAI != nil {
		names = append(names, cfg.OpenAI.APIKeySecret)
	}
	out := map[string]string{}
	t := &agentTestHandler{env: h.env}
	for _, n := range names {
		if n == "" {
			continue
		}
		if got, err := t.secrets(ctx, n); err == nil {
			out[n] = got[n]
		} else if !h.env.Services.Hosted {
			if v := os.Getenv(n); v != "" {
				out[n] = v
			}
		}
	}
	return out
}

func verifyStatusOf(j *jobs.Job, agent string) ConnectVerifyStatus {
	st := ConnectVerifyStatus{JobID: j.ID, Status: string(j.Status), Agent: agent, Error: j.Error, Rows: []connect.VerifyRow{}}
	var p struct {
		Agent string `json:"agent"`
		Suite string `json:"suite"`
	}
	_ = json.Unmarshal(j.Params, &p)
	if st.Agent == "" {
		st.Agent = strings.TrimSuffix(strings.TrimPrefix(p.Agent, "agents/"), ".yaml")
	}
	st.Suite = p.Suite
	if len(j.Result) > 0 {
		var res connect.VerifyResult
		if json.Unmarshal(j.Result, &res) == nil && len(res.Rows) > 0 {
			st.Result, st.Rows = &res, res.Rows
			return st
		}
	}
	if len(j.Progress) > 0 {
		var pr struct {
			Rows []connect.VerifyRow `json:"rows"`
		}
		if json.Unmarshal(j.Progress, &pr) == nil {
			st.Rows = pr.Rows
		}
	}
	return st
}

func (h *connectHandler) getVerify(ctx *fasthttp.RequestCtx) {
	js := h.env.Services.Jobs
	if js == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "this server runs no jobs")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := js.Get(work, quickOrgID(ctx, h.env.Services.OrgID), id)
	if err != nil || j.Kind != ConnectVerifyKind {
		h.fail(ctx, fasthttp.StatusNotFound, fmt.Sprintf("verification %s not found", id))
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, verifyStatusOf(j, ""))
}

// hasOrgKey reports whether the org has a usable provider key (a judge or
// simulated user needs one). A local server uses its own environment keys.
func (h *connectHandler) hasOrgKey(ctx context.Context) bool {
	ks := h.env.Services.Keys
	if ks != nil {
		if keys, err := ks.List(ctx, h.env.Services.OrgID, keystore.KindProvider); err == nil {
			for _, k := range keys {
				if usableKey(k) {
					return true
				}
			}
		}
	}
	return !h.env.Services.Hosted
}

// benchmarkInfos lists the benchmarks the picker considers.
func (h *connectHandler) benchmarkInfos(ctx context.Context, cfg *schemas.AgentConfig) []connect.BenchmarkInfo {
	src := h.env.Services.Suites
	if src == nil {
		return nil
	}
	ids, err := src.SuiteIDs(ctx)
	if err != nil {
		return nil
	}
	hosted := h.env.Services.Hosted
	examples := benchcatalog.ExampleSuiteIDs(benchcatalog.SourceRoot())
	// A hidden practice suite is not this org's to run, and an Add benchmark
	// staging copy (draft-<set id>) is internal and never listed (N242);
	// drop both before the cap.
	ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
		return benchcatalog.HidePracticeSuite(hosted, id) || onboard.IsStagingSuite(id)
	})
	if len(ids) > 120 {
		ids = ids[:120]
	}
	var out []connect.BenchmarkInfo
	for _, id := range ids {
		s, err := src.LoadSuite(ctx, id)
		if err != nil || s == nil {
			continue
		}
		// Practice examples (acme-*, coding-core) are the fallback only (B8.task).
		// Tasks counts the ready (admitted public) tasks only, so a benchmark
		// with none is never picked (PickFirstLook skips Tasks == 0; N242).
		info := connect.BenchmarkInfo{ID: id, Tasks: len(evalAdmittedTasks(s)), Practice: PracticeBenchmark(id) || examples[id]}
		ok, _, reason := evalCompat(cfg, s)
		info.Compatible, info.Reason = ok, reason
		if s.Lock == nil {
			// The planner refuses a suite without a lock, so it is never picked.
			info.Compatible, info.Reason = false, "it is not ready yet: run its readiness check on the benchmark page"
		}
		info.NeedsModel = s.Judge != nil || s.Simulator != nil || s.Grading == "judged"
		var limits []float64
		for _, t := range s.Tasks {
			if t != nil && t.Timeouts.AgentS > 0 {
				limits = append(limits, float64(t.Timeouts.AgentS))
			}
		}
		if len(limits) > 0 {
			sort.Float64s(limits)
			info.EstimateS, info.EstimateBasis = limits[len(limits)/2], "median task time limit, no measured runs"
		}
		out = append(out, info)
	}
	return out
}

func (h *connectHandler) pick(ctx context.Context, cfg *schemas.AgentConfig) (connect.FirstLookPick, error) {
	return connect.PickFirstLook(h.benchmarkInfos(ctx, cfg), h.hasOrgKey(ctx))
}

func (h *connectHandler) firstLook(ctx *fasthttp.RequestCtx) {
	var req ConnectFirstLookRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if !agentIDRe.MatchString(req.Agent) {
		h.fail(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("agent id %q is not valid", req.Agent))
		return
	}
	work, cancel := workContext(ctx, connectDetectWait)
	defer cancel()
	cfg, err := h.loadAgent(work, req.Agent)
	if err != nil {
		h.fail(ctx, fasthttp.StatusNotFound, err.Error())
		return
	}
	pick, err := h.pick(work, cfg)
	if err != nil {
		h.fail(ctx, fasthttp.StatusConflict, err.Error())
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.Model
	}
	ev := schemas.EvaluationSpec{
		Name:              "First look: " + req.Agent,
		Mode:              "score",
		Preset:            connect.FirstLookPreset,
		Arms:              []schemas.EvaluationArm{{Label: "A", Agent: req.Agent, Model: model, Provider: req.Provider}},
		Benchmarks:        []string{pick.Benchmark},
		TasksPerBenchmark: pick.Tasks,
		Trials:            connect.FirstLookTrials,
		Faults:            []string{},
	}
	out := ConnectFirstLookResponse{Pick: pick, Evaluation: ev}
	out.BareModel, out.BareModelReason = h.bareModelCard(work, cfg, req, model, pick)
	if FirstLookReference != nil {
		out.Reference = FirstLookReference(work, pick.Benchmark, model)
	}
	if string(ctx.QueryArgs().Peek("estimate")) == "1" {
		out.EstimateUSD = h.firstLookCost(work, ev)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// bareModelCard is the first look's optional "Compare with the bare model"
// evaluation, or nil and the reason it is not offered.
func (h *connectHandler) bareModelCard(ctx context.Context, cfg *schemas.AgentConfig, req ConnectFirstLookRequest, model string, pick connect.FirstLookPick) (*schemas.EvaluationSpec, string) {
	if req.Agent == utils.BareModelAgent || utils.IsBareModel(cfg) {
		return nil, "this agent is the bare model"
	}
	if model == "" {
		return nil, "the agent names no model, and the bare model runs on the agent's model"
	}
	if src := h.env.Services.Suites; src != nil {
		s, err := src.LoadSuite(ctx, pick.Benchmark)
		if err != nil || s == nil {
			return nil, "the benchmark could not be read"
		}
		bare := &schemas.AgentConfig{ID: utils.BareModelAgent, Adapter: "command", Model: model, Env: map[string]string{utils.BareModelEnv: "1"}}
		if ok, _, reason := evalCompat(bare, s); !ok {
			return nil, "the bare model cannot run " + pick.Benchmark + ": " + reason
		}
	}
	return &schemas.EvaluationSpec{
		Name:              "First look: " + req.Agent + " vs the bare model",
		Mode:              schemas.EvaluationModeOpponent,
		Arms:              []schemas.EvaluationArm{{Label: "A", Agent: req.Agent, Model: model, Provider: req.Provider}, {Label: "B", Agent: utils.BareModelAgent, Model: model, Provider: req.Provider}},
		Benchmarks:        []string{pick.Benchmark},
		TasksPerBenchmark: pick.Tasks,
		Trials:            connect.FirstLookTrials,
		Faults:            []string{},
		Concurrency:       connect.FirstLookConcurrency,
	}, ""
}
