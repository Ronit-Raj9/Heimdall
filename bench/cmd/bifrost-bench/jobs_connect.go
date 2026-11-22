package main

// agent.verify (lane Z01, Verify v2): the Connect box's "prove it" job. It
// runs the connect rows (reach, sign-in, request, answer with a nonce,
// latency, conversation memory, session isolation, version, tools, model
// calls) against the saved agent with the org's secrets, streaming each row
// as job progress, then runs one real library task through the real
// launcher on the org's worker (a bridge experiment like agent.check:
// uncounted, graded and sealed). Its rows replace the READY ping of
// "Test my endpoint" for every remote adapter.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/connect"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/webhooks"
)

const kindAgentVerify = "agent.verify"

// agentVerifyParams are the job's params.
type agentVerifyParams struct {
	Agent string `json:"agent"`           // agents/<id>.yaml
	Suite string `json:"suite,omitempty"` // the real task's benchmark; empty skips the real task
	Task  string `json:"task,omitempty"`  // empty: the benchmark's first task
	// SkipRealTask runs the connect rows only.
	SkipRealTask bool `json:"skip_real_task,omitempty"`
	TimeoutMin   int  `json:"timeout_min,omitempty"`
}

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindAgentVerify, Local: true, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(2, 1)},
		Validate: validateAgentVerify, Run: runAgentVerify,
	})
}

func validateAgentVerify(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p agentVerifyParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if err := validAgentFile(env.ProjectRoot, "agent", p.Agent); err != nil {
		return nil, "", invalid(err)
	}
	if p.Suite != "" {
		if err := validSuiteRef(p.Suite); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.Task != "" {
		if err := ops.ValidID("task", p.Task); err != nil {
			return nil, "", invalid(err)
		}
	}
	if _, err := bridgeTimeout(p.TimeoutMin); err != nil {
		return nil, "", invalid(err)
	}
	return p, p.Agent, nil
}

// verifySecrets resolves every secret the config names from the org's
// secrets; a local server falls back to its own environment, as runs do.
// Missing ones are left out: the row that needs one says so.
func verifySecrets(ctx context.Context, env jobEnv, cfg *schemas.AgentConfig) map[string]string {
	var names []string
	if cfg.Remote != nil {
		names = append(names, cfg.Remote.SecretNames()...)
		names = append(names, cfg.Remote.AuthEnv, cfg.Remote.SignEnv)
	}
	if cfg.OpenAI != nil {
		names = append(names, cfg.OpenAI.APIKeySecret)
	}
	var want []string
	for _, n := range names {
		if n != "" {
			want = append(want, n)
		}
	}
	out := map[string]string{}
	if env.Services.Keys != nil {
		if got, err := toolproxy.OrgSecretEnv(ctx, env.Services.Keys, env.OrgID, want); err == nil {
			out = got
		}
	}
	for _, n := range want {
		if out[n] == "" && !env.Hosted {
			if v := os.Getenv(n); v != "" {
				out[n] = v
			}
		}
	}
	return out
}

// verifyClient refuses private addresses on hosted servers (checked again
// at dial time) and never follows redirects.
func verifyClient(hosted bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if hosted {
		tr.DialContext = webhooks.GuardedDialer(10 * time.Second).DialContext
	}
	return &http.Client{Timeout: 110 * time.Second, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func runAgentVerify(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p agentVerifyParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	path, err := ops.Confine(env.ProjectRoot, "agent", p.Agent)
	if err != nil {
		return nil, invalid(err)
	}
	var cfg schemas.AgentConfig
	if err := suite.DecodeYAMLFile(path, p.Agent, suite.SchemaAgent, &cfg); err != nil {
		return nil, fmt.Errorf("read %s: %w", p.Agent, err)
	}
	realTask := p.Suite != "" && !p.SkipRealTask
	target := "this server"
	if env.Hosted {
		target = "Heimdall's server"
	}
	emit := func(rows []connect.VerifyRow) {
		if progress != nil {
			progress(map[string]any{"rows": rows})
		}
	}
	res := connect.Verify(ctx, &cfg, connect.VerifyOptions{Client: verifyClient(env.Hosted), Hosted: env.Hosted, Target: target,
		Secrets: verifySecrets(ctx, env, &cfg), Emit: emit, RealTask: realTask})
	for _, r := range res.Rows {
		if r.Status != connect.StatusPending {
			log.Printf("%-10s %-5s %s", r.ID, r.Status, r.Detail)
		}
	}
	if realTask {
		row := res.Row(connect.RowRealTask)
		if !res.OK {
			row.Status, row.Detail = connect.StatusSkip, "skipped: fix the rows above first"
		} else {
			row.Status = connect.StatusRunning
			emit(res.Rows)
			verifyRealTask(ctx, env, &cfg, p, row, log)
			res.OK = res.OK && row.Status != connect.StatusFail
		}
		emit(res.Rows)
		log.Printf("%-10s %-5s %s", row.ID, row.Status, row.Detail)
	}
	if !res.OK {
		return res, errors.New("verification failed: " + firstFail(res))
	}
	return res, nil
}

func firstFail(res *connect.VerifyResult) string {
	for _, r := range res.Rows {
		if r.Status == connect.StatusFail {
			if r.Failure != nil {
				return r.Title + ": " + r.Failure.Message
			}
			return r.Title + ": " + r.Detail
		}
	}
	return "a row failed"
}

// verifyScripted reports whether the real task may answer model calls from
// the scripted model: only when they go through Heimdall's gateway. An
// endpoint that calls its own provider, an OpenAI-compatible endpoint and an
// A2A agent run on their own models.
func verifyScripted(cfg *schemas.AgentConfig) bool {
	switch {
	case utils.IsOpenAIAgent(cfg), utils.IsA2A(cfg):
		return false
	case cfg.Remote != nil:
		return cfg.Remote.ModelTraffic == "gateway"
	}
	return true
}

// verifyRealTask runs one task of p.Suite through the real launcher on
// the org's worker and fills row.
func verifyRealTask(ctx context.Context, env jobEnv, cfg *schemas.AgentConfig, p agentVerifyParams, row *connect.VerifyRow, log jobs.Logger) {
	start := time.Now()
	defer func() { row.MS = time.Since(start).Milliseconds() }()
	fail := func(status, detail string) {
		row.Status, row.Detail = status, detail
		if f, ok := connect.ExplainRunError(detail); ok {
			row.Failure = &f
		}
	}
	timeout, _ := bridgeTimeout(p.TimeoutMin)
	if p.TimeoutMin == 0 {
		timeout = 20 * time.Minute
	}
	l, err := bridgeLauncher(env)
	if err != nil {
		fail(connect.StatusSkip, err.Error())
		return
	}
	if err := requireRunner(l); err != nil {
		fail(connect.StatusSkip, err.Error())
		return
	}
	task := p.Task
	if task == "" && env.App != nil {
		s, err := loadSuiteForVerify(ctx, env, p.Suite)
		if err != nil {
			fail(connect.StatusFail, err.Error())
			return
		}
		task = s
	}
	exp := checkExperiment(p.Agent, p.Suite, task)
	exp.Name = strings.Replace(exp.Name, "check-", "verify-", 1)
	exp.ScriptedUpstream = verifyScripted(cfg)
	m, err := l.Plan(ctx, exp)
	if err != nil {
		fail(connect.StatusFail, "plan the task: "+err.Error())
		return
	}
	if err := runExperiment(ctx, l, m, timeout, log); err != nil {
		fail(connect.StatusFail, err.Error())
		return
	}
	runs, err := sealedRuns(ctx, env, m.ExpID)
	if err != nil || len(runs) == 0 {
		fail(connect.StatusFail, fmt.Sprintf("no finished run in %s; open the experiment for what stopped it", m.ExpID))
		return
	}
	r := runs[0]
	who := "the scripted model"
	if !exp.ScriptedUpstream {
		who = "the agent's own model"
	}
	switch schemas.Outcome(r.Outcome) {
	case schemas.OutcomePass:
		row.Status, row.Detail = connect.StatusPass, fmt.Sprintf("task %s of %s ran end to end on %s, graded pass and recorded (run %s)", r.Task, p.Suite, who, r.RunID)
	case schemas.OutcomeFail, schemas.OutcomeRefusal:
		row.Status, row.Detail = connect.StatusPass, fmt.Sprintf("task %s of %s ran end to end on %s and was graded %s (run %s): the connection works, the score is the agent's", r.Task, p.Suite, who, r.Outcome, r.RunID)
	case schemas.OutcomeTimeout, schemas.OutcomeBudget:
		fail(connect.StatusWarn, fmt.Sprintf("task %s did not finish inside its budget (%s, run %s)", r.Task, r.Outcome, r.RunID))
		f := connect.Lookup(connect.FailTimeout, map[string]string{"timeout": fmt.Sprint(int(timeout.Seconds()))})
		row.Failure = &f
	default:
		detail := r.Detail
		if detail == "" {
			detail = "the run ended " + r.Outcome
		}
		fail(connect.StatusFail, fmt.Sprintf("task %s: %s (run %s)", r.Task, detail, r.RunID))
	}
}

func loadSuiteForVerify(ctx context.Context, env jobEnv, ref string) (string, error) {
	s, err := engine.LoadSuite(ctx, env.App.reg, ref)
	if err != nil {
		return "", fmt.Errorf("benchmark %s: %w", ref, err)
	}
	if len(s.TaskIDs) == 0 {
		return "", fmt.Errorf("benchmark %s has no tasks", ref)
	}
	return s.TaskIDs[0], nil
}
