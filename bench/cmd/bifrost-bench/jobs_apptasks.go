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

	"github.com/maximhq/bifrost/bench/apptasks"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// apptasks.generate (Wave B Z18): the model steps of turning an OpenAPI
// file and a policy into tasks (apptasks package). stage rules drafts the
// numbered policy rules a person then approves; stage tasks drafts seed
// data and scenarios rule by rule and proves each one against the app
// copy's rules. Both run on a pinned model through the server's gateway on
// a virtual key minted for the job: hosted with the org's own model key
// (never the server's), under a hard cap of calls and USD that the
// assistant checks before every call; the spend is added to the key's
// month spend. The caller must confirm the cost (yes: true).
func init() {
	registerJobKind(jobKindSpec{Kind: kindAppTasksGenerate, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithLimit(4, 1)},
		Validate: validateAppTasksGenerate, Run: runAppTasksGenerate})
}

const kindAppTasksGenerate = "apptasks.generate"

// Caps: hosted jobs never pass them; max_usd may only lower the USD cap.
// Vars so tests can lower them.
var (
	hostedAppTasksMaxCalls = 200
	hostedAppTasksMaxUSD   = 5.0
	localAppTasksMaxCalls  = 400
)

type appTasksParams struct {
	Draft    string  `json:"draft"`
	Stage    string  `json:"stage"` // rules | tasks
	Yes      bool    `json:"yes"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	KeyID    string  `json:"key_id,omitempty"`
	MaxUSD   float64 `json:"max_usd,omitempty"`
	PerRule  int     `json:"per_rule,omitempty"`
	Judged   bool    `json:"judged,omitempty"` // keep reply rubrics (the benchmark will pin a judge)
}

type appTasksResult struct {
	Draft    string                `json:"draft"`
	Stage    string                `json:"stage"`
	Status   string                `json:"status"`
	Model    string                `json:"model"`
	KeyID    string                `json:"key_id,omitempty"`
	Rules    int                   `json:"rules"`
	Tasks    int                   `json:"tasks"`
	Dropped  int                   `json:"dropped"`
	Calls    int                   `json:"calls"`
	SpentUSD float64               `json:"spent_usd"`
	CapCalls int                   `json:"cap_calls"`
	CapUSD   float64               `json:"cap_usd,omitempty"`
	Dropouts []apptasks.Dropped    `json:"dropped_tasks,omitempty"`
	Records  []apptasks.CallRecord `json:"call_records,omitempty"`
}

func parseAppTasksParams(raw json.RawMessage) (appTasksParams, error) {
	var p appTasksParams
	if err := decodeParams(raw, &p); err != nil {
		return p, err
	}
	if !apptasks.ValidDraftID(p.Draft) {
		return p, fmt.Errorf("draft %q is not a draft id (ag_ and 12 hex digits)", p.Draft)
	}
	if p.Stage != "rules" && p.Stage != "tasks" {
		return p, errors.New(`stage must be "rules" or "tasks"`)
	}
	if !p.Yes {
		return p, errors.New("drafting calls the pinned model and costs money: set yes to true to confirm")
	}
	if p.Provider == "" || p.Model == "" {
		return p, errors.New("provider and model are required: the model is pinned and recorded in the draft")
	}
	for f, v := range map[string]string{"provider": p.Provider, "model": p.Model} {
		if !calibrateNamePattern.MatchString(v) || strings.Contains(v, "..") {
			return p, fmt.Errorf("%s %q is not a valid name", f, v)
		}
	}
	if err := checkID("key_id", p.KeyID, false); err != nil {
		return p, err
	}
	if p.MaxUSD < 0 || math.IsNaN(p.MaxUSD) || math.IsInf(p.MaxUSD, 0) {
		return p, errors.New("max_usd must be 0 (default cap) or a positive amount")
	}
	if p.PerRule < 0 || p.PerRule > apptasks.MaxPerRule {
		return p, fmt.Errorf("per_rule must be between 0 (default 2) and %d", apptasks.MaxPerRule)
	}
	return p, nil
}

func validateAppTasksGenerate(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	p, err := parseAppTasksParams(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	if env.Hosted {
		if err := appTasksOrgKey(ctx, env, p); err != nil {
			return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
		}
	}
	return p, p.Draft, nil
}

// appTasksOrgKey checks, when the job is created on the hosted server, that
// the organization has a working key for the drafting model's provider, so
// a missing key is refused at once instead of when the job runs (fix wave
// 3, N238). It wraps calibrateOrgKey and rewords only its "no usable key"
// refusal, which names the judge although this is the drafting model.
func appTasksOrgKey(ctx context.Context, env jobEnv, p appTasksParams) error {
	_, err := calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, p.Provider, p.KeyID)
	if err != nil && p.KeyID == "" && strings.HasPrefix(err.Error(), "the org has no usable ") {
		return fmt.Errorf("Your organization has no working %s model key. Add one on the Models page.", p.Provider)
	}
	return err
}

// appTasksStore is the project's draft folder.
func appTasksStore(root string) apptasks.Store {
	return apptasks.Store{Dir: filepath.Join(root, filepath.FromSlash(apptasks.Folder))}
}

func runAppTasksGenerate(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	p, err := parseAppTasksParams(j.Params)
	if err != nil {
		return nil, err
	}
	if env.ProjectRoot == "" {
		return nil, errors.New("no project folder on this server")
	}
	st := appTasksStore(env.ProjectRoot)
	unlock, err := st.TryLock(p.Draft)
	if err != nil {
		return nil, err
	}
	defer unlock()
	d, err := st.Load(p.Draft)
	if err != nil {
		return nil, err
	}
	m, _, err := st.Mock(d)
	if err != nil {
		return nil, fmt.Errorf("the draft's OpenAPI file: %w", err)
	}
	_, policy, err := st.Files(d)
	if err != nil {
		return nil, err
	}
	if p.Stage == "tasks" && d.Status != apptasks.StatusRulesApproved && d.Status != apptasks.StatusGenerated {
		return nil, errors.New("approve the policy rules first: a person checks the rule list once before tasks are drafted")
	}
	if p.Stage == "rules" && d.Status == apptasks.StatusWritten {
		return nil, errors.New("tasks from this draft are already in the benchmark; start a new draft")
	}
	pin := apptasks.Pinned{Provider: p.Provider, Model: p.Model}
	budget := &apptasks.Budget{MaxCalls: localAppTasksMaxCalls, MaxUSD: p.MaxUSD}
	if env.App != nil && env.App.prices != nil {
		budget.Price = graders.PricesFrom(env.App.prices.Lookup)
	}
	if env.Hosted {
		budget.MaxCalls, budget.RequirePrice = hostedAppTasksMaxCalls, true
		if budget.MaxUSD == 0 || budget.MaxUSD > hostedAppTasksMaxUSD {
			budget.MaxUSD = hostedAppTasksMaxUSD
		}
	}
	res := &appTasksResult{Draft: d.ID, Stage: p.Stage, Model: pin.String(), CapCalls: budget.MaxCalls, CapUSD: budget.MaxUSD}

	var orgKey *keystore.Key
	if env.Hosted {
		if orgKey, err = calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, p.Provider, p.KeyID); err != nil {
			return nil, err
		}
		res.KeyID = orgKey.ID
		if orgKey.BudgetUSD > 0 && orgKey.MonthSpentUSD+budget.MaxUSD > orgKey.BudgetUSD {
			left := orgKey.BudgetUSD - orgKey.MonthSpentUSD
			if left <= 0 {
				return nil, fmt.Errorf("key %s spent its month budget ($%.2f): raise it on /models", orgKey.ID, orgKey.BudgetUSD)
			}
			budget.MaxUSD, res.CapUSD = left, left
		}
	}
	gw, url, closeGW, err := calibrateGatewayFor(ctx, env)
	if err != nil {
		return nil, err
	}
	defer closeGW()
	expID := "apptasks"
	if env.Hosted {
		cg, ok := gw.(calibrateGateway)
		if !ok {
			return nil, errors.New("this server's gateway can not route calls to the org's own key")
		}
		value, err := env.Services.Keys.Reveal(ctx, env.OrgID, orgKey.ID)
		if err != nil || value == "" {
			return nil, fmt.Errorf("read key %s: it can not be decrypted; replace it on /models", orgKey.ID)
		}
		cred := schemas.ProviderCredential{KeyID: orgKey.ID, Provider: orgKey.Provider, BaseURL: orgKey.BaseURL, Models: orgKey.Models, Value: value}
		if err := cg.ApplyOrgCredentials(ctx, env.OrgID, []schemas.ProviderCredential{cred}); err != nil {
			return nil, fmt.Errorf("register the org key with the gateway: %w", err)
		}
		expID = "apptasks-" + calibrateOrgSafe(env.OrgID)
		cg.BindExperimentOrg(expID, env.OrgID)
	}
	runID := "r_gen" + strings.TrimPrefix(utils.NewID("x", 10), "x_")
	key, err := gw.MintKey(ctx, &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: expID},
		Agent: &schemas.AgentConfig{ID: "apptasks", Model: p.Model, Gateway: schemas.GatewayPolicy{Provider: p.Provider}},
	})
	if err != nil {
		return nil, fmt.Errorf("mint the generator's key: %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := gw.RevokeKey(rctx, key); err != nil {
			log.Warnf("generator key not revoked; the janitor removes it: %v", err)
		}
	}()
	a := &apptasks.Assistant{
		Chat:   &apptasks.GatewayChat{URL: url, Key: key.Value, Model: pin, HTTP: &http.Client{Timeout: 3 * time.Minute}},
		Model:  pin,
		Budget: budget,
		Log:    log.Printf,
	}
	log.Printf("draft %s, stage %s, model %s, cap %d call(s) and $%.2f", d.ID, p.Stage, pin, budget.MaxCalls, budget.MaxUSD)
	callsBefore := len(d.Calls)
	var runErr error
	switch p.Stage {
	case "rules":
		runErr = apptasks.DraftRules(ctx, a, d, m, policy)
	case "tasks":
		runErr = apptasks.GenerateTasks(ctx, a, d, m, policy, apptasks.GenerateOptions{PerRule: p.PerRule, Judged: p.Judged})
	}
	calls, usd := budget.Spent()
	res.Calls, res.SpentUSD = calls, usd
	if orgKey != nil && usd > 0 {
		if err := env.Services.Keys.AddSpend(ctx, env.OrgID, orgKey.ID, usd); err != nil {
			log.Warnf("add $%.4f to key %s's month spend: %v", usd, orgKey.ID, err)
		} else {
			log.Printf("$%.4f added to key %s's month spend (%d call(s))", usd, orgKey.ID, calls)
		}
	}
	if err := st.Save(d); err != nil {
		return nil, fmt.Errorf("save the draft: %w", err)
	}
	if runErr != nil {
		return nil, runErr
	}
	res.Status, res.Rules, res.Tasks, res.Dropped, res.Dropouts = d.Status, len(d.Rules), len(d.Plans), len(d.Dropped), d.Dropped
	res.Records = d.Calls[callsBefore:]
	switch p.Stage {
	case "rules":
		log.Printf("%d rule(s) drafted: review and approve them before tasks are drafted", len(d.Rules))
	default:
		log.Printf("%d task(s) proved against the app copy, %d dropped; write them into %s, then run the readiness checks", len(d.Plans), len(d.Dropped), d.Suite)
	}
	return res, nil
}
