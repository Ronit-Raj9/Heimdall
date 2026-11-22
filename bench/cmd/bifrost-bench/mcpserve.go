package main

// The Heimdall MCP server (docs/mcp-server.md): `bifrost-bench mcp serve`
// speaks the Model Context Protocol (JSON-RPC 2.0, one message per line) on
// stdin and stdout and answers every tool from a Heimdall server's public
// API v1 (/api/v1, docs/api-v1.md) with the caller's API token. It holds no
// state of its own: the token's role and scope decide what each tool may do.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

// mcpProtocolVersions are the MCP revisions this server speaks, newest first.
var mcpProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	mcpServerName = "heimdall"
	// mcpTraceCap bounds a trace returned to the model; the rest is cut with a note.
	mcpTraceCap = 200 << 10
	// mcpMaxWait bounds run_evaluation's wait.
	mcpMaxWait = 2 * time.Hour
)

func runMCPServe(args []string) int {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	base := fs.String("url", envOr("HEIMDALL_URL", "http://127.0.0.1:8090"), "Heimdall server root (env HEIMDALL_URL)")
	tokenEnv := fs.String("token-env", "HEIMDALL_TOKEN", "environment variable holding the API token (an hm_ org token, or the local write token)")
	timeout := fs.Duration("timeout", 60*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, mcpUsage)
		return exitUsage
	}
	token := strings.TrimSpace(os.Getenv(*tokenEnv))
	if token == "" { // Z02: fall back to the server and token `heimdall login` stored
		explicit := os.Getenv("HEIMDALL_URL")
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "url" {
				explicit = *base
			}
		})
		if c, err := connectlocal.LoadCredentials(); err == nil {
			key := connectlocal.NormalizeServer(explicit)
			if key == "" {
				key = c.Default
			}
			if sc := c.Servers[key]; sc != nil && sc.Token != "" {
				token = sc.Token
				if explicit == "" {
					*base = key
				}
			}
		}
	}
	u, err := url.Parse(strings.TrimRight(*base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fail("mcp serve: -url must be an http(s) server root, got %q", *base)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	api := &heimdallAPI{base: u.String(), token: token, client: &http.Client{Timeout: *timeout}}
	srv := &mcpServer{api: api, version: benchVersion, poll: 10 * time.Second}
	if err := srv.serve(ctx, os.Stdin, os.Stdout); err != nil {
		return fail("mcp serve: %v", err)
	}
	return exitOK
}

// ---- Heimdall API v1 ----

// heimdallAPI calls a Heimdall server's /api/v1.
type heimdallAPI struct {
	base   string
	token  string
	client *http.Client
}

// mcpAPIError is a refused or failed call, with the server's message.
type mcpAPIError struct {
	Status  int
	Type    string
	Message string
}

func (e *mcpAPIError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("Heimdall answered %d %s: %s", e.Status, e.Type, e.Message)
}

// do sends one request to /api/v1+path and returns the body. A non-2xx
// answer is an *mcpAPIError carrying the server's error message.
func (a *heimdallAPI) do(ctx context.Context, method, path string, query url.Values, body any) ([]byte, string, error) {
	u := a.base + "/api/v1" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "heimdall-mcp/"+benchVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", &mcpAPIError{Message: "cannot reach Heimdall at " + a.base + ": " + err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, "", &mcpAPIError{Message: "read answer: " + err.Error()}
	}
	if resp.StatusCode/100 != 2 {
		e := &mcpAPIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(data))}
		var eb struct {
			Error struct{ Message, Type string } `json:"error"`
		}
		if json.Unmarshal(data, &eb) == nil && eb.Error.Message != "" {
			e.Message, e.Type = eb.Error.Message, eb.Error.Type
		}
		return nil, "", e
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (a *heimdallAPI) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	data, _, err := a.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (a *heimdallAPI) postJSON(ctx context.Context, path string, body, out any) error {
	data, _, err := a.do(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// ---- MCP ----

type mcpServer struct {
	api     *heimdallAPI
	version string
	poll    time.Duration // run_evaluation's poll interval
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// serve reads one JSON-RPC message per line until in ends or ctx is done.
// Requests are answered in order; notifications get no answer.
func (s *mcpServer) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	enc := json.NewEncoder(out)
	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	go func() {
		defer close(lines)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		scanErr <- sc.Err()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				select {
				case err := <-scanErr:
					return err
				default:
					return nil
				}
			}
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if resp := s.handle(ctx, line); resp != nil {
				if err := enc.Encode(resp); err != nil {
					return err
				}
			}
		}
	}
}

// handle answers one message; nil for a notification.
func (s *mcpServer) handle(ctx context.Context, line []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}}
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	if notification {
		return nil // notifications/initialized, notifications/cancelled: nothing to answer
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		ver := mcpProtocolVersions[0]
		for _, v := range mcpProtocolVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": mcpServerName, "title": "Heimdall agent benchmarking", "version": s.version},
			"instructions": "Heimdall benchmarks your own AI agents. List agents and benchmarks, estimate an evaluation's cost before running it, " +
				"run it, then read its report card, run traces and failure explanations. Every answer says its mode; only live is a real measurement.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
			break
		}
		tool, ok := mcpToolTable[p.Name]
		if !ok {
			resp.Error = &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
			break
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		result, err := tool.run(ctx, s, args)
		resp.Result = toolResult(result, err)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

// toolResult is a tools/call result: the payload as indented JSON text (and
// as structuredContent when it is an object), or the error as a tool error
// the model can read and act on.
func toolResult(v any, err error) map[string]any {
	if err != nil {
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
	}
	if s, ok := v.(string); ok {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}
	}
	b, mErr := json.MarshalIndent(v, "", "  ")
	if mErr != nil {
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "encode result: " + mErr.Error()}}}
	}
	out := map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
	if _, isMap := v.(map[string]any); isMap {
		out["structuredContent"] = v
	}
	return out
}

// mcpTool is one tool: its description, input schema and implementation.
type mcpTool struct {
	description string
	schema      map[string]any
	readOnly    bool
	run         func(ctx context.Context, s *mcpServer, args json.RawMessage) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// evaluationSpecSchema is POST /api/v1/evaluations' body.
var evaluationSpecSchema = map[string]any{
	"type":        "object",
	"description": "Evaluation spec: one agent (mode score) or two arms (versions, models, opponent) across benchmarks or a pack",
	"properties": map[string]any{
		"name": str("Short name"),
		"mode": map[string]any{"type": "string", "enum": []string{"score", "versions", "models", "opponent"}},
		"arms": map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": obj(map[string]any{
			"label":    str("Arm label, A or B"),
			"agent":    str("Agent id (list_agents)"),
			"version":  map[string]any{"type": "integer", "description": "Agent version; 0 or absent for the current config"},
			"model":    str("Model id"),
			"provider": str("Provider; optional"),
		}, "label", "agent", "model")},
		"benchmarks":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Benchmark ids (list_benchmarks); or pack"},
		"pack":                str("Evaluation pack instead of benchmarks: coding, support, memory, ..."),
		"tasks_per_benchmark": map[string]any{"type": "integer", "description": "Default 8, at most 50"},
		"trials":              map[string]any{"type": "integer", "description": "Repeats per task, default 2, at most 10"},
		"faults":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Faults besides clean: model-down, broken-tool, misleading-instruction"},
		"budget_usd":          map[string]any{"type": "number", "description": "Stop spending above this"},
	},
	"required": []string{"name", "mode", "arms"},
}

var mcpToolTable map[string]mcpTool

func init() {
	mcpToolTable = map[string]mcpTool{
		"list_agents": {
			description: "List the organization's agents with their versions and capabilities.",
			schema:      obj(map[string]any{}), readOnly: true,
			run: func(ctx context.Context, s *mcpServer, _ json.RawMessage) (any, error) {
				var out map[string]any
				return out, s.api.getJSON(ctx, "/agents", nil, &out)
			},
		},
		"list_benchmarks": {
			description: "List the benchmarks available to evaluate on, and the evaluation packs that group them. Pass agent to see which ones it can run.",
			schema:      obj(map[string]any{"agent": str("Agent id: add its compatibility with each benchmark")}), readOnly: true,
			run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
				var args struct {
					Agent string `json:"agent"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, err
				}
				var benchmarks, packs map[string]any
				if err := s.api.getJSON(ctx, "/benchmarks", nil, &benchmarks); err != nil {
					return nil, err
				}
				out := map[string]any{"mode": benchmarks["mode"], "benchmarks": benchmarks}
				if err := s.api.getJSON(ctx, "/benchmarks/packs", nil, &packs); err == nil {
					out["packs"] = packs
				}
				if args.Agent != "" {
					var compat map[string]any
					if err := s.api.getJSON(ctx, "/agents/"+url.PathEscape(args.Agent)+"/compatibility", nil, &compat); err != nil {
						return nil, err
					}
					out["compatibility"] = compat
				}
				return out, nil
			},
		},
		"estimate": {
			description: "Estimate an evaluation before running it: runs, cost range in USD and minutes per benchmark. Unknown prices stay unknown (known false), never guessed. Nothing runs.",
			schema:      obj(map[string]any{"spec": evaluationSpecSchema}, "spec"), readOnly: true,
			run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
				var args struct {
					Spec json.RawMessage `json:"spec"`
				}
				if err := json.Unmarshal(raw, &args); err != nil || len(args.Spec) == 0 {
					return nil, errors.New("spec is required")
				}
				var out map[string]any
				return out, s.api.postJSON(ctx, "/evaluations/estimate", args.Spec, &out)
			},
		},
		"run_evaluation": {
			description: "Start an evaluation (it runs real agents and spends on the org's own provider keys). Returns the evaluation id at once, or with wait true waits up to wait_minutes and returns the report card. Estimate first.",
			schema: obj(map[string]any{
				"spec":         evaluationSpecSchema,
				"wait":         map[string]any{"type": "boolean", "description": "Wait for it to finish and return the report card"},
				"wait_minutes": map[string]any{"type": "integer", "description": "How long to wait, default 30, at most 120"},
			}, "spec"),
			run: runEvaluationTool,
		},
		"get_report": {
			description: "The report card of an evaluation: score with 95% interval per arm, per benchmark and per capability, reliability under faults, safety, and the A/B verdict for two arms.",
			schema:      obj(map[string]any{"evaluation_id": str("Evaluation id")}, "evaluation_id"), readOnly: true,
			run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
				var args struct {
					EvaluationID string `json:"evaluation_id"`
				}
				if err := json.Unmarshal(raw, &args); err != nil || args.EvaluationID == "" {
					return nil, errors.New("evaluation_id is required")
				}
				var ev, rep map[string]any
				if err := s.api.getJSON(ctx, "/evaluations/"+url.PathEscape(args.EvaluationID), nil, &ev); err != nil {
					return nil, err
				}
				if err := s.api.getJSON(ctx, "/evaluations/"+url.PathEscape(args.EvaluationID)+"/report", nil, &rep); err != nil {
					return nil, err
				}
				return map[string]any{"mode": rep["mode"], "status": ev["status"], "report": rep}, nil
			},
		},
		"get_trace": {
			description: "One run's trace: the agent's step timeline and events (json), the steps one per line (jsonl), or the OpenTelemetry export (otlp). Long traces are cut.",
			schema: obj(map[string]any{
				"run_id": str("Run id (from a report card's runs or explain_failure)"),
				"format": map[string]any{"type": "string", "enum": []string{"json", "jsonl", "otlp"}, "description": "Default json"},
			}, "run_id"), readOnly: true,
			run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
				var args struct {
					RunID  string `json:"run_id"`
					Format string `json:"format"`
				}
				if err := json.Unmarshal(raw, &args); err != nil || args.RunID == "" {
					return nil, errors.New("run_id is required")
				}
				q := url.Values{}
				if args.Format != "" {
					q.Set("format", args.Format)
				}
				data, _, err := s.api.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(args.RunID)+"/trace", q, nil)
				if err != nil {
					return nil, err
				}
				text := string(data)
				if len(text) > mcpTraceCap {
					text = text[:mcpTraceCap] + fmt.Sprintf("\n... cut: %d of %d bytes shown; download the full trace from GET /api/v1/runs/%s/trace", mcpTraceCap, len(data), args.RunID)
				}
				return text, nil
			},
		},
		"explain_failure": {
			description: "Explain why an evaluation or a run failed, read only: the failure patterns found per benchmark and arm (loops, tool errors, give-ups, ...) with how often each showed and the runs that show it. Pass evaluation_id, and run_id to focus on one run.",
			schema: obj(map[string]any{
				"evaluation_id": str("Evaluation id"),
				"run_id":        str("Run id; optional, focuses the explanation on that run"),
			}), readOnly: true,
			run: explainFailureTool,
		},
	}
}

// mcpTools lists the tools for tools/list, sorted by name.
func mcpTools() []any {
	names := make([]string, 0, len(mcpToolTable))
	for n := range mcpToolTable {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, n := range names {
		t := mcpToolTable[n]
		out = append(out, map[string]any{
			"name": n, "description": t.description, "inputSchema": t.schema,
			"annotations": map[string]any{"readOnlyHint": t.readOnly, "openWorldHint": false},
		})
	}
	return out
}

func runEvaluationTool(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var args struct {
		Spec        json.RawMessage `json:"spec"`
		Wait        bool            `json:"wait"`
		WaitMinutes int             `json:"wait_minutes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || len(args.Spec) == 0 {
		return nil, errors.New("spec is required")
	}
	var ev map[string]any
	if err := s.api.postJSON(ctx, "/evaluations", args.Spec, &ev); err != nil {
		return nil, err
	}
	id, _ := ev["id"].(string)
	if id == "" {
		return nil, errors.New("Heimdall returned no evaluation id")
	}
	if !args.Wait {
		return map[string]any{"mode": ev["mode"], "evaluation": ev, "next": "call get_report with evaluation_id " + id + " once status is done"}, nil
	}
	wait := 30 * time.Minute
	if args.WaitMinutes > 0 {
		wait = time.Duration(args.WaitMinutes) * time.Minute
	}
	wait = min(wait, mcpMaxWait)
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		status, _ := ev["status"].(string)
		if status == "done" || status == "failed" || status == "cancelled" {
			break
		}
		select {
		case <-wctx.Done():
			return map[string]any{"mode": ev["mode"], "evaluation": ev, "waited": wait.String(),
				"next": "still " + status + "; call get_report with evaluation_id " + id + " later"}, nil
		case <-time.After(s.poll):
		}
		if err := s.api.getJSON(wctx, "/evaluations/"+url.PathEscape(id), nil, &ev); err != nil {
			if wctx.Err() != nil {
				continue // the wait ran out mid-request: report it above
			}
			return nil, err
		}
	}
	var rep map[string]any
	if err := s.api.getJSON(ctx, "/evaluations/"+url.PathEscape(id)+"/report", nil, &rep); err != nil {
		return nil, err
	}
	return map[string]any{"mode": rep["mode"], "status": ev["status"], "evaluation": ev, "report": rep}, nil
}

// explainFailureTool reads the evaluation's experiments' triage (and the run
// when run_id is given) and returns the failure patterns, most common first.
// It never changes anything.
func explainFailureTool(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var args struct {
		EvaluationID string `json:"evaluation_id"`
		RunID        string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.EvaluationID == "" && args.RunID == "" {
		return nil, errors.New("evaluation_id or run_id is required")
	}
	out := map[string]any{}
	var run map[string]any
	expIDs := []string{}
	benchOf := map[string]string{}
	if args.RunID != "" {
		if err := s.api.getJSON(ctx, "/runs/"+url.PathEscape(args.RunID), nil, &run); err != nil {
			return nil, err
		}
		out["mode"] = run["mode"]
		out["run"] = runSummary(run)
		if exp := nestedString(run, "run.exp_id", "slot.exp_id"); exp != "" {
			expIDs = append(expIDs, exp)
		}
	}
	if args.EvaluationID != "" {
		var ev struct {
			Mode     string `json:"mode"`
			Status   string `json:"status"`
			Children []struct {
				Benchmark    string `json:"benchmark"`
				ExperimentID string `json:"experiment_id"`
				Status       string `json:"status"`
				Skipped      string `json:"skipped"`
			} `json:"children"`
		}
		if err := s.api.getJSON(ctx, "/evaluations/"+url.PathEscape(args.EvaluationID), nil, &ev); err != nil {
			return nil, err
		}
		out["mode"], out["evaluation_status"] = ev.Mode, ev.Status
		var skipped []any
		for _, c := range ev.Children {
			if c.ExperimentID == "" {
				skipped = append(skipped, map[string]any{"benchmark": c.Benchmark, "status": c.Status, "reason": c.Skipped})
				continue
			}
			benchOf[c.ExperimentID] = c.Benchmark
			if !containsString(expIDs, c.ExperimentID) {
				expIDs = append(expIDs, c.ExperimentID)
			}
		}
		if len(skipped) > 0 {
			out["skipped_benchmarks"] = skipped
		}
	}
	type pattern struct {
		Benchmark   string         `json:"benchmark,omitempty"`
		Experiment  string         `json:"experiment_id"`
		Pattern     string         `json:"pattern"`
		Label       string         `json:"label"`
		Description string         `json:"description"`
		PerArm      map[string]any `json:"per_arm"`
		Runs        map[string]any `json:"runs"`
		ThisRun     bool           `json:"this_run,omitempty"`
		count       int
	}
	var patterns []pattern
	for _, exp := range expIDs {
		var tri struct {
			Groups []struct {
				Pattern     string              `json:"pattern"`
				Label       string              `json:"label"`
				Description string              `json:"description"`
				PerArm      map[string]any      `json:"per_arm"`
				Runs        map[string][]string `json:"runs"`
			} `json:"groups"`
		}
		if err := s.api.getJSON(ctx, "/experiments/"+url.PathEscape(exp)+"/triage", nil, &tri); err != nil {
			var ae *mcpAPIError
			if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
				continue // not analysed yet
			}
			return nil, err
		}
		for _, g := range tri.Groups {
			p := pattern{Benchmark: benchOf[exp], Experiment: exp, Pattern: g.Pattern, Label: g.Label, Description: g.Description, PerArm: g.PerArm, Runs: map[string]any{}}
			for arm, ids := range g.Runs {
				p.count += len(ids)
				if containsString(ids, args.RunID) {
					p.ThisRun = true
				}
				if len(ids) > 10 {
					p.Runs[arm] = map[string]any{"first": ids[:10], "total": len(ids)}
				} else {
					p.Runs[arm] = ids
				}
			}
			if p.count == 0 {
				continue
			}
			if args.RunID != "" && args.EvaluationID == "" && !p.ThisRun {
				continue
			}
			patterns = append(patterns, p)
		}
	}
	sort.SliceStable(patterns, func(i, j int) bool {
		if patterns[i].ThisRun != patterns[j].ThisRun {
			return patterns[i].ThisRun
		}
		return patterns[i].count > patterns[j].count
	})
	out["patterns"] = patterns
	switch {
	case len(patterns) == 0 && len(expIDs) == 0:
		out["summary"] = "No benchmark of this evaluation has run yet, so there is nothing to explain."
	case len(patterns) == 0:
		out["summary"] = "No failure pattern was found in the analysed runs. Read a failing run's trace with get_trace."
	default:
		top := patterns[0]
		where := top.Benchmark
		if where == "" {
			where = top.Experiment
		}
		out["summary"] = fmt.Sprintf("Most common failure: %s (%s) in %d runs on %s. %d patterns in all; open a run with get_trace to see the steps.",
			top.Label, top.Pattern, top.count, where, len(patterns))
	}
	return out, nil
}

// runSummary keeps the parts of a run detail that explain its outcome.
func runSummary(run map[string]any) map[string]any {
	keep := map[string]any{}
	for _, k := range []string{"grade", "gates", "metrics", "seal_error"} {
		if v, ok := run[k]; ok && v != nil {
			keep[k] = v
		}
	}
	for _, src := range []string{"slot", "run"} {
		m, _ := run[src].(map[string]any)
		for _, k := range []string{"run_id", "exp_id", "suite", "task", "arm", "condition", "trial", "outcome", "error"} {
			if v, ok := m[k]; ok {
				if _, have := keep[k]; !have {
					keep[k] = v
				}
			}
		}
	}
	return keep
}

// nestedString returns the first string found at one of the dotted paths.
func nestedString(m map[string]any, paths ...string) string {
	for _, p := range paths {
		var cur any = m
		for _, part := range strings.Split(p, ".") {
			mm, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = mm[part]
		}
		if s, ok := cur.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func containsString(list []string, s string) bool {
	if s == "" {
		return false
	}
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
