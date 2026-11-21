package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// `ci evaluate`: the hosted pull-request gate. It registers the pull
// request's agent config (or the live config with a new image or endpoint)
// as a version on a hosted Heimdall, evaluates it against the live version,
// waits, fetches the report card, applies the evaluation gate, posts one
// pull-request comment and sets the commit status. The GitHub Action
// (ci/action.yml, mode evaluate) and the GitLab CI template both run it.

const ciEvaluateUsage = `usage: bifrost-bench ci evaluate -agent <id> [-config agents/<id>.yaml | -image <ref@sha256:...> | -endpoint <url>]
           [-url https://heimdall.example.com] [-pack coding] [-benchmark <id>]... [-fault model-down]...
           [-baseline live|<version>] [-model <model>] [-trials 2] [-tasks 8] [-budget-usd 20]
           [-fail-on regressed,failed] [-max-score-drop 0.05] [-max-cost-increase 0.25]
           [-timeout 3h] [-poll 20s] [-out <dir>] [-provider auto|github|gitlab|none] [-no-comment] [-no-status]

The org API token is read from HEIMDALL_TOKEN (never a flag). Every flag
has a HEIMDALL_<NAME> environment fallback (HEIMDALL_URL, HEIMDALL_AGENT,
HEIMDALL_BENCHMARKS=a,b, HEIMDALL_FAULTS=..., HEIMDALL_FAIL_ON=...).
Exit: 0 pass, 1 regressed, 2 inconclusive, 3 evaluation failed, 4 error, 64 usage.`

// ciEvalOptions are the parsed settings of one `ci evaluate`.
type ciEvalOptions struct {
	url, token, agent, config, image, endpoint string
	baseline, model, provider, pack, out       string
	benchmarks, faults                         []string
	trials, tasks                              int
	budget, maxDrop, maxCost                   float64
	failOn                                     []string
	timeout, poll                              time.Duration
	noComment, noStatus                        bool
}

// ciEnvOr returns the HEIMDALL_<name> variable when the flag was not given.
func ciEnvOr(fs *flag.FlagSet, flagName, env string, cur string) string {
	if flagSet(fs, flagName) {
		return cur
	}
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v
	}
	return cur
}

func parseCIEvaluate(args []string) (*ciEvalOptions, error) {
	fs := flag.NewFlagSet("ci evaluate", flag.ContinueOnError)
	o := &ciEvalOptions{}
	fs.StringVar(&o.url, "url", "", "hosted Heimdall address (HEIMDALL_URL)")
	fs.StringVar(&o.agent, "agent", "", "agent id on the hosted Heimdall (HEIMDALL_AGENT)")
	fs.StringVar(&o.config, "config", "", "the agent config file in this repository to register as the pull request's version (HEIMDALL_CONFIG)")
	fs.StringVar(&o.image, "image", "", "an image the pull request built (ref, ideally ref@sha256:...): the live config with this image (HEIMDALL_IMAGE)")
	fs.StringVar(&o.endpoint, "endpoint", "", "a preview deployment URL: the live config with this remote endpoint (HEIMDALL_ENDPOINT)")
	fs.StringVar(&o.baseline, "baseline", "live", "version to compare with: live (the agent's saved config) or a version number (HEIMDALL_BASELINE)")
	fs.StringVar(&o.model, "model", "", "model for both versions; empty uses each version's own model (HEIMDALL_MODEL)")
	fs.StringVar(&o.pack, "pack", "", "benchmark pack, e.g. coding (HEIMDALL_PACK)")
	var benches, faults listFlag
	fs.Var(&benches, "benchmark", "benchmark id (repeatable; HEIMDALL_BENCHMARKS, comma-separated)")
	fs.Var(&faults, "fault", "fault to add besides clean: model-down, broken-tool, misleading-instruction (repeatable; HEIMDALL_FAULTS)")
	trials := fs.String("trials", "", "repeats per task (HEIMDALL_TRIALS); empty uses the server default")
	tasks := fs.String("tasks", "", "tasks per benchmark (HEIMDALL_TASKS); empty uses the server default")
	budget := fs.String("budget-usd", "", "spend cap of the evaluation in USD (HEIMDALL_BUDGET_USD)")
	failOn := fs.String("fail-on", strings.Join(ci.DefaultFailOn, ","), "outcomes that fail: regressed, inconclusive, failed (HEIMDALL_FAIL_ON)")
	maxDrop := fs.String("max-score-drop", "", "fail when a benchmark's score falls by more than this (0.05 = 5 points) (HEIMDALL_MAX_SCORE_DROP)")
	maxCost := fs.String("max-cost-increase", "", "fail when total cost grows by more than this fraction (0.25 = 25%) (HEIMDALL_MAX_COST_INCREASE)")
	timeout := fs.String("timeout", "3h", "how long to wait for the evaluation (HEIMDALL_TIMEOUT)")
	poll := fs.String("poll", "20s", "how often to check on it (HEIMDALL_POLL)")
	fs.StringVar(&o.out, "out", "", "write report.json, gate.json and comment.md here (HEIMDALL_OUT)")
	fs.StringVar(&o.provider, "provider", "auto", "where to post the comment and status: auto, github, gitlab or none (HEIMDALL_PROVIDER)")
	fs.BoolVar(&o.noComment, "no-comment", false, "do not post the pull request comment")
	fs.BoolVar(&o.noStatus, "no-status", false, "do not set the commit status")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, fmt.Errorf("unexpected arguments %q", pos)
	}
	o.url = ciEnvOr(fs, "url", "HEIMDALL_URL", o.url)
	o.agent = ciEnvOr(fs, "agent", "HEIMDALL_AGENT", o.agent)
	o.config = ciEnvOr(fs, "config", "HEIMDALL_CONFIG", o.config)
	o.image = ciEnvOr(fs, "image", "HEIMDALL_IMAGE", o.image)
	o.endpoint = ciEnvOr(fs, "endpoint", "HEIMDALL_ENDPOINT", o.endpoint)
	o.baseline = ciEnvOr(fs, "baseline", "HEIMDALL_BASELINE", o.baseline)
	o.model = ciEnvOr(fs, "model", "HEIMDALL_MODEL", o.model)
	o.pack = ciEnvOr(fs, "pack", "HEIMDALL_PACK", o.pack)
	o.out = ciEnvOr(fs, "out", "HEIMDALL_OUT", o.out)
	o.provider = ciEnvOr(fs, "provider", "HEIMDALL_PROVIDER", o.provider)
	o.benchmarks = benches
	if len(o.benchmarks) == 0 {
		o.benchmarks = splitList(os.Getenv("HEIMDALL_BENCHMARKS"))
	}
	o.faults = faults
	if len(o.faults) == 0 {
		o.faults = splitList(os.Getenv("HEIMDALL_FAULTS"))
	}
	o.token = strings.TrimSpace(os.Getenv("HEIMDALL_TOKEN"))

	num := func(name, env, raw string, dst *float64) error {
		raw = ciEnvOr(fs, name, env, raw)
		if raw == "" {
			return nil
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			return fmt.Errorf("-%s %q: want a number from 0", name, raw)
		}
		*dst = v
		return nil
	}
	var tr, tk float64
	for _, n := range []struct {
		name, env, raw string
		dst            *float64
	}{
		{"trials", "HEIMDALL_TRIALS", *trials, &tr}, {"tasks", "HEIMDALL_TASKS", *tasks, &tk},
		{"budget-usd", "HEIMDALL_BUDGET_USD", *budget, &o.budget},
		{"max-score-drop", "HEIMDALL_MAX_SCORE_DROP", *maxDrop, &o.maxDrop},
		{"max-cost-increase", "HEIMDALL_MAX_COST_INCREASE", *maxCost, &o.maxCost},
	} {
		if err := num(n.name, n.env, n.raw, n.dst); err != nil {
			return nil, err
		}
	}
	o.trials, o.tasks = int(tr), int(tk)
	if o.failOn, err = ci.ParseFailOn(ciEnvOr(fs, "fail-on", "HEIMDALL_FAIL_ON", *failOn)); err != nil {
		return nil, err
	}
	for _, d := range []struct {
		name, env, raw string
		dst            *time.Duration
	}{{"timeout", "HEIMDALL_TIMEOUT", *timeout, &o.timeout}, {"poll", "HEIMDALL_POLL", *poll, &o.poll}} {
		raw := ciEnvOr(fs, d.name, d.env, d.raw)
		v, err := time.ParseDuration(raw)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("-%s %q: want a duration such as 3h or 20s", d.name, raw)
		}
		*d.dst = v
	}

	switch {
	case o.url == "":
		return nil, errors.New("the Heimdall address is missing: set -url or HEIMDALL_URL")
	case o.token == "":
		return nil, errors.New("the org API token is missing: set HEIMDALL_TOKEN (create one in Heimdall under Settings > API tokens)")
	case o.agent == "":
		return nil, errors.New("the agent is missing: set -agent or HEIMDALL_AGENT")
	case o.config == "" && o.image == "" && o.endpoint == "":
		return nil, errors.New("say what the pull request changes: -config, -image or -endpoint")
	case o.pack == "" && len(o.benchmarks) == 0:
		return nil, errors.New("pick benchmarks: -pack or -benchmark")
	}
	u, err := url.Parse(o.url)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("-url %q: want the http(s) address of your Heimdall", o.url)
	}
	if u.Scheme == "http" && !loopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("-url %q: the API token would cross the network unencrypted; use https", o.url)
	}
	o.url = strings.TrimRight(o.url, "/")
	switch o.provider {
	case "auto", "github", "gitlab", "none":
	default:
		return nil, fmt.Errorf("-provider %q: want auto, github, gitlab or none", o.provider)
	}
	return o, nil
}

func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// heimdallClient calls the hosted Heimdall API with the org API token.
type heimdallClient struct {
	base, token string
	http        *http.Client
}

// hostedAPIError is a non-2xx answer.
type hostedAPIError struct {
	Status  int
	Message string
}

func (e *hostedAPIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

func (c *heimdallClient) do(ctx context.Context, method, path string, body, out any) error {
	var lastErr error
	tries := 1
	if method == http.MethodGet {
		tries = 4 // reads are safe to retry over a network blip or a restart
	}
	for i := 0; i < tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i*i) * time.Second):
			}
		}
		err := c.once(ctx, method, path, body, out)
		var ae *hostedAPIError
		if err == nil || ctx.Err() != nil || (errors.As(err, &ae) && ae.Status < 500 && ae.Status != http.StatusTooManyRequests) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (c *heimdallClient) once(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/bench"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var eb struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &eb) == nil && eb.Error.Message != "" {
			msg = eb.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if resp.StatusCode == http.StatusUnauthorized {
			msg += " (check HEIMDALL_TOKEN: an org API token that is not revoked or expired)"
		}
		return &hostedAPIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decode answer: %w", method, path, err)
		}
	}
	return nil
}

// Wire types of the hosted API this command reads (handlers package).
type ciVersionList struct {
	Agent    string `json:"agent"`
	Builtin  bool   `json:"builtin"`
	Live     int    `json:"live"`
	Versions []struct {
		Version int    `json:"version"`
		Hash    string `json:"hash"`
	} `json:"versions"`
}

type ciVersionDoc struct {
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	YAML    string `json:"yaml"`
}

type ciVersionCreated struct {
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	Created bool   `json:"created"`
}

// runCIEvaluate is `ci evaluate`.
func runCIEvaluate(args []string) int {
	o, err := parseCIEvaluate(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s ci evaluate: %v\n\n%s\n", progName, err, ciEvaluateUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ciEvaluate(ctx, o, detectPR(o.provider), os.Stdout)
}

// ciEvaluate runs the gate. pr may be nil (no comment, no status).
func ciEvaluate(ctx context.Context, o *ciEvalOptions, pr prReporter, stdout io.Writer) int {
	c := &heimdallClient{base: o.url, token: o.token, http: &http.Client{Timeout: 2 * time.Minute}}
	logf := func(format string, args ...any) { fmt.Fprintf(stdout, format+"\n", args...) }
	setReporterAgent(pr, o.agent)
	if pr != nil && (o.noComment || o.noStatus) {
		pr = &filteredReporter{inner: pr, noComment: o.noComment, noStatus: o.noStatus}
	}
	agentPath := "/agents/" + url.PathEscape(o.agent)

	var vl ciVersionList
	if err := c.do(ctx, http.MethodGet, agentPath+"/versions", nil, &vl); err != nil {
		return fail("ci evaluate: read versions of agent %s: %v", o.agent, err)
	}
	if vl.Builtin {
		return fail("ci evaluate: agent %s is built in and read-only; register your own agent on Heimdall and point -agent at it", o.agent)
	}
	baseline := vl.Live
	if o.baseline != "" && o.baseline != "live" {
		n, err := strconv.Atoi(strings.TrimPrefix(o.baseline, "v"))
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "%s ci evaluate: -baseline %q: want live or a version number\n", progName, o.baseline)
			return exitUsage
		}
		baseline = n
	}
	if baseline == 0 {
		return fail("ci evaluate: agent %s has no live version: save its config on the agent page first", o.agent)
	}
	var live ciVersionDoc
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/versions/%d", agentPath, baseline), nil, &live); err != nil {
		return fail("ci evaluate: read version %d of agent %s: %v", baseline, o.agent, err)
	}

	doc := []byte(live.YAML)
	if o.config != "" {
		b, err := os.ReadFile(o.config)
		if err != nil {
			return fail("ci evaluate: %v", err)
		}
		doc = b
	}
	if o.image != "" || o.endpoint != "" {
		patched, err := patchAgentYAML(doc, o.image, o.endpoint)
		if err != nil {
			return fail("ci evaluate: %v", err)
		}
		doc = patched
	}
	prCtx := prInfo(pr)
	note := "CI"
	if prCtx.Number != "" {
		note = "pull request #" + prCtx.Number
	}
	if prCtx.SHA != "" {
		note += " at " + shortSHA(prCtx.SHA)
	}
	var made ciVersionCreated
	if err := c.do(ctx, http.MethodPost, agentPath+"/versions", map[string]string{"yaml": string(doc), "note": note}, &made); err != nil {
		return fail("ci evaluate: register the pull request's config: %v", err)
	}
	logf("registered %s as version %d of %s (created: %v); live version %d", srcName(o), made.Version, o.agent, made.Created, baseline)

	gateOpts := ci.GateOptions{FailOn: o.failOn, MaxScoreDrop: o.maxDrop, MaxCostIncrease: o.maxCost}
	if made.Version == baseline || made.Hash == live.Hash {
		g := ci.GateResult{Outcome: ci.OutcomeNoChange, Pass: true}
		body := ci.NoChangeComment(o.agent, baseline, prCtx.SHA)
		writeCIOutputs(o, nil, g, "", made.Version, baseline, "", body, logf)
		postPR(ctx, pr, ci.EvaluationMarker(o.agent), body, "success", "No agent change: this config is the live version", "", logf)
		logf("no agent change: the config is the live version %d; nothing to evaluate", baseline)
		return exitOK
	}

	modelA := armModel(o.model, []byte(live.YAML))
	modelB := armModel(o.model, doc)
	if modelA == "" || modelB == "" {
		return fail("ci evaluate: a version has no model; set -model")
	}
	name := fmt.Sprintf("%s v%d vs v%d", o.agent, made.Version, baseline)
	if prCtx.Number != "" {
		name = fmt.Sprintf("PR #%s: %s", prCtx.Number, name)
	}
	spec := schemas.EvaluationSpec{
		Name: name, Mode: schemas.EvaluationModeVersions,
		Arms: []schemas.EvaluationArm{
			{Label: ci.ArmLive, Agent: o.agent, Version: baseline, Model: modelA},
			{Label: ci.ArmPR, Agent: o.agent, Version: made.Version, Model: modelB},
		},
		Benchmarks: o.benchmarks, Pack: o.pack, Faults: o.faults,
		Trials: o.trials, TasksPerBenchmark: o.tasks, BudgetUSD: o.budget,
	}
	var ev schemas.Evaluation
	if err := c.do(ctx, http.MethodPost, "/evaluations", spec, &ev); err != nil {
		return fail("ci evaluate: launch the evaluation: %v", err)
	}
	reportURL := o.url + "/evaluations/" + url.PathEscape(ev.ID)
	logf("evaluation %s launched: %s", ev.ID, reportURL)
	if pr != nil {
		if err := pr.Status(ctx, "pending", fmt.Sprintf("Evaluating v%d against live v%d", made.Version, baseline), reportURL); err != nil {
			logf("warning: commit status: %v", err)
		}
	}

	final, err := waitEvaluation(ctx, c, ev.ID, o.timeout, o.poll, logf)
	if err != nil {
		// Stop spending on an evaluation nobody will read: a timeout, or the
		// CI job cancelled by a newer push.
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = c.do(cctx, http.MethodPost, "/evaluations/"+url.PathEscape(ev.ID)+"/cancel", nil, nil)
		cancel()
		g := ci.GateResult{Outcome: ci.OutcomeFailed, Pass: false, ExitCode: exitInfra, Reasons: []string{err.Error()}}
		body := ci.EvaluationComment(ci.CommentInput{Agent: o.agent, LiveVersion: baseline, PRVersion: made.Version, Gate: g,
			ReportURL: reportURL, Commit: prCtx.SHA, Note: "The evaluation was cancelled: " + err.Error() + "."})
		writeCIOutputs(o, nil, g, ev.ID, made.Version, baseline, reportURL, body, logf)
		postPR(context.Background(), pr, ci.EvaluationMarker(o.agent), body, "error", "Evaluation did not finish: "+err.Error(), reportURL, logf)
		return fail("ci evaluate: evaluation %s: %v", ev.ID, err)
	}
	var rep reportcard.Report
	if err := c.do(ctx, http.MethodGet, "/evaluations/"+url.PathEscape(ev.ID)+"/report", nil, &rep); err != nil {
		return fail("ci evaluate: fetch the report card of %s: %v", ev.ID, err)
	}
	g := ci.EvaluateGate(final.Status, &rep, gateOpts)
	body := ci.EvaluationComment(ci.CommentInput{Agent: o.agent, LiveVersion: baseline, PRVersion: made.Version, Status: final.Status,
		Report: &rep, Gate: g, ReportURL: reportURL, Commit: prCtx.SHA})
	writeCIOutputs(o, &rep, g, ev.ID, made.Version, baseline, reportURL, body, logf)
	state := "success"
	if !g.Pass {
		state = "failure"
	}
	postPR(ctx, pr, ci.EvaluationMarker(o.agent), body, state, ci.StatusDescription(g), reportURL, logf)
	logf("gate %s: %s (%s)", map[bool]string{true: "passed", false: "failed"}[g.Pass], g.Outcome, strings.Join(g.Reasons, "; "))
	return g.ExitCode
}

func srcName(o *ciEvalOptions) string {
	switch {
	case o.config != "":
		return o.config
	case o.image != "":
		return "the live config with image " + o.image
	}
	return "the live config with endpoint " + o.endpoint
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// waitEvaluation polls until the evaluation is done, failed or cancelled.
func waitEvaluation(ctx context.Context, c *heimdallClient, id string, timeout, poll time.Duration, logf func(string, ...any)) (*schemas.Evaluation, error) {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		var ev schemas.Evaluation
		if err := c.do(ctx, http.MethodGet, "/evaluations/"+url.PathEscape(id), nil, &ev); err != nil {
			if ctx.Err() != nil {
				return nil, errors.New("the CI job was cancelled")
			}
			return nil, fmt.Errorf("read status: %w", err)
		}
		done := 0
		for _, ch := range ev.Children {
			if ch.Status == "done" || ch.Status == "failed" || ch.Status == "cancelled" || ch.Status == schemas.EvaluationChildSkipped {
				done++
			}
		}
		if line := fmt.Sprintf("%s: %d of %d benchmarks finished", ev.Status, done, len(ev.Children)); line != last {
			logf("%s", line)
			last = line
		}
		switch ev.Status {
		case schemas.EvaluationDone, schemas.EvaluationFailed, schemas.EvaluationCancelled:
			return &ev, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("still %s after %s", ev.Status, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("the CI job was cancelled")
		case <-time.After(poll):
		}
	}
}

// armModel is the model an arm runs: the -model override, else the
// version's own model field. A "<provider>/<model>" name is split by the
// server.
func armModel(override string, doc []byte) string {
	if override != "" {
		return override
	}
	var cfg struct {
		Model string `yaml:"model"`
	}
	_ = yaml.Unmarshal(doc, &cfg)
	return strings.TrimSpace(cfg.Model)
}

// patchAgentYAML returns doc with the image (and image_digest when the ref
// pins one) or the remote endpoint replaced.
func patchAgentYAML(doc []byte, image, endpoint string) ([]byte, error) {
	var tree map[string]any
	if err := yaml.Unmarshal(doc, &tree); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	if image != "" {
		if strings.ContainsAny(image, " \n\t") {
			return nil, fmt.Errorf("-image %q is not an image reference", image)
		}
		tree["image"] = image
		if _, digest, ok := strings.Cut(image, "@"); ok && strings.HasPrefix(digest, "sha256:") {
			tree["image_digest"] = digest
		} else {
			delete(tree, "image_digest")
		}
		delete(tree, "build") // the pull request already built the image
	}
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("-endpoint %q: want an http(s) URL", endpoint)
		}
		remote, _ := tree["remote"].(map[string]any)
		if remote == nil {
			return nil, errors.New("-endpoint needs a remote agent (adapter remote_http with a remote: block); this agent has none")
		}
		remote["url"] = endpoint
	}
	return yaml.Marshal(tree)
}

// writeCIOutputs writes the files under -out, the step summary and the
// GitHub step outputs.
func writeCIOutputs(o *ciEvalOptions, rep *reportcard.Report, g ci.GateResult, evID string, version, live int, reportURL, comment string, logf func(string, ...any)) {
	if o.out != "" {
		if err := os.MkdirAll(o.out, 0o755); err != nil {
			logf("warning: %v", err)
		} else {
			if rep != nil {
				writeJSONFile(filepath.Join(o.out, "report.json"), rep, logf)
			}
			writeJSONFile(filepath.Join(o.out, "gate.json"), map[string]any{
				"gate": g, "evaluation": evID, "agent": o.agent, "version": version, "live_version": live, "report_url": reportURL,
			}, logf)
			if err := os.WriteFile(filepath.Join(o.out, "comment.md"), []byte(comment), 0o644); err != nil {
				logf("warning: %v", err)
			}
		}
	}
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		appendFile(p, comment+"\n", logf)
	}
	if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
		var b strings.Builder
		for _, kv := range [][2]string{
			{"outcome", g.Outcome}, {"pass", strconv.FormatBool(g.Pass)}, {"exit-code", strconv.Itoa(g.ExitCode)},
			{"evaluation", evID}, {"report-url", reportURL}, {"version", strconv.Itoa(version)}, {"live-version", strconv.Itoa(live)},
		} {
			fmt.Fprintf(&b, "%s=%s\n", kv[0], strings.ReplaceAll(kv[1], "\n", " "))
		}
		appendFile(p, b.String(), logf)
	}
}

func writeJSONFile(path string, v any, logf func(string, ...any)) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err == nil {
		err = os.WriteFile(path, append(b, '\n'), 0o644)
	}
	if err != nil {
		logf("warning: %s: %v", path, err)
	}
}

func appendFile(path, s string, logf func(string, ...any)) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logf("warning: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		logf("warning: %v", err)
	}
}

func postPR(ctx context.Context, pr prReporter, marker, body, state, desc, link string, logf func(string, ...any)) {
	if pr == nil {
		return
	}
	if err := pr.Comment(ctx, marker, body); err != nil {
		logf("warning: pull request comment: %v", err)
	}
	if err := pr.Status(ctx, state, desc, link); err != nil {
		logf("warning: commit status: %v", err)
	}
}
