package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/authoring"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskgates"
)

// authoring.assist (lane Z19, INFRA_I3 section 4.4): the assist engine
// drafts rubric criteria, one wrong reply per criterion, near-miss wrong
// answers and a per-task exploit for the answer tasks of an org's
// benchmark, through the gateway on the org's own provider key, within a
// hard budget the caller confirms. Every drafted field is labelled on the
// task and the task goes pending review: it counts in no evaluation, gate,
// release or report card until a person accepts it on the Review tab, after
// the readiness check proves each wrong answer fails.
//
// Hosted: the org's key from the key store (registered as org slots, the
// assist key bound to a per-org experiment, as calibrate.run does), the
// budget capped at hostedAssistMaxUSD, the spend added to the key's month
// spend. Local: the gateway with the operator's environment keys.
func init() {
	registerJobKind(jobKindSpec{Kind: kindAuthoringAssist, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithLimit(4, 1)},
		Validate: validateAuthoringAssist, Run: runAuthoringAssist})
}

const kindAuthoringAssist = "authoring.assist"

// Vars so tests can lower them.
var (
	hostedAssistMaxUSD  = 5.0
	defaultAssistBudget = 1.0
	assistMaxTasks      = 200
)

// assistParams are authoring.assist's params.
type assistParams struct {
	Suite     string   `json:"suite"`
	Tasks     []string `json:"tasks,omitempty"` // empty: every answer task
	Ops       []string `json:"ops"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	KeyID     string   `json:"key_id,omitempty"`
	BudgetUSD float64  `json:"budget_usd,omitempty"`
	NearMiss  int      `json:"near_miss,omitempty"`
	Redraft   bool     `json:"redraft,omitempty"` // run an op again on tasks it already drafted
	Yes       bool     `json:"yes"`               // confirms the calls cost money
	Estimate  bool     `json:"estimate"`          // forecast only, no call
}

// assistResult is the job result the Review tab shows.
type assistResult struct {
	Suite       string                 `json:"suite"`
	Estimate    bool                   `json:"estimate"`
	Model       string                 `json:"model"`
	KeyID       string                 `json:"key_id,omitempty"`
	BudgetUSD   float64                `json:"budget_usd"`
	ForecastUSD float64                `json:"forecast_usd"`
	Calls       int                    `json:"calls"`
	SpentUSD    float64                `json:"spent_usd"`
	Tasks       []*authoring.TaskDraft `json:"tasks"`
	Skipped     []assistSkip           `json:"skipped,omitempty"`
	Pending     int                    `json:"pending"`
	Stopped     bool                   `json:"stopped,omitempty"`
	Next        string                 `json:"next"`
}

type assistSkip struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

func parseAssistParams(raw json.RawMessage) (assistParams, []authoring.Op, error) {
	var p assistParams
	if err := decodeParams(raw, &p); err != nil {
		return p, nil, err
	}
	if err := checkID("suite", p.Suite, true); err != nil {
		return p, nil, err
	}
	if err := checkIDList("tasks", p.Tasks); err != nil {
		return p, nil, err
	}
	if !p.Yes && !p.Estimate {
		return p, nil, errors.New("the assist calls a model and costs money: set yes to true to confirm (or estimate to true for the forecast only)")
	}
	if p.Provider == "" || p.Model == "" {
		return p, nil, errors.New("provider and model are required: the assist uses one pinned model")
	}
	for f, v := range map[string]string{"provider": p.Provider, "model": p.Model} {
		if !calibrateNamePattern.MatchString(v) || strings.Contains(v, "..") {
			return p, nil, fmt.Errorf("%s %q is not a valid name", f, v)
		}
	}
	if err := checkID("key_id", p.KeyID, false); err != nil {
		return p, nil, err
	}
	if p.BudgetUSD < 0 || math.IsNaN(p.BudgetUSD) || math.IsInf(p.BudgetUSD, 0) {
		return p, nil, errors.New("budget_usd must be 0 (the default) or a positive amount")
	}
	if p.NearMiss < 0 || p.NearMiss > 5 {
		return p, nil, errors.New("near_miss must be 0 (default 3) to 5")
	}
	if len(p.Ops) == 0 {
		return p, nil, errors.New("ops is required: rubric, criterion_wrong, near_miss_wrong, exploit")
	}
	var ops []authoring.Op
	seen := map[authoring.Op]bool{}
	for _, s := range p.Ops {
		op, err := authoring.ParseOp(s)
		if err != nil {
			return p, nil, err
		}
		if !seen[op] {
			seen[op] = true
			ops = append(ops, op)
		}
	}
	return p, ops, nil
}

func validateAuthoringAssist(_ context.Context, _ jobEnv, raw json.RawMessage) (any, string, error) {
	p, _, err := parseAssistParams(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	return p, p.Suite, nil
}

// assistBudget is the cap a run gets: the asked budget, else the default,
// never above the hosted cap on a hosted server.
func assistBudget(asked float64, hosted bool) float64 {
	b := asked
	if b == 0 {
		b = defaultAssistBudget
	}
	if hosted && b > hostedAssistMaxUSD {
		b = hostedAssistMaxUSD
	}
	return b
}

// assistTasks picks the tasks to draft: the named ones, else every answer
// task; generated tasks a person rejected are left alone.
func assistTasks(s *schemas.Suite, ids []string) ([]*schemas.Task, []assistSkip, error) {
	var out []*schemas.Task
	var skipped []assistSkip
	if len(ids) > 0 {
		for _, id := range ids {
			t := s.TaskByID(id)
			if t == nil {
				return nil, nil, fmt.Errorf("task %s is not in benchmark %s", id, s.ID)
			}
			out = append(out, t)
		}
	} else {
		out = append(out, s.Tasks...)
	}
	if len(out) > assistMaxTasks {
		return nil, nil, fmt.Errorf("%d tasks: the assist drafts at most %d per job; name the tasks", len(out), assistMaxTasks)
	}
	return out, skipped, nil
}

// assistForecast is the worst-case cost of the plan: per task and op, the
// prompt (the instruction and answer, about 3 characters a token, plus the
// prompt template) and a full reply.
func assistForecast(s *schemas.Suite, tasks []*schemas.Task, ops []authoring.Op, price func(string, schemas.Tokens) (float64, bool), model string) (float64, int, bool) {
	usd, calls := 0.0, 0
	for _, t := range tasks {
		it := taskgates.ItemOf(s, t)
		in := int64(len(it.Instruction)+len(it.Expected))/3 + 900
		for range ops {
			calls++
			if price == nil {
				return 0, calls, false
			}
			c, ok := price(model, schemas.Tokens{Fresh: in, Out: 1500})
			if !ok {
				return 0, calls, false
			}
			usd += c
		}
	}
	return usd, calls, true
}

// assistGatewayFor is calibrateGatewayFor; a var so tests swap in a fake.
var assistGatewayFor = func(ctx context.Context, env jobEnv) (schemas.Gateway, string, func(), error) {
	return calibrateGatewayFor(ctx, env)
}

// assistLoadSuite loads the benchmark; a var for tests.
var assistLoadSuite = func(ctx context.Context, a *app, id string) (*schemas.Suite, error) {
	return (judgeSvc{a: a}).load(ctx, id)
}

func runAuthoringAssist(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	p, ops, err := parseAssistParams(j.Params)
	if err != nil {
		return nil, err
	}
	if env.App == nil {
		return nil, errors.New("no project config on this server")
	}
	a := env.App
	if b, ok := env.Services.Suites.(interface{ SuiteBuiltin(string) bool }); ok && b.SuiteBuiltin(p.Suite) {
		return nil, fmt.Errorf("benchmark %s is in the built-in library, which is read-only; copy it into your project to draft on it", p.Suite)
	}
	s, err := assistLoadSuite(ctx, a, p.Suite)
	if err != nil {
		return nil, err
	}
	tasks, skipped, err := assistTasks(s, p.Tasks)
	if err != nil {
		return nil, err
	}
	var price func(string, schemas.Tokens) (float64, bool)
	if a.prices != nil {
		price = graders.PricesFrom(a.prices.Lookup)
	}
	res := &assistResult{Suite: s.ID, Estimate: p.Estimate, Model: p.Provider + "/" + p.Model, BudgetUSD: assistBudget(p.BudgetUSD, env.Hosted), Skipped: skipped}
	forecast, calls, priced := assistForecast(s, tasks, ops, price, p.Model)
	res.ForecastUSD, res.Calls = forecast, calls
	if !priced {
		return nil, fmt.Errorf("model %s is not in the price snapshot, so the assist budget can not be enforced; pick a priced model", p.Model)
	}
	if p.Estimate {
		res.Next = fmt.Sprintf("Up to %d calls, at most $%.4f (the budget stops the job at $%.2f). Confirm to start.", calls, forecast, res.BudgetUSD)
		return res, nil
	}
	var orgKey *keystore.Key
	expID := authoring.AgentID
	gw, url, closeGW, err := assistGatewayFor(ctx, env)
	if err != nil {
		return nil, err
	}
	if closeGW != nil {
		defer closeGW()
	}
	if env.Hosted {
		cg, ok := gw.(calibrateGateway)
		if !ok {
			return nil, errors.New("this server's gateway can not route calls to the org's own key")
		}
		k, err := calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, p.Provider, p.KeyID)
		if err != nil {
			return nil, err
		}
		orgKey, res.KeyID = k, k.ID
		if k.BudgetUSD > 0 && k.MonthSpentUSD+res.BudgetUSD > k.BudgetUSD {
			res.BudgetUSD = math.Max(0, k.BudgetUSD-k.MonthSpentUSD)
			if res.BudgetUSD <= 0 {
				return nil, fmt.Errorf("key %s spent its month budget ($%.2f): raise it on /models", k.ID, k.BudgetUSD)
			}
		}
		value, err := env.Services.Keys.Reveal(ctx, env.OrgID, k.ID)
		if err != nil || value == "" {
			return nil, fmt.Errorf("read key %s: it can not be decrypted; replace it on /models", k.ID)
		}
		cred := schemas.ProviderCredential{KeyID: k.ID, Provider: k.Provider, BaseURL: k.BaseURL, Models: k.Models, Value: value}
		if err := cg.ApplyOrgCredentials(ctx, env.OrgID, []schemas.ProviderCredential{cred}); err != nil {
			return nil, fmt.Errorf("register the org key with the gateway: %w", err)
		}
		expID = "authoring-" + calibrateOrgSafe(env.OrgID)
		cg.BindExperimentOrg(expID, env.OrgID)
	}
	asst, err := authoring.New(authoring.Config{Gateway: gw, URL: url, HTTP: &http.Client{Timeout: 2 * time.Minute},
		Provider: p.Provider, Model: p.Model, Price: price, BudgetUSD: res.BudgetUSD, ExpID: expID, Logger: a.logger})
	if err != nil {
		return nil, err
	}
	defer asst.Close(ctx)
	actor := j.CreatedBy
	defer func() {
		spent, _ := asst.Spent()
		res.SpentUSD = spent
		if orgKey != nil && spent > 0 {
			if err := env.Services.Keys.AddSpend(ctx, env.OrgID, orgKey.ID, spent); err != nil {
				log.Warnf("add $%.4f to key %s's month spend: %v", spent, orgKey.ID, err)
			}
		}
	}()
	for i, t := range tasks {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		d, err := authoring.DraftTask(ctx, asst, s, t, authoring.TaskOptions{Ops: ops, NearMiss: p.NearMiss, Redraft: p.Redraft && !j.Resume()})
		if err != nil {
			res.Skipped = append(res.Skipped, assistSkip{Task: t.ID, Reason: err.Error()})
			continue
		}
		res.Tasks = append(res.Tasks, d)
		edits, err := authoring.Edits(s, t, d)
		if err != nil {
			return res, fmt.Errorf("task %s: %w", t.ID, err)
		}
		if len(edits) > 0 {
			if err := writeSuiteEdits(ctx, env, s.Root, edits, actor, "assist: "+strings.Join(p.Ops, ", ")); err != nil {
				return res, fmt.Errorf("task %s: %w", t.ID, err)
			}
			res.Pending++
		}
		spent, _ := asst.Spent()
		log.Printf("task %s: %d field(s) drafted for $%.4f ($%.4f of $%.2f spent so far)", t.ID, len(d.Generated), d.CostUSD, spent, res.BudgetUSD)
		if progress != nil {
			progress(map[string]any{"done": i + 1, "total": len(tasks), "spent_usd": spent})
		}
		if d.Stopped {
			res.Stopped = true
			break
		}
	}
	switch {
	case res.Pending == 0:
		res.Next = "Nothing was drafted; see each task's reasons."
	case res.Stopped:
		res.Next = fmt.Sprintf("The budget ran out after %d task(s). Lock the benchmark and run the readiness check, then accept or reject each drafted task on the Review tab.", res.Pending)
	default:
		res.Next = fmt.Sprintf("%d task(s) drafted and pending review. Lock the benchmark and run the readiness check, then accept or reject each one on the Review tab.", res.Pending)
	}
	return res, nil
}

// projectWriter adapts the org project store to taskgates.ProjectWriter.
type projectWriter struct{ ps projectfs.Store }

func (w projectWriter) Write(ctx context.Context, rel string, data []byte, actor, note, ifMatch string) error {
	_, err := w.ps.Write(ctx, projectfs.AreaBenchmarks, rel, data, projectfs.WriteOpts{Actor: actor, Note: note, IfMatch: ifMatch})
	return err
}

// writeSuiteEdits writes edits under a suite root through the org's
// project store (versioned, guarded) when the suite is in its benchmarks
// area, else on disk.
func writeSuiteEdits(ctx context.Context, env jobEnv, root string, edits []taskgates.FileEdit, actor, note string) error {
	if ps := env.Services.Projects; ps != nil {
		if base, err := ps.Resolve(projectfs.AreaBenchmarks, "."); err == nil {
			if rel, err := filepath.Rel(base, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return taskgates.WriteEditsVia(ctx, projectWriter{ps}, filepath.ToSlash(rel), edits, actor, note)
			}
		}
	}
	return taskgates.WriteEdits(root, edits)
}
