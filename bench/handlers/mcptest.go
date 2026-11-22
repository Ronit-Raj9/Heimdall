package handlers

// MCP server mode (lane Z07, docs/mcp-server-testing.md): the org's own MCP
// server is the thing under test. Inspect measures it directly (reliability,
// error recovery, latency, schema and description quality); a drafted
// benchmark lets standard reference agents run tasks against it through the
// tool proxy; the server report reads those runs' tool ledgers per tool.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// MCP server mode routes.
const (
	MCPServerInspectPath    = APIPrefix + "/mcp-servers/inspect"
	MCPServerBenchmarksPath = APIPrefix + "/mcp-servers/benchmarks"
	MCPServerReportPath     = APIPrefix + "/mcp-servers/benchmarks/{id}/report"
)

// mcpInspectTimeout bounds one inspection.
const mcpInspectTimeout = 90 * time.Second

// MCPServerTarget says where the server is and how to authenticate.
type MCPServerTarget struct {
	URL string `json:"url"`
	// Secret names the org secret holding the credential; empty: none.
	Secret     string `json:"secret,omitempty"`
	AuthHeader string `json:"auth_header,omitempty"` // default Authorization
	AuthScheme string `json:"auth_scheme,omitempty"` // for example Bearer
}

// MCPServerInspectRequest is the body of POST /mcp-servers/inspect.
type MCPServerInspectRequest struct {
	MCPServerTarget
	Repeats      int                       `json:"repeats,omitempty"`
	CallReadOnly bool                      `json:"call_read_only,omitempty"`
	CallTools    []string                  `json:"call_tools,omitempty"`
	Args         map[string]map[string]any `json:"args,omitempty"`
}

// MCPServerDraftRequest is the body of POST /mcp-servers/benchmarks: the
// server is inspected again (sample calls give the reference controls),
// then a benchmark is drafted into the org's benchmarks.
type MCPServerDraftRequest struct {
	MCPServerInspectRequest
	ID       string   `json:"id"`        // benchmark id
	ServerID string   `json:"server_id"` // the name agents see the server under
	Tools    []string `json:"tools,omitempty"`
	MaxTasks int      `json:"max_tasks,omitempty"`
}

// MCPServerDraftResponse says what was drafted.
type MCPServerDraftResponse struct {
	mcptest.DraftResult
	Scores mcptest.Scores `json:"scores"`
	// ReferenceAgents are built-in agents that speak MCP natively; the
	// evaluation of a drafted benchmark runs them as its arms.
	ReferenceAgents []string `json:"reference_agents"`
	Next            string   `json:"next"`
}

// MCPServerReport answers GET /mcp-servers/benchmarks/{id}/report.
type MCPServerReport struct {
	Benchmark  string `json:"benchmark"`
	Experiment string `json:"experiment,omitempty"`
	mcptest.ServerReport
}

// MCPReferenceAgents are the built-in agents with native MCP client support.
var MCPReferenceAgents = []string{"claude-code", "codex", "opencode"}

type mcpTestArea struct {
	env OrgEnv
	// client builds the HTTP client of one inspection (tests replace it).
	client func() *http.Client
	getenv func(string) string // local mode secrets
}

func init() {
	RegisterOrgArea("mcptest", func(env OrgEnv, r *router.Router) {
		h := &mcpTestArea{env: env, getenv: os.Getenv}
		r.POST(MCPServerInspectPath, env.W(h.inspect))
		r.POST(MCPServerBenchmarksPath, env.W(h.draft))
		r.GET(MCPServerReportPath, env.R(h.report))
	})
}

func (h *mcpTestArea) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

// httpClient is the inspection client: on a hosted server a guarded dialer
// (no private or metadata addresses) and no redirects.
func (h *mcpTestArea) httpClient() *http.Client {
	if h.client != nil {
		return h.client()
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if h.env.Services.Hosted {
		tr := &http.Transport{DialContext: webhooks.GuardedDialer(10 * time.Second).DialContext, Proxy: nil,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
		return &http.Client{Timeout: 30 * time.Second, Transport: tr, CheckRedirect: noRedirect}
	}
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect}
}

// target checks the request's server and resolves its credential.
func (h *mcpTestArea) target(ctx context.Context, t MCPServerTarget) (mcptest.Target, string) {
	t.URL = strings.TrimSpace(t.URL)
	if err := mcptest.CheckDraftOptions(mcptest.DraftOptions{SuiteID: "x", ServerID: "x", URL: t.URL, AuthEnv: t.Secret, AuthHeader: t.AuthHeader, AuthScheme: t.AuthScheme}); err != nil {
		return mcptest.Target{}, err.Error()
	}
	if h.env.Services.Hosted {
		if err := CheckPullURL(t.URL); err != nil {
			return mcptest.Target{}, "url: " + err.Error()
		}
	}
	out := mcptest.Target{URL: t.URL, Headers: map[string]string{}}
	if t.Secret != "" {
		var v string
		if h.env.Services.Keys != nil {
			v = OrgSecretGetenv(ctx, h.env.Services)(t.Secret)
		} else if !h.env.Services.Hosted {
			v = h.getenv(t.Secret)
		}
		if v == "" {
			return mcptest.Target{}, "secret " + t.Secret + " is not set: add it under Settings, Secrets, then try again"
		}
		header := t.AuthHeader
		if header == "" {
			header = "Authorization"
		}
		if t.AuthScheme != "" {
			v = t.AuthScheme + " " + v
		}
		out.Headers[header] = v
	}
	return out, ""
}

func (h *mcpTestArea) run(ctx *fasthttp.RequestCtx, req MCPServerInspectRequest) (*mcptest.Report, bool) {
	tgt, problem := h.target(ctx, req.MCPServerTarget)
	if problem != "" {
		h.bad(ctx, problem)
		return nil, false
	}
	if req.Repeats < 0 || req.Repeats > mcptest.MaxRepeats {
		h.bad(ctx, "repeats: 1 to 20")
		return nil, false
	}
	c, cancel := context.WithTimeout(ctx, mcpInspectTimeout)
	defer cancel()
	rep := mcptest.Inspect(c, tgt, h.httpClient(), mcptest.Options{Repeats: req.Repeats, CallReadOnly: req.CallReadOnly, CallTools: req.CallTools, Args: req.Args})
	if req.Secret != "" && tgt.Headers != nil { // never echo the credential, even if the server did
		for _, v := range tgt.Headers {
			scrubReport(rep, v)
		}
	}
	return rep, true
}

// scrubReport replaces a credential anywhere in the report's text.
func scrubReport(rep *mcptest.Report, secret string) {
	if secret == "" {
		return
	}
	b, err := json.Marshal(rep)
	if err != nil || !strings.Contains(string(b), secret) {
		return
	}
	var clean mcptest.Report
	if json.Unmarshal([]byte(strings.ReplaceAll(string(b), secret, "[REDACTED]")), &clean) == nil {
		*rep = clean
	}
}

// inspect handles POST /mcp-servers/inspect.
func (h *mcpTestArea) inspect(ctx *fasthttp.RequestCtx) {
	var req MCPServerInspectRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"url","secret"?,"auth_header"?,"auth_scheme"?,"repeats"?,"call_read_only"?,"call_tools"?,"args"?}: `+err.Error())
		return
	}
	rep, ok := h.run(ctx, req)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, rep)
}

// draft handles POST /mcp-servers/benchmarks.
func (h *mcpTestArea) draft(ctx *fasthttp.RequestCtx) {
	projects := h.env.Services.Projects
	if projects == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the project file store is not available on this server")
		return
	}
	var req MCPServerDraftRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"id","server_id","url","secret"?,"auth_header"?,"auth_scheme"?,"tools"?,"max_tasks"?,"call_read_only"?,"call_tools"?,"args"?}: `+err.Error())
		return
	}
	opts := mcptest.DraftOptions{SuiteID: strings.TrimSpace(req.ID), ServerID: strings.TrimSpace(req.ServerID), URL: strings.TrimSpace(req.URL),
		AuthEnv: req.Secret, AuthHeader: req.AuthHeader, AuthScheme: req.AuthScheme, Tools: req.Tools, MaxTasks: req.MaxTasks}
	if err := mcptest.CheckDraftOptions(opts); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if isBuiltinSuite(h.env.Services.Suites, opts.SuiteID) {
		sendBuiltinReadOnly(ctx, h.env.Mode, opts.SuiteID)
		return
	}
	if err := os.MkdirAll(filepath.Join(projects.Root(), string(projectfs.AreaBenchmarks)), 0o755); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "create benchmarks folder: "+err.Error())
		return
	}
	dir, err := projects.Resolve(projectfs.AreaBenchmarks, opts.SuiteID)
	if err != nil {
		h.bad(ctx, "benchmark "+opts.SuiteID+": "+err.Error())
		return
	}
	if _, err := os.Lstat(dir); err == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "benchmark "+opts.SuiteID+" already exists; pick another id")
		return
	}
	rep, ok := h.run(ctx, req.MCPServerInspectRequest)
	if !ok {
		return
	}
	if !rep.Reached {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "the server did not answer: "+rep.Error)
		return
	}
	opts.Report = rep
	files, res, err := mcptest.Draft(opts)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err := writeTreeAtomic(dir, files); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "write benchmark: "+err.Error())
		return
	}
	next := "Run the readiness check, review each task, then evaluate it with the reference agents."
	if len(res.NoReference) > 0 {
		next = "Put working arguments into the reference of the tasks without one, run the readiness check, review each task, then evaluate it with the reference agents."
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, &MCPServerDraftResponse{DraftResult: res, Scores: rep.Scores, ReferenceAgents: MCPReferenceAgents, Next: next})
}

// report handles GET /mcp-servers/benchmarks/{id}/report[?experiment=].
func (h *mcpTestArea) report(ctx *fasthttp.RequestCtx) {
	projects, index := h.env.Services.Projects, h.env.Services.Index
	if projects == nil || index == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "run results are not available on this server")
		return
	}
	id, ok := idParam(ctx, h.env.Mode, "id", "benchmark")
	if !ok {
		return
	}
	dir, err := projects.Resolve(projectfs.AreaBenchmarks, id)
	if err != nil {
		sendNotFound(ctx, h.env.Mode, "benchmark", id)
		return
	}
	server, tools, taskTool, err := readDraftedServer(dir)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "benchmark "+id+" was not drafted from an MCP server: "+err.Error())
		return
	}
	exp := string(ctx.QueryArgs().Peek("experiment"))
	if exp != "" && !validID(exp) {
		h.bad(ctx, "experiment: not a valid id")
		return
	}
	var runs []mcptest.RunLedger
	exps := index.Experiments()
	if exp != "" {
		exps = []string{exp}
	}
	for _, e := range exps {
		for _, rv := range index.Runs(e) {
			if rv.Slot.Suite != id || rv.Superseded || !rv.Slot.Counted {
				continue
			}
			rl := mcptest.RunLedger{RunID: rv.Run.RunID, Agent: rv.Slot.Arm, Task: rv.Slot.Task, Passed: rv.Run.Outcome == schemas.OutcomePass}
			if b, err := index.RunFile(rv.Run.RunID, "tools.jsonl"); err == nil {
				rl.Rows = parseLedger(b)
			}
			runs = append(runs, rl)
		}
	}
	out := MCPServerReport{Benchmark: id, Experiment: exp, ServerReport: mcptest.BuildServerReport(server, tools, taskTool, runs)}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, &out)
}

// readDraftedServer reads mcp-server.json and each task's expect.json.
func readDraftedServer(dir string) (string, []mcptest.ToolView, map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, mcptest.ServerFile))
	if err != nil {
		return "", nil, nil, errors.New("no " + mcptest.ServerFile)
	}
	var sf struct {
		Server string             `json:"server"`
		Tools  []mcptest.ToolView `json:"tools"`
	}
	if err := json.Unmarshal(b, &sf); err != nil || sf.Server == "" {
		return "", nil, nil, errors.New(mcptest.ServerFile + " does not parse")
	}
	taskTool := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(dir, "tasks"))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		eb, err := os.ReadFile(filepath.Join(dir, "tasks", n, "verifier", "tests", "expect.json"))
		if err != nil {
			continue
		}
		var ex struct {
			Tool string `json:"tool"`
		}
		if json.Unmarshal(eb, &ex) == nil && ex.Tool != "" {
			taskTool[n] = ex.Tool
		}
	}
	return sf.Server, sf.Tools, taskTool, nil
}

func parseLedger(b []byte) []schemas.ToolCall {
	var out []schemas.ToolCall
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var tc schemas.ToolCall
		if json.Unmarshal([]byte(line), &tc) == nil {
			out = append(out, tc)
		}
	}
	return out
}
