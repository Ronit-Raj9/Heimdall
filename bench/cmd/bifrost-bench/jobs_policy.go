package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/maximhq/bifrost/bench/schemas"
)

// policy.generate (lane Z23): a pinned model drafts three support
// scenarios per clause of an uploaded policy (normal, edge, a customer who
// pushes to break the rule) through the gateway, within a hard budget the
// caller confirms. Drafts wait in the document's staging folder, pending
// review; they are tasks only once accepted (POST
// /suites/{id}/policy-docs/{doc}/review), and readiness then proves each
// compliant reference passes and each violating control fails.
//
// Hosted: the org's own provider key from the key store, bound to a
// per-org experiment (as calibrate.run), the budget capped at
// hostedPolicyMaxUSD and the key's month budget, the spend added to the
// key. Local: the gateway with the operator's environment keys.
func init() {
	registerJobKind(jobKindSpec{Kind: kindPolicyGenerate, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithLimit(4, 1)},
		Validate: validatePolicyGenerate, Run: runPolicyGenerate})
}

const kindPolicyGenerate = "policy.generate"

// Vars so tests can lower them.
var (
	hostedPolicyMaxUSD  = 5.0
	defaultPolicyBudget = 1.0
	policyMaxClauses    = 60
)

type policyParams struct {
	Suite     string   `json:"suite"`
	Doc       string   `json:"doc"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	KeyID     string   `json:"key_id,omitempty"`
	BudgetUSD float64  `json:"budget_usd,omitempty"`
	Clauses   []string `json:"clauses,omitempty"`
	Redraft   bool     `json:"redraft,omitempty"`
	HeldOut   bool     `json:"held_out,omitempty"`
	ShapeTask string   `json:"shape_task,omitempty"`
	Estimate  bool     `json:"estimate,omitempty"`
	Yes       bool     `json:"yes,omitempty"`
}

type policyJobResult struct {
	*policybench.GenerateResult
	Estimate    bool    `json:"estimate"`
	ForecastUSD float64 `json:"forecast_usd"`
	KeyID       string  `json:"key_id,omitempty"`
}

func parsePolicyParams(raw json.RawMessage) (policyParams, error) {
	var p policyParams
	if err := decodeParams(raw, &p); err != nil {
		return p, err
	}
	if err := checkID("suite", p.Suite, true); err != nil {
		return p, err
	}
	if !policybench.ValidDocID(p.Doc) {
		return p, fmt.Errorf("doc %q is not a policy document id", p.Doc)
	}
	for f, v := range map[string]string{"provider": p.Provider, "model": p.Model} {
		if v == "" {
			return p, fmt.Errorf("%s is required: generation uses one pinned model", f)
		}
		if !calibrateNamePattern.MatchString(v) || len(v) > 128 {
			return p, fmt.Errorf("%s %q is not a valid name", f, v)
		}
	}
	if err := checkID("key_id", p.KeyID, false); err != nil {
		return p, err
	}
	if err := checkID("shape_task", p.ShapeTask, false); err != nil {
		return p, err
	}
	if p.BudgetUSD < 0 || math.IsNaN(p.BudgetUSD) || math.IsInf(p.BudgetUSD, 0) {
		return p, errors.New("budget_usd must be 0 (the default) or a positive amount")
	}
	if len(p.Clauses) > policyMaxClauses {
		return p, fmt.Errorf("at most %d clauses per job", policyMaxClauses)
	}
	for _, c := range p.Clauses {
		if c == "" || len(c) > 20 {
			return p, fmt.Errorf("clause %q is not a clause number", c)
		}
	}
	if !p.Yes && !p.Estimate {
		return p, errors.New("generation calls a model and costs money: set yes to true to confirm (or estimate to true for the forecast only)")
	}
	return p, nil
}

func validatePolicyGenerate(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	p, err := parsePolicyParams(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	if err := orgModelKeyAtCreate(ctx, env, p.Provider, p.KeyID); err != nil {
		return nil, "", err
	}
	return p, p.Suite, nil
}

func policyBudget(want float64, hosted bool) float64 {
	b := want
	if b <= 0 {
		b = defaultPolicyBudget
	}
	if hosted && b > hostedPolicyMaxUSD {
		b = hostedPolicyMaxUSD
	}
	return b
}

// policySuiteRoot is the benchmark folder; a var for tests.
var policySuiteRoot = func(env jobEnv, id string) (string, error) {
	if env.App == nil {
		return "", errors.New("no project config on this server")
	}
	if b, ok := env.Services.Suites.(interface{ SuiteBuiltin(string) bool }); ok && b.SuiteBuiltin(id) {
		return "", fmt.Errorf("benchmark %s is in the built-in library, which is read-only; copy it into your project to add policy tasks", id)
	}
	return (judgeSvc{a: env.App}).suiteRoot(id)
}

// policyGatewayFor is calibrateGatewayFor; a var for tests.
var policyGatewayFor = func(ctx context.Context, env jobEnv) (schemas.Gateway, string, func(), error) {
	return calibrateGatewayFor(ctx, env)
}

// policyPrices is the price lookup; a var for tests.
var policyPrices = func(env jobEnv) func(string, schemas.Tokens) (float64, bool) {
	if env.App == nil || env.App.prices == nil {
		return nil
	}
	return graders.PricesFrom(env.App.prices.Lookup)
}

func runPolicyGenerate(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	p, err := parsePolicyParams(j.Params)
	if err != nil {
		return nil, err
	}
	root, err := policySuiteRoot(env, p.Suite)
	if err != nil {
		return nil, err
	}
	doc, err := policybench.Load(root, p.Doc)
	if err != nil {
		return nil, fmt.Errorf("policy document %s: %w", p.Doc, err)
	}
	shape, taken, err := policybench.SuiteShape(root, p.ShapeTask)
	if err != nil {
		return nil, err
	}
	tools, err := policybench.Tools(root, shape.Desk)
	if err != nil {
		return nil, err
	}
	price := policyPrices(env)
	if price == nil {
		return nil, errors.New("no price snapshot on this server, so the generation budget can not be enforced")
	}
	clauses, skipped, err := policybench.Selected(doc, p.Clauses, p.Redraft)
	if err != nil {
		return nil, err
	}
	if len(clauses) > policyMaxClauses {
		clauses = clauses[:policyMaxClauses]
	}
	budget := policyBudget(p.BudgetUSD, env.Hosted)
	psh, where, err := policybench.PolicyShape(shape, doc)
	if err != nil {
		return nil, err
	}
	forecast, priced := policybench.Forecast(doc, clauses, tools, psh.Seed, price, p.Model, 0)
	if !priced {
		return nil, fmt.Errorf("model %s is not in the price snapshot, so the budget can not be enforced; pick a priced model", p.Model)
	}
	res := &policyJobResult{GenerateResult: &policybench.GenerateResult{Doc: doc.ID, Model: p.Provider + "/" + p.Model, BudgetUSD: budget,
		Clauses: len(clauses), Skipped: skipped, PolicyIn: where, Drafted: []policybench.Draft{}}, Estimate: p.Estimate, ForecastUSD: forecast}
	if res.Skipped == nil {
		res.Skipped = []policybench.Skip{}
	}
	if p.Estimate {
		res.Next = fmt.Sprintf("%d clause(s), one call each (up to %d with retries), at most $%.4f for one attempt each; the budget stops the job at $%.2f. Confirm to start.",
			len(clauses), len(clauses)*(policybench.Retries+1), forecast, budget)
		return res, nil
	}
	if len(clauses) == 0 {
		res.Next = "Every clause is drafted, skipped or listed as already drafted; nothing to do."
		return res, nil
	}
	var orgKey *keystore.Key
	expID := policybench.AgentID
	gw, url, closeGW, err := policyGatewayFor(ctx, env)
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
		if k.BudgetUSD > 0 && k.MonthSpentUSD+budget > k.BudgetUSD {
			budget = math.Max(0, k.BudgetUSD-k.MonthSpentUSD)
			if budget <= 0 {
				return nil, fmt.Errorf("key %s spent its month budget ($%.2f): raise it on /models", k.ID, k.BudgetUSD)
			}
			res.BudgetUSD = budget
		}
		value, err := env.Services.Keys.Reveal(ctx, env.OrgID, k.ID)
		if err != nil || value == "" {
			return nil, fmt.Errorf("read key %s: it can not be decrypted; replace it on /models", k.ID)
		}
		cred := schemas.ProviderCredential{KeyID: k.ID, Provider: k.Provider, BaseURL: k.BaseURL, Models: k.Models, Value: value}
		if err := cg.ApplyOrgCredentials(ctx, env.OrgID, []schemas.ProviderCredential{cred}); err != nil {
			return nil, fmt.Errorf("register the org key with the gateway: %w", err)
		}
		expID = "policy-" + calibrateOrgSafe(env.OrgID)
		cg.BindExperimentOrg(expID, env.OrgID)
	}
	var logger schemas.Logger = schemas.NoopLogger{}
	if env.App != nil && env.App.logger != nil {
		logger = env.App.logger
	}
	g, err := policybench.NewGenerator(policybench.GenConfig{Gateway: gw, URL: url, HTTP: &http.Client{Timeout: 3 * time.Minute},
		Provider: p.Provider, Model: p.Model, Price: price, BudgetUSD: budget, ExpID: expID, Logger: logger})
	if err != nil {
		return nil, err
	}
	defer g.Close(ctx)
	want := p.Clauses
	if len(want) == 0 {
		for _, c := range clauses {
			want = append(want, c.Number)
		}
	}
	gen, genErr := policybench.Generate(ctx, g, root, doc, shape, tools, policybench.GenerateOptions{
		Clauses: want, Redraft: p.Redraft, HeldOut: p.HeldOut, By: j.CreatedBy,
		Taken: func(id string) bool { return taken[id] },
		Logf:  log.Printf,
		Progress: func(done, total int, spent float64) {
			if progress != nil {
				progress(map[string]any{"done": done, "total": total, "spent_usd": spent})
			}
		},
	})
	spent, _ := g.Spent()
	if orgKey != nil && spent > 0 {
		if err := env.Services.Keys.AddSpend(ctx, env.OrgID, orgKey.ID, spent); err != nil {
			log.Warnf("add $%.4f to key %s's month spend: %v", spent, orgKey.ID, err)
		}
	}
	if gen != nil {
		gen.Skipped = append(skipped, gen.Skipped...)
		res.GenerateResult = gen
		res.BudgetUSD = budget
	}
	if genErr != nil {
		return res, genErr
	}
	return res, nil
}
