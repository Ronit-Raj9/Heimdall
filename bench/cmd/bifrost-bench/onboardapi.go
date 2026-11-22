package main

// Onboarding calls shared by `heimdall connect`, `heimdall first-look`,
// `heimdall serve <target>` and the MCP tools connect_agent, verify_agent
// and first_look (lane Z02, audit/INFRA_I2_AGENT_ONBOARDING.md items 6-8).
// They talk to a Heimdall server with the org API token `heimdall login`
// stored (or HEIMDALL_TOKEN). Server-side detection and verification are
// lane Z01's routes; when a server does not have them yet, the calls fall
// back to what it does have and say so.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Routes the onboarding flow calls.
const (
	connectDetectPath = "/api/v1/connect/detect"          // Z01: server-side detection
	connectVerifyPath = "/api/v1/connect/verify"          // Z01: agent.verify job (GET .../{job} polls it)
	agentTestV1Path   = "/api/v1/agents/%s/test"          // Y10: one sample task
	agentsV1Path      = "/api/v1/agents"                  // Y10: create from YAML
	agentConfigPath   = "/api/bench/agents/%s/config"     // r-merge: write agents/<id>.yaml
	pullAgentsPath    = "/api/v1/pull/agents"             // Y11: is a process serving it
	compatPath        = "/api/v1/agents/%s/compatibility" // which benchmarks it can run
	estimatePath      = "/api/v1/evaluations/estimate"    //
	evaluationsPath   = "/api/v1/evaluations"             //
	evaluationPath    = "/api/v1/evaluations/%s"          //
	reportPath        = "/api/v1/evaluations/%s/report"   //
	firstLookTasks    = 5                                 // first look: 5 tasks
	firstLookTrials   = 1                                 // by 1 trial
	firstLookName     = "First look"                      //
	pullImage         = "bench/coding-core-base:1"        // the pull launcher's image (docs/pull-agents.md)
	onboardUA         = "heimdall-cli/"                   //
	firstLookNote     = "a first look is 5 tasks by 1 trial: too small for a verdict, never a release gate"
)

// onboardAPI calls a Heimdall server with a token.
type onboardAPI struct {
	base   string
	token  string
	client *http.Client
}

func newOnboardAPI(base, token string) *onboardAPI {
	return &onboardAPI{base: strings.TrimRight(base, "/"), token: token, client: &http.Client{Timeout: 60 * time.Second}}
}

// call sends one request; a non-2xx answer is an *mcpAPIError with the
// server's message.
func (a *onboardAPI) call(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", onboardUA+benchVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, &mcpAPIError{Message: "cannot reach Heimdall at " + a.base + ": " + err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		e := &mcpAPIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(data))}
		var eb struct {
			Error struct{ Message, Type string } `json:"error"`
		}
		if json.Unmarshal(data, &eb) == nil && eb.Error.Message != "" {
			e.Message, e.Type = eb.Error.Message, eb.Error.Type
		}
		return resp.StatusCode, e
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode, nil
}

// missingRoute reports a route the server does not have (an older server,
// or a lane not merged yet).
func missingRoute(err error) bool {
	var e *mcpAPIError
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Status == http.StatusMethodNotAllowed) &&
		(e.Type == "" || e.Type == "not_found" || strings.Contains(strings.ToLower(e.Message), "route") || strings.Contains(strings.ToLower(e.Message), "not found"))
}

// ---- detection (Z01) ----

// remoteDetection is POST /api/v1/connect/detect's answer, read loosely:
// the agent config comes as yaml, agent_yaml or an agent object.
type remoteDetection struct {
	Kind       string          `json:"kind"`
	Summary    string          `json:"summary"`
	Confidence string          `json:"confidence"`
	YAML       string          `json:"yaml"`
	AgentYAML  string          `json:"agent_yaml"`
	Agent      json.RawMessage `json:"agent"`
	Evidence   []string        `json:"evidence"`
	Secrets    []struct {
		Name   string `json:"name"`
		Header string `json:"header"`
		Prompt string `json:"prompt"`
	} `json:"secrets"`
	Failure *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Fix     string `json:"fix"`
	} `json:"failure"`
	Code string         `json:"code"`
	Raw  map[string]any `json:"-"`
}

// config returns the detected agent config as YAML, or "".
func (d *remoteDetection) config() string {
	switch {
	case d.YAML != "":
		return d.YAML
	case d.AgentYAML != "":
		return d.AgentYAML
	}
	return ""
}

// failureCode is the detection's failure code, "" when it found the agent.
func (d *remoteDetection) failureCode() string {
	if d.Failure != nil && d.Failure.Code != "" {
		return d.Failure.Code
	}
	return d.Code
}

// localOnly reports failure codes that mean the server can not reach the
// agent, but this machine may: relay it with `heimdall serve <url>`.
func localOnly(code string) bool {
	switch code {
	case "dns", "private_address", "unreachable", "timeout", "connect", "tls", "bind_loopback":
		return true
	}
	return false
}

func (a *onboardAPI) detect(ctx context.Context, input string) (*remoteDetection, error) {
	var raw map[string]any
	if _, err := a.call(ctx, http.MethodPost, connectDetectPath, map[string]any{"input": input}, &raw); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(raw)
	var d remoteDetection
	_ = json.Unmarshal(b, &d)
	if len(d.Agent) > 0 && d.config() == "" && string(d.Agent) != "null" {
		if y, err := jsonToYAML(d.Agent); err == nil {
			d.YAML = y
		}
	}
	d.Raw = raw
	return &d, nil
}

// ---- agents ----

var agentNameClean = regexp.MustCompile(`[^a-z0-9._-]+`)

// agentNameFrom makes a valid agent id out of a host, folder or object name.
func agentNameFrom(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = agentNameClean.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-._")
	}
	if s == "" || s == "new" || s == "config" || s == "templates" {
		s = "my-agent"
	}
	return s
}

// pullAgentYAML is the config of an agent served from the user's own
// process (pull mode, Y11).
func pullAgentYAML(id, framework string) string {
	return fmt.Sprintf(`# Connected with heimdall connect (%s).
id: %s
# Served from your own process with heimdall serve (pull mode, docs/pull-agents.md):
# nothing connects in to your network.
adapter: pull
image: %s
budgets: {wall_s: 900, cpus: 1, mem_gb: 2}
pull:
  name: %s
`, framework, id, pullImage, id)
}

// registerAgent creates the agent (API v1, Y10) or, on a server without
// that route, writes its config file. exists true means the name was taken
// and the config was left as it was.
func (a *onboardAPI) registerAgent(ctx context.Context, id, yamlDoc string) (exists bool, err error) {
	_, err = a.call(ctx, http.MethodPost, agentsV1Path, map[string]any{"yaml": yamlDoc}, nil)
	if err == nil {
		return false, nil
	}
	var e *mcpAPIError
	if errors.As(err, &e) && e.Status == http.StatusConflict {
		return true, nil
	}
	if !missingRoute(err) {
		return false, err
	}
	_, err = a.call(ctx, http.MethodPut, fmt.Sprintf(agentConfigPath, url.PathEscape(id)), map[string]any{"yaml": yamlDoc}, nil)
	return false, err
}

// ---- verification ----

// verifyRow is one check of a verification.
type verifyRow struct {
	Check   string `json:"check"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Code    string `json:"code,omitempty"`
	Fix     string `json:"fix,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// verifyResult is what verify found and which route answered.
type verifyResult struct {
	Agent  string         `json:"agent"`
	OK     bool           `json:"ok"`
	Source string         `json:"source"` // agent.verify | pull | agent.test
	Rows   []verifyRow    `json:"rows"`
	Raw    map[string]any `json:"raw,omitempty"`
}

// verify checks the agent: Z01's agent.verify when the server has it,
// else whether a process serves a pull agent, else Y10's one sample task.
func (a *onboardAPI) verify(ctx context.Context, id string, pull bool) (*verifyResult, error) {
	out := &verifyResult{Agent: id}
	var raw map[string]any
	_, err := a.call(ctx, http.MethodPost, connectVerifyPath, map[string]any{"agent": id}, &raw)
	if err == nil {
		out.Source, out.Raw = "agent.verify", raw
		if job := jobIDOf(raw); job != "" {
			if done, werr := a.waitVerify(ctx, job, 5*time.Minute); werr == nil {
				raw = done
				out.Raw = done
			}
		}
		out.Rows = rowsOf(raw)
		out.OK = allOK(out.Rows, raw)
		return out, nil
	}
	if !missingRoute(err) {
		return nil, err
	}
	if pull {
		out.Source = "pull"
		row, perr := a.pullConnected(ctx, id)
		if perr != nil {
			return nil, perr
		}
		out.Rows = []verifyRow{row}
		out.OK = row.OK
		return out, nil
	}
	_, err = a.call(ctx, http.MethodPost, fmt.Sprintf(agentTestV1Path, url.PathEscape(id)), map[string]any{}, &raw)
	if err != nil {
		if missingRoute(err) {
			return nil, errors.New("this server has no agent verification route yet (needs /api/v1/connect/verify or /api/v1/agents/{id}/test); run a first look instead")
		}
		return nil, err
	}
	out.Source, out.Raw = "agent.test", raw
	ok, _ := raw["ok"].(bool)
	detail, _ := raw["answer"].(string)
	if why, _ := raw["error"].(string); why != "" {
		detail = why
	}
	out.Rows = []verifyRow{{Check: "answer", OK: ok, Detail: clip(detail, 200)}}
	out.OK = ok
	return out, nil
}

// pullConnected is the pull agent's serving status (GET /api/v1/pull/agents).
func (a *onboardAPI) pullConnected(ctx context.Context, id string) (verifyRow, error) {
	var doc struct {
		Agents []map[string]any `json:"agents"`
	}
	if _, err := a.call(ctx, http.MethodGet, pullAgentsPath, nil, &doc); err != nil {
		if missingRoute(err) {
			return verifyRow{}, errors.New("this server has no pull-mode routes (/api/v1/pull); it needs Heimdall with pull agents (docs/pull-agents.md)")
		}
		return verifyRow{}, err
	}
	for _, ag := range doc.Agents {
		name, _ := ag["name"].(string)
		if name == "" {
			name, _ = ag["agent"].(string)
		}
		if name != id {
			continue
		}
		connected, _ := ag["connected"].(bool)
		if connected {
			return verifyRow{Check: "serving", OK: true, Detail: "a process is serving " + id}, nil
		}
		return verifyRow{Check: "serving", OK: false, Code: "not_serving", Detail: "no process serves " + id + " yet", Fix: "heimdall serve"}, nil
	}
	return verifyRow{Check: "serving", OK: false, Code: "not_serving", Detail: "no process has served " + id + " yet", Fix: "heimdall serve"}, nil
}

func jobIDOf(raw map[string]any) string {
	if j, ok := raw["job"].(map[string]any); ok {
		if id, _ := j["id"].(string); id != "" {
			return id
		}
	}
	if id, _ := raw["job_id"].(string); id != "" {
		return id
	}
	if k, _ := raw["kind"].(string); k == "agent.verify" {
		id, _ := raw["id"].(string)
		return id
	}
	return ""
}

// waitVerify polls Z01's agent.verify job (GET /api/v1/connect/verify/{job})
// until it ends and returns its result (ok, rows), else the last status.
func (a *onboardAPI) waitVerify(ctx context.Context, id string, max time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(max)
	for {
		var st map[string]any
		if _, err := a.call(ctx, http.MethodGet, connectVerifyPath+"/"+url.PathEscape(id), nil, &st); err != nil {
			return nil, err
		}
		switch st["status"] {
		case "succeeded", "failed", "cancelled":
			if res, ok := st["result"].(map[string]any); ok {
				return res, nil
			}
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, errors.New("verification still running")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// waitJob polls a job until it ends.
func (a *onboardAPI) waitJob(ctx context.Context, id string, max time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(max)
	for {
		var j map[string]any
		if _, err := a.call(ctx, http.MethodGet, "/api/bench/jobs/"+url.PathEscape(id), nil, &j); err != nil {
			return nil, err
		}
		switch j["status"] {
		case "succeeded", "failed", "cancelled":
			if res, ok := j["result"].(map[string]any); ok {
				return res, nil
			}
			return j, nil
		}
		if time.Now().After(deadline) {
			return j, errors.New("verification still running")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func rowsOf(raw map[string]any) []verifyRow {
	var rows []verifyRow
	for _, key := range []string{"rows", "checks"} {
		list, _ := raw[key].([]any)
		for _, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			r := verifyRow{}
			for _, k := range []string{"check", "name", "id"} {
				if s, _ := m[k].(string); s != "" {
					r.Check = s
					break
				}
			}
			r.OK, _ = m["ok"].(bool)
			if st, _ := m["status"].(string); st != "" {
				r.OK = st == "pass" || st == "ok" || st == "passed"
				r.Skipped = st == "skip" || st == "skipped"
			}
			for _, k := range []string{"detail", "message", "evidence"} {
				if s, _ := m[k].(string); s != "" {
					r.Detail = s
					break
				}
			}
			r.Code, _ = m["code"].(string)
			r.Fix, _ = m["fix"].(string)
			rows = append(rows, r)
		}
	}
	return rows
}

func allOK(rows []verifyRow, raw map[string]any) bool {
	if ok, has := raw["ok"].(bool); has {
		return ok
	}
	if len(rows) == 0 {
		return false
	}
	for _, r := range rows {
		if !r.OK && !r.Skipped {
			return false
		}
	}
	return true
}

// ---- first look ----

// firstLookPlan is which benchmark a first look runs and what it costs.
type firstLookPlan struct {
	Agent      string         `json:"agent"`
	Model      string         `json:"model,omitempty"`
	Benchmark  string         `json:"benchmark"`
	Tasks      int            `json:"tasks"`
	Trials     int            `json:"trials"`
	Minutes    map[string]any `json:"est_minutes,omitempty"`
	CostUSD    map[string]any `json:"est_cost_usd,omitempty"`
	Why        string         `json:"why"`
	Considered int            `json:"benchmarks_considered"`
	Note       string         `json:"note"`
}

// firstLookSpec is the evaluation a plan starts.
func (p *firstLookPlan) spec() map[string]any {
	return map[string]any{
		"name": firstLookName + ": " + p.Agent, "mode": "score",
		"arms":       []any{map[string]any{"label": "A", "agent": p.Agent, "model": p.Model}},
		"benchmarks": []string{p.Benchmark}, "tasks_per_benchmark": p.Tasks, "trials": p.Trials,
	}
}

// planFirstLook picks the fastest benchmark the agent can run, by the
// server's own estimate for 5 tasks by 1 trial. benchmark, when set, is
// used as it is.
func (a *onboardAPI) planFirstLook(ctx context.Context, agent, model, benchmark string) (*firstLookPlan, error) {
	p := &firstLookPlan{Agent: agent, Model: model, Tasks: firstLookTasks, Trials: firstLookTrials, Note: firstLookNote}
	var candidates []string
	if benchmark != "" {
		candidates = []string{benchmark}
		p.Why = "the benchmark you named"
	} else {
		var compat struct {
			Benchmarks []struct {
				ID string `json:"id"`
				OK bool   `json:"ok"`
			} `json:"benchmarks"`
		}
		if _, err := a.call(ctx, http.MethodGet, fmt.Sprintf(compatPath, url.PathEscape(agent)), nil, &compat); err != nil {
			return nil, fmt.Errorf("which benchmarks %s can run: %w", agent, err)
		}
		for _, b := range compat.Benchmarks {
			if b.OK {
				candidates = append(candidates, b.ID)
			}
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no benchmark in this organization's library fits %s yet: add one under Benchmarks, or give the agent capabilities", agent)
		}
	}
	p.Considered = len(candidates)
	spec := map[string]any{"name": firstLookName, "mode": "score", "arms": []any{map[string]any{"label": "A", "agent": agent, "model": model}},
		"benchmarks": candidates, "tasks_per_benchmark": firstLookTasks, "trials": firstLookTrials}
	var est struct {
		PerBenchmark []struct {
			Benchmark  string         `json:"benchmark"`
			Skipped    string         `json:"skipped"`
			EstMinutes map[string]any `json:"est_minutes"`
			EstCostUSD map[string]any `json:"est_cost_usd"`
		} `json:"per_benchmark"`
	}
	if _, err := a.call(ctx, http.MethodPost, estimatePath, spec, &est); err != nil {
		return nil, fmt.Errorf("estimate: %w", err)
	}
	type cand struct {
		id    string
		known bool
		high  float64
		min   map[string]any
		cost  map[string]any
	}
	var cs []cand
	for _, b := range est.PerBenchmark {
		if b.Skipped != "" {
			continue
		}
		known, _ := b.EstMinutes["known"].(bool)
		high, _ := b.EstMinutes["high"].(float64)
		cs = append(cs, cand{b.Benchmark, known, high, b.EstMinutes, b.EstCostUSD})
	}
	if len(cs) == 0 {
		return nil, fmt.Errorf("none of the %d benchmarks %s fits can run now (the estimate skipped them all)", len(candidates), agent)
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].known != cs[j].known {
			return cs[i].known
		}
		return cs[i].high < cs[j].high
	})
	best := cs[0]
	p.Benchmark, p.Minutes, p.CostUSD = best.id, best.min, best.cost
	if p.Why == "" {
		if best.known {
			p.Why = fmt.Sprintf("the fastest of %d benchmarks %s can run, by the estimate", len(cs), agent)
		} else {
			p.Why = fmt.Sprintf("no benchmark has timing history yet; %s is the first of %d %s can run", best.id, len(cs), agent)
		}
	}
	return p, nil
}

// startFirstLook starts the evaluation: the server's first_look preset
// when it has one (Z01, burst slots), else the plan's explicit spec.
func (a *onboardAPI) startFirstLook(ctx context.Context, p *firstLookPlan, usePreset bool) (map[string]any, error) {
	var ev map[string]any
	if usePreset {
		spec := map[string]any{"name": firstLookName + ": " + p.Agent, "mode": "score", "preset": "first_look",
			"arms": []any{map[string]any{"label": "A", "agent": p.Agent, "model": p.Model}}}
		if p.Benchmark != "" {
			spec["benchmarks"] = []string{p.Benchmark}
		}
		_, err := a.call(ctx, http.MethodPost, evaluationsPath, spec, &ev)
		if err == nil {
			return ev, nil
		}
		var e *mcpAPIError
		if !errors.As(err, &e) || e.Status != http.StatusBadRequest || !strings.Contains(strings.ToLower(e.Message), "preset") {
			return nil, err
		}
	}
	if _, err := a.call(ctx, http.MethodPost, evaluationsPath, p.spec(), &ev); err != nil {
		return nil, err
	}
	return ev, nil
}

// waitEvaluation polls an evaluation until it ends or max passes.
func (a *onboardAPI) waitEvaluation(ctx context.Context, id string, max time.Duration, tick func(map[string]any)) (map[string]any, error) {
	deadline := time.Now().Add(max)
	for {
		var ev map[string]any
		if _, err := a.call(ctx, http.MethodGet, fmt.Sprintf(evaluationPath, url.PathEscape(id)), nil, &ev); err != nil {
			return nil, err
		}
		if tick != nil {
			tick(ev)
		}
		switch ev["status"] {
		case "done", "failed", "cancelled":
			return ev, nil
		}
		if time.Now().After(deadline) {
			return ev, nil
		}
		select {
		case <-ctx.Done():
			return ev, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *onboardAPI) report(ctx context.Context, id string) (map[string]any, error) {
	var rep map[string]any
	_, err := a.call(ctx, http.MethodGet, fmt.Sprintf(reportPath, url.PathEscape(id)), nil, &rep)
	return rep, err
}

// evaluationLink is the report card page of an evaluation.
func evaluationLink(base, id string) string {
	return strings.TrimRight(base, "/") + "/evaluations/" + url.PathEscape(id)
}

// jsonToYAML re-encodes a JSON object as YAML.
func jsonToYAML(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	b, err := yaml.Marshal(v)
	return string(b), err
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// estimateRangeText prints an estimate range like "2.1 to 3.4 min".
func estimateRangeText(m map[string]any, unit string, prefix string) string {
	if m == nil {
		return "unknown"
	}
	known, _ := m["known"].(bool)
	lo, _ := m["low"].(float64)
	hi, _ := m["high"].(float64)
	if !known && hi == 0 {
		return "unknown"
	}
	s := fmt.Sprintf("%s%.2f to %s%.2f%s", prefix, lo, prefix, hi, unit)
	if !known {
		s += " (partly unknown)"
	}
	return s
}
